package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
	"golang.org/x/crypto/nacl/box"
)

const (
	testNamespace = "myapp"
	testCIDa      = "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"
	testCIDb      = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
)

var (
	sourceRoot = secrets.Root{CurrentID: "1", CurrentIKM: strings.Repeat("s", 64)}
	destRoot   = secrets.Root{CurrentID: "1", CurrentIKM: strings.Repeat("d", 64)}
	testDB     = append([]byte("SQLite format 3\x00"), []byte("tables")...)
)

type fakeDB struct {
	rows      map[string][]map[string]any // by table
	queryErr  error
	batches   [][]rqlite.BatchOp
	noRowFrom int // ops at or past this global index match no row; -1 for none
	executed  int
}

func (f *fakeDB) Query(_ context.Context, dest any, query string, _ ...any) error {
	if f.queryErr != nil {
		return f.queryErr
	}
	for table, rows := range f.rows {
		if strings.Contains(query, "FROM "+table+" ") || strings.HasSuffix(query, "FROM "+table) {
			*dest.(*[]map[string]any) = rows
			return nil
		}
	}
	*dest.(*[]map[string]any) = nil
	return nil
}

func (f *fakeDB) Batch(_ context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	f.batches = append(f.batches, ops)
	res := &rqlite.BatchResult{Committed: true}
	for range ops {
		affected := int64(1)
		if f.noRowFrom >= 0 && f.executed >= f.noRowFrom {
			affected = 0
		}
		f.executed++
		res.Results = append(res.Results, rqlite.OpResult{Kind: rqlite.BatchOpExec, RowsAffected: affected})
	}
	return res, nil
}

type fakeSnap struct {
	db     []byte
	loaded [][]byte
}

func (f *fakeSnap) Backup(context.Context) ([]byte, error) { return f.db, nil }
func (f *fakeSnap) Load(_ context.Context, db []byte) error {
	f.loaded = append(f.loaded, db)
	return nil
}

type fakePins struct {
	pinned []string
	fail   error
}

func (f *fakePins) Pin(_ context.Context, cid, _ string, rf int) (*ipfs.PinResponse, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if rf != 3 {
		return nil, fmt.Errorf("replication factor %d", rf)
	}
	f.pinned = append(f.pinned, cid)
	return &ipfs.PinResponse{Cid: cid}, nil
}

type fakeAudit struct{ actions []string }

func (f *fakeAudit) RecordFromRequest(_ context.Context, _ *http.Request, e auth.AuditEvent) {
	f.actions = append(f.actions, e.Action)
}

type rig struct {
	h     *Handler
	db    *fakeDB
	snap  *fakeSnap
	pins  *fakePins
	audit *fakeAudit
}

func newRig(t *testing.T, root secrets.Root, callerNS string, owner bool) *rig {
	t.Helper()
	r := &rig{db: &fakeDB{rows: map[string][]map[string]any{}, noRowFrom: -1}, snap: &fakeSnap{db: testDB},
		pins: &fakePins{}, audit: &fakeAudit{}}
	h, err := New(Config{
		Namespace: testNamespace, DB: r.db, Snapshots: r.snap, Pins: r.pins,
		Root: func() secrets.Root { return root }, ReplicationFactor: 3,
		Caller: func(*http.Request) (string, bool) { return callerNS, owner },
		Audit:  r.audit, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.h = h
	return r
}

// sealUnder encrypts value as the source cluster stores it.
func sealUnder(t *testing.T, root secrets.Root, purpose, value string) string {
	t.Helper()
	ks, err := root.Keyset(purpose)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := ks.Encrypt(value)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func seedSource(t *testing.T, r *rig) {
	r.db.rows["ipfs_content_ownership"] = []map[string]any{{"cid": testCIDa}}
	r.db.rows["deployments"] = []map[string]any{
		{"content_cid": testCIDb, "build_cid": nil, "id": "dep1",
			"environment": sealUnder(t, sourceRoot, "orama-deployment-environment-v1", `{"API_KEY":"x"}`)},
	}
	r.db.rows["function_secrets"] = []map[string]any{
		{"id": float64(1000000), "encrypted_value": sealUnder(t, sourceRoot, "orama-secrets-encryption-v1", "sk_live_1")},
	}
	r.db.rows["namespace_webrtc_config"] = []map[string]any{{"id": "w1", "turn_shared_secret": "legacy-plain"}}
}

func do(h http.HandlerFunc, method string, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, "/", bytes.NewReader(body)))
	return rec
}

func backupFrom(t *testing.T, src *rig, pub *[32]byte) []byte {
	t.Helper()
	body, _ := json.Marshal(BackupRequest{PublicKey: hex.EncodeToString(pub[:])})
	rec := do(src.h.BackupHandler, http.MethodPost, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// restoreBody is what the operator's machine builds: open with the owner key,
// re-seal to the destination's restore key.
func restoreBody(t *testing.T, blob []byte, ownerPriv *[32]byte, dest secrets.Root) []byte {
	t.Helper()
	plain, err := nsbackup.Open(ownerPriv, blob)
	if err != nil {
		t.Fatal(err)
	}
	p, err := nsbackup.UnmarshalPayload(plain)
	if err != nil {
		t.Fatal(err)
	}
	destPub, _, err := nsbackup.RestoreKey(dest)
	if err != nil {
		t.Fatal(err)
	}
	req, err := p.Rewrap(destPub)
	if err != nil {
		t.Fatal(err)
	}
	b, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ownerKey(t *testing.T) (*[32]byte, *[32]byte) {
	t.Helper()
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func assertNothingWritten(t *testing.T, r *rig) {
	t.Helper()
	if len(r.snap.loaded) != 0 || len(r.db.batches) != 0 || len(r.pins.pinned) != 0 {
		t.Fatalf("wrote: loads=%d batches=%d pins=%v", len(r.snap.loaded), len(r.db.batches), r.pins.pinned)
	}
}

var errBoom = errors.New("boom")
