package namespacecmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"golang.org/x/crypto/nacl/box"
)

var restoreTestDB = append([]byte("SQLite format 3\x00"), []byte("rows")...)

type restoreFixture struct {
	dir      string
	opts     restoreOptions
	destPriv *[32]byte
	calls    int
	got      nsbackup.RestoreRequest
	status   int
}

// newRestoreFixture writes a sealed backup of namespace "myapp" and its key
// file, and starts a gateway that records what it is sent.
func newRestoreFixture(t *testing.T) (*restoreFixture, gatewayTarget) {
	t.Helper()
	ownerPub, ownerPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	destPub, destPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := nsbackup.Payload{Namespace: "myapp", RQLite: restoreTestDB,
		Secrets: []nsbackup.Secret{{Table: "function_secrets", Column: "encrypted_value", IDs: []string{"1"}, Value: "sk"}}}
	plain, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := nsbackup.Seal(ownerPub, plain)
	if err != nil {
		t.Fatal(err)
	}
	f := &restoreFixture{dir: t.TempDir(), destPriv: destPriv, status: http.StatusOK}
	f.opts = restoreOptions{inPath: f.write(t, "b.orbk", blob), namespace: "myapp", destKey: hex.EncodeToString(destPub[:]),
		keyFile: f.write(t, "key", []byte(hex.EncodeToString(ownerPriv[:])+"\n"))}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		if r.URL.Path != "/v1/namespace/restore" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if f.got, err = nsbackup.UnmarshalRestoreRequest(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if f.status != http.StatusOK {
			http.Error(w, "gateway says no", f.status)
			return
		}
		_ = json.NewEncoder(w).Encode(backuphandlers.RestoreResponse{Namespace: "myapp", Secrets: 1, RQLiteBytes: len(restoreTestDB)})
	}))
	t.Cleanup(srv.Close)
	return f, gatewayTarget{url: srv.URL, token: "tok", client: srv.Client()}
}

func (f *restoreFixture) write(t *testing.T, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunRestore_sends_secrets_sealed_to_the_destination(t *testing.T) {
	f, gw := newRestoreFixture(t)
	var out bytes.Buffer
	if err := runRestore(context.Background(), &out, gw, f.opts); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || f.got.Namespace != "myapp" || !bytes.Equal(f.got.RQLite, restoreTestDB) {
		t.Fatalf("calls=%d got=%+v", f.calls, f.got)
	}
	secs, err := f.got.OpenSecrets(f.destPriv)
	if err != nil || len(secs) != 1 || secs[0].Value != "sk" {
		t.Fatalf("destination could not open the secrets: %v %+v", err, secs)
	}
	if !strings.Contains(out.String(), "Restored namespace myapp") {
		t.Fatalf("output %q", out.String())
	}
}

func TestRunRestore_wrong_key_sends_nothing(t *testing.T) {
	f, gw := newRestoreFixture(t)
	_, other, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.opts.keyFile = f.write(t, "other", []byte(hex.EncodeToString(other[:])))
	if err := runRestore(context.Background(), io.Discard, gw, f.opts); err == nil || !strings.Contains(err.Error(), "cannot be opened") {
		t.Fatalf("wrong key: %v", err)
	}
	if f.calls != 0 {
		t.Fatal("a request was sent")
	}
}

func TestRunRestore_corrupt_or_truncated_backup_sends_nothing(t *testing.T) {
	f, gw := newRestoreFixture(t)
	blob, err := os.ReadFile(f.opts.inPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"truncated": blob[:len(blob)-1], "empty": {}, "header only": blob[:5]} {
		f.opts.inPath = f.write(t, name, b)
		if err := runRestore(context.Background(), io.Discard, gw, f.opts); err == nil {
			t.Fatalf("%s: restored", name)
		}
	}
	if f.calls != 0 {
		t.Fatal("a request was sent")
	}
}

func TestRunRestore_namespace_mismatch_sends_nothing(t *testing.T) {
	f, gw := newRestoreFixture(t)
	f.opts.namespace = "other"
	if err := runRestore(context.Background(), io.Discard, gw, f.opts); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("mismatch: %v", err)
	}
	if f.calls != 0 {
		t.Fatal("a request was sent")
	}
}

func TestRunRestore_bad_keys_are_refused(t *testing.T) {
	f, gw := newRestoreFixture(t)
	bad := f.opts
	bad.destKey = "abcd"
	if err := runRestore(context.Background(), io.Discard, gw, bad); err == nil {
		t.Fatal("short --dest-key accepted")
	}
	bad = f.opts
	bad.keyFile = filepath.Join(f.dir, "missing")
	if err := runRestore(context.Background(), io.Discard, gw, bad); err == nil {
		t.Fatal("missing key file accepted")
	}
	if f.calls != 0 {
		t.Fatal("a request was sent")
	}
}

func TestRunRestore_gateway_refusal_is_reported(t *testing.T) {
	f, gw := newRestoreFixture(t)
	f.status = http.StatusConflict
	err := runRestore(context.Background(), io.Discard, gw, f.opts)
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "gateway says no") {
		t.Fatalf("refusal: %v", err)
	}
}

func TestFetchBackup_refuses_a_bad_key_before_calling(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	gw := gatewayTarget{url: srv.URL, token: "tok", client: srv.Client()}
	if _, err := fetchBackup(context.Background(), gw, "not hex"); err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
