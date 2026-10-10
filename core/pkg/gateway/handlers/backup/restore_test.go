package backup

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

func TestRestoreHandler_round_trip_onto_another_cluster(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	blob := backupFrom(t, src, pub)
	if bytes.Contains(blob, []byte("sk_live_1")) || bytes.Contains(blob, testDB) {
		t.Fatal("the sealed backup carries plaintext")
	}

	dst := newRig(t, destRoot, testNamespace, true)
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, blob, priv, destRoot))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	var resp RestoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Pins != 2 || resp.Secrets != 2 || resp.SecretsWithoutRow != 0 || resp.RQLiteBytes != len(testDB) {
		t.Fatalf("response %+v", resp)
	}
	if len(dst.snap.loaded) != 1 || !bytes.Equal(dst.snap.loaded[0], testDB) {
		t.Fatal("snapshot was not loaded as backed up")
	}
	if strings.Join(dst.pins.sorted(), ",") != testCIDa+","+testCIDb {
		t.Fatalf("pinned %v", dst.pins.pinned)
	}
	got := decryptOps(t, secretBatch(t, dst), destRoot)
	want := map[string]string{
		"UPDATE function_secrets SET encrypted_value = ? WHERE id = ? [1000000]":      "sk_live_1",
		"UPDATE namespace_webrtc_config SET turn_shared_secret = ? WHERE id = ? [w1]": "legacy-plain",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q (all: %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("extra writes: %v", got)
	}
	if strings.Join(src.audit.actions, ",") != auth.AuditNamespaceBackedUp ||
		strings.Join(dst.audit.actions, ",") != auth.AuditNamespaceRestored {
		t.Fatalf("audit: src %v dst %v", src.audit.actions, dst.audit.actions)
	}
}

// secretBatch is the batch of UPDATEs that wrote the restored secrets.
func secretBatch(t *testing.T, r *rig) []rqlite.BatchOp {
	t.Helper()
	for _, b := range r.db.batches {
		if len(b) > 0 && strings.HasPrefix(b[0].SQL, "UPDATE ") {
			return b
		}
	}
	t.Fatalf("no batch wrote secrets: %v", r.db.batches)
	return nil
}

// decryptOps maps each UPDATE (with its ids) to its value opened under root.
// A value the destination cannot open fails the test.
func decryptOps(t *testing.T, ops []rqlite.BatchOp, root secrets.Root) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, op := range ops {
		col := ""
		for _, c := range secrets.NamespaceColumns() {
			if strings.HasPrefix(op.SQL, "UPDATE "+c.Table+" SET "+c.Column+" ") {
				col = c.Purpose
			}
		}
		ks, err := root.Keyset(col)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := ks.Decrypt(op.Args[0].(string))
		if err != nil {
			t.Fatalf("%s: destination cannot open the value: %v", op.SQL, err)
		}
		ids := make([]string, 0, len(op.Args)-1)
		for _, a := range op.Args[1:] {
			ids = append(ids, a.(string))
		}
		out[op.SQL+" ["+strings.Join(ids, ",")+"]"] = plain
	}
	return out
}

func TestRestoreHandler_wrong_restore_key_writes_nothing(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	// Sealed to some other cluster's restore key.
	body := restoreBody(t, backupFrom(t, src, pub), priv, secrets.Root{CurrentIKM: "another cluster"})

	dst := newRig(t, destRoot, testNamespace, true)
	rec := do(dst.h.RestoreHandler, http.MethodPost, body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "restore-key") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestRestoreHandler_corrupt_or_truncated_writes_nothing(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	body := restoreBody(t, backupFrom(t, src, pub), priv, destRoot)

	flipped := append([]byte(nil), body...)
	flipped[len(flipped)-1] ^= 1
	for name, b := range map[string][]byte{
		"truncated": body[:len(body)-3], "flipped": flipped, "empty": nil, "garbage": []byte("hello"),
	} {
		dst := newRig(t, destRoot, testNamespace, true)
		if rec := do(dst.h.RestoreHandler, http.MethodPost, b); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		assertNothingWritten(t, dst)
	}
}

func TestRestoreHandler_namespace_mismatch_is_refused(t *testing.T) {
	req := nsbackup.RestoreRequest{Namespace: "other", RQLite: testDB}
	body, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	dst := newRig(t, destRoot, testNamespace, true)
	if rec := do(dst.h.RestoreHandler, http.MethodPost, body); rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestRestoreHandler_refuses_callers_who_are_not_the_owner(t *testing.T) {
	for name, dst := range map[string]*rig{
		"admin":           newRig(t, destRoot, testNamespace, false),
		"other namespace": newRig(t, destRoot, "other", true),
	} {
		if rec := do(dst.h.RestoreHandler, http.MethodPost, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		assertNothingWritten(t, dst)
	}
}

func TestRestoreHandler_empty_pins_and_secrets(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// The write batches are the destination's quota put back and the scrub of
	// the loaded image (plaintext keys, other namespaces' ownership rows).
	if len(dst.snap.loaded) != 1 || len(dst.db.batches) != 3 || len(dst.pins.pinned) != 0 {
		t.Fatalf("loads=%d batches=%d pins=%v", len(dst.snap.loaded), len(dst.db.batches), dst.pins.pinned)
	}
}

func TestRestoreHandler_counts_secrets_without_a_row(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.noRowFrom = 1
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	var resp RestoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.SecretsWithoutRow != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRestoreHandler_pin_failure_is_reported(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.pins.fail = errBoom
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "again is safe") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(dst.audit.actions) != 0 {
		t.Fatal("a failed restore was recorded as a success")
	}
}

func TestWriteSecrets_splits_into_atomic_batches(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	ops := make([]rqlite.BatchOp, rqlite.MaxBatchOps+1)
	missing, err := dst.h.writeBatches(t.Context(), ops)
	if err != nil || missing != 0 {
		t.Fatal(missing, err)
	}
	if len(dst.db.batches) != 2 || len(dst.db.batches[0]) != rqlite.MaxBatchOps || len(dst.db.batches[1]) != 1 {
		t.Fatalf("batches %d", len(dst.db.batches))
	}
}

func TestBackupHandler_refuses_a_bad_key_and_non_owners(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	for _, body := range []string{`{"public_key":"abcd"}`, `not json`, `{}`} {
		if rec := do(src.h.BackupHandler, http.MethodPost, []byte(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, rec.Code)
		}
	}
	admin := newRig(t, sourceRoot, testNamespace, false)
	if rec := do(admin.h.BackupHandler, http.MethodPost, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("admin: %d", rec.Code)
	}
	if rec := do(src.h.BackupHandler, http.MethodGet, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestBackupHandler_fails_on_a_secret_it_cannot_decrypt(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	src.db.rows["function_secrets"] = []map[string]any{
		{"id": "1", "encrypted_value": sealUnder(t, destRoot, "orama-secrets-encryption-v1", "x")},
	}
	pub, _ := ownerKey(t)
	body, _ := json.Marshal(BackupRequest{PublicKey: hex.EncodeToString(pub[:])})
	if rec := do(src.h.BackupHandler, http.MethodPost, body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestBackupHandler_cannot_be_opened_by_the_cluster(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	pub, _ := ownerKey(t)
	blob := backupFrom(t, src, pub)
	_, clusterPriv, err := nsbackup.RestoreKey(sourceRoot, testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nsbackup.Open(clusterPriv, blob); err != nsbackup.ErrNotForKey {
		t.Fatalf("the cluster's own key opened the backup: %v", err)
	}
}

func TestRestoreKeyHandler_returns_the_key_restores_are_sealed_to(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, false)
	rec := do(dst.h.RestoreKeyHandler, http.MethodGet, nil)
	var resp RestoreKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want, _, err := nsbackup.RestoreKey(destRoot, testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if resp.PublicKey != hex.EncodeToString(want[:]) || resp.Namespace != testNamespace {
		t.Fatalf("%+v", resp)
	}
	other := newRig(t, destRoot, "other", true)
	if rec := do(other.h.RestoreKeyHandler, http.MethodGet, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("other namespace: %d", rec.Code)
	}
}

func TestNew_requires_every_dependency(t *testing.T) {
	ok := Config{Namespace: testNamespace, DB: &fakeDB{}, Registry: &fakeDB{}, Snapshots: &fakeSnap{}, Pins: &fakePins{},
		Root: func() secrets.Root { return destRoot }, ReplicationFactor: 3,
		Caller: func(*http.Request) (string, bool) { return "", false }, Audit: &fakeAudit{}, Logger: zap.NewNop()}
	if _, err := New(ok); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"namespace": func(c *Config) { c.Namespace = "" },
		"db":        func(c *Config) { c.DB = nil },
		"registry":  func(c *Config) { c.Registry = nil },
		"rf zero":   func(c *Config) { c.ReplicationFactor = 0 },
		"root":      func(c *Config) { c.Root = nil },
	} {
		c := ok
		mutate(&c)
		if _, err := New(c); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// unreadable fails the test if the handler reads any of it.
type unreadable struct{ t *testing.T }

func (u unreadable) Read([]byte) (int, error) {
	u.t.Helper()
	u.t.Error("the handler read the body of a request announced as over the limit")
	return 0, errors.New("unread")
}

func TestRestoreHandler_announced_oversize_is_refused_unread(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	r := httptest.NewRequest(http.MethodPost, "/", unreadable{t})
	r.ContentLength = int64(MaxRestoreBytes) + 1
	rec := httptest.NewRecorder()
	dst.h.RestoreHandler(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestRestoreHandler_unannounced_oversize_is_refused_while_reading(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	r := httptest.NewRequest(http.MethodPost, "/", io.LimitReader(zeroReader{}, int64(MaxRestoreBytes)+1))
	r.ContentLength = -1
	rec := httptest.NewRecorder()
	dst.h.RestoreHandler(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
