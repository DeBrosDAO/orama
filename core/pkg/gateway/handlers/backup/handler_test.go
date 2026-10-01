package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	testDB     = mustImage("CREATE TABLE notes (v TEXT)", "INSERT INTO notes VALUES ('a')")
)

type fakeDB struct {
	rows     map[string][]map[string]any // by table
	queryErr error
	batchErr error
	// used is SUM(size_bytes) before and after a load: [0] then [1].
	used      []int64
	usedReads int
	// batches holds the write batches (UPDATE / INSERT / DELETE) in order.
	batches   [][]rqlite.BatchOp
	noRowFrom int // UPDATEs at or past this index match no row; -1 for none
	updates   int
}

func (f *fakeDB) Query(_ context.Context, dest any, query string, _ ...any) error {
	if f.queryErr != nil {
		return f.queryErr
	}
	if query == storageUseQuery {
		var used int64
		if f.usedReads < len(f.used) {
			used = f.used[f.usedReads]
		}
		f.usedReads++
		*dest.(*[]map[string]any) = []map[string]any{{"used": float64(used)}}
		return nil
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
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	res := &rqlite.BatchResult{Committed: true}
	if len(ops) > 0 && ops[0].Kind == rqlite.BatchOpQuery {
		return res, nil
	}
	f.batches = append(f.batches, ops)
	for _, op := range ops {
		affected := int64(1)
		if strings.HasPrefix(op.SQL, "UPDATE ") {
			if f.noRowFrom >= 0 && f.updates >= f.noRowFrom {
				affected = 0
			}
			f.updates++
		}
		res.Results = append(res.Results, rqlite.OpResult{Kind: rqlite.BatchOpExec, RowsAffected: affected})
	}
	return res, nil
}

type fakeSnap struct {
	db        []byte
	backupErr error
	loadErr   error
	loaded    [][]byte
	// loadCtxErr is the context's error as Load saw it.
	loadCtxErr error
	// onLoad runs inside Load, before it answers.
	onLoad func()
}

func (f *fakeSnap) Backup(context.Context) ([]byte, error) { return f.db, f.backupErr }
func (f *fakeSnap) Load(ctx context.Context, db []byte) error {
	f.loaded = append(f.loaded, db)
	if f.onLoad != nil {
		f.onLoad()
	}
	f.loadCtxErr = ctx.Err()
	return f.loadErr
}

type fakePins struct {
	mu       sync.Mutex
	pinned   []string
	fail     error
	inFlight int
	peak     int
	// hang makes Pin wait for its context, to test the deadline.
	hang bool
	// entered, when set, is told each time a Pin starts; gate, when set,
	// holds every Pin until it is closed.
	entered chan struct{}
	gate    chan struct{}
}

func (f *fakePins) Pin(ctx context.Context, cid, _ string, rf int) (*ipfs.PinResponse, error) {
	f.mu.Lock()
	f.inFlight++
	f.peak = max(f.peak, f.inFlight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.fail != nil {
		return nil, f.fail
	}
	if rf != 3 {
		return nil, fmt.Errorf("replication factor %d", rf)
	}
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.pinned = append(f.pinned, cid)
	f.mu.Unlock()
	return &ipfs.PinResponse{Cid: cid}, nil
}

func (f *fakePins) sorted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.pinned...)
	sort.Strings(out)
	return out
}

type fakeAudit struct{ actions []string }

func (f *fakeAudit) RecordFromRequest(_ context.Context, _ *http.Request, e auth.AuditEvent) {
	f.actions = append(f.actions, e.Action)
}

type rig struct {
	h        *Handler
	db       *fakeDB
	registry *fakeDB
	snap     *fakeSnap
	pins     *fakePins
	audit    *fakeAudit
}

func newRig(t *testing.T, root secrets.Root, callerNS string, owner bool) *rig {
	t.Helper()
	r := &rig{db: &fakeDB{rows: map[string][]map[string]any{}, noRowFrom: -1},
		registry: &fakeDB{rows: map[string][]map[string]any{}, noRowFrom: -1}, snap: &fakeSnap{db: testDB},
		pins: &fakePins{}, audit: &fakeAudit{}}
	h, err := New(Config{
		Namespace: testNamespace, DB: r.db, Registry: r.registry, Snapshots: r.snap, Pins: r.pins,
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
	// Deployments are the registry's; the namespace's own database has none.
	r.registry.rows["deployments"] = []map[string]any{{"content_cid": testCIDb, "build_cid": nil}}
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
	return restoreBodyFor(t, blob, ownerPriv, dest, testNamespace)
}

// restoreBodyFor seals the secrets to dest's restore key for keyNamespace.
func restoreBodyFor(t *testing.T, blob []byte, ownerPriv *[32]byte, dest secrets.Root, keyNamespace string) []byte {
	t.Helper()
	plain, err := nsbackup.Open(ownerPriv, blob)
	if err != nil {
		t.Fatal(err)
	}
	p, err := nsbackup.UnmarshalPayload(plain)
	if err != nil {
		t.Fatal(err)
	}
	destPub, _, err := nsbackup.RestoreKey(dest, keyNamespace)
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

// buildImage is a real SQLite database file made by running stmts.
func buildImage(stmts ...string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "image-test")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "i.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func mustImage(stmts ...string) []byte {
	b, err := buildImage(stmts...)
	if err != nil {
		panic(err)
	}
	return b
}
