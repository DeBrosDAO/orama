package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

func TestEnsureStateDir_createsItPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "namespaces", "acme", "gateway")
	if err := ensureStateDir(dir); err != nil {
		t.Fatalf("ensureStateDir: %v", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir mode = %o, want 0700: it holds private keys", perm)
	}
}

// An existing directory keeps whatever mode it was created with unless it is
// tightened, and this one holds private keys.
func TestEnsureStateDir_tightensAnExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gateway")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureStateDir(dir); err != nil {
		t.Fatalf("ensureStateDir: %v", err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %o, want 0700", st.Mode().Perm())
	}
}

func TestEnsureStateDir_refusesNoneAndUnwritable(t *testing.T) {
	if err := ensureStateDir(""); err == nil {
		t.Error("a gateway with no state directory was allowed to start")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureStateDir(filepath.Join(blocker, "gateway")); err == nil {
		t.Error("an uncreatable state directory was reported ready")
	}
}

// bootstrapEncryptionRoot used to answer every failure with a copy of the
// cluster secret — the wrong key on a rotated cluster — or an empty root.
func TestBootstrapEncryptionRoot_failsInsteadOfFallingBack(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{StateDir: filepath.Join(blocker, "gateway"), ClusterSecret: "cluster-secret"}
	if h, err := bootstrapEncryptionRoot(cfg, nil); err == nil {
		t.Fatalf("returned a root it could not cache: %+v", h.Get())
	}

	cfg = &Config{StateDir: t.TempDir()}
	if _, err := bootstrapEncryptionRoot(cfg, nil); err == nil {
		t.Fatal("returned an empty root with no registry, no file and no cluster secret")
	}
}

// The root lands in the gateway's own state directory, seeded from the node's
// secrets/ copy that install wrote.
func TestBootstrapEncryptionRoot_cachesInTheStateDir(t *testing.T) {
	oramaDir := t.TempDir()
	seed := secrets.SecretsDir(oramaDir)
	if err := os.MkdirAll(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	const ikm = "abababababababababababababababababababababababababababababababab"
	if err := os.WriteFile(filepath.Join(seed, secrets.FileName), []byte(ikm), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(oramaDir, "data", "namespaces", "index", "gateway")
	if err := ensureStateDir(state); err != nil {
		t.Fatal(err)
	}

	h, err := bootstrapEncryptionRoot(&Config{DataDir: oramaDir, StateDir: state, ClusterSecret: "not-the-root"}, nil)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if h.Get().CurrentIKM != ikm {
		t.Errorf("root = %q, want the node's seeded root", h.Get().CurrentIKM)
	}
	if got, err := os.ReadFile(filepath.Join(state, secrets.FileName)); err != nil || string(got) != ikm {
		t.Errorf("the root was not cached in the gateway's state directory: %q, %v", got, err)
	}
}

// A gateway whose serverless init failed used to log a warning and serve: no
// auth service, so every /v1/auth/* route was a 404 while /health said 200.
func TestInitializeBackends_serverlessFailureIsFatal(t *testing.T) {
	oldOlric, oldIPFS := initOlricBackend, initIPFSBackend
	initOlricBackend = func(*logging.ColoredLogger, *Config, *Dependencies, client.NetworkClient) {}
	initIPFSBackend = func(*logging.ColoredLogger, *Config, *Dependencies) error { return nil }
	t.Cleanup(func() { initOlricBackend, initIPFSBackend = oldOlric, oldIPFS })

	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatal(err)
	}
	// No IPFS client and no database handle: serverless cannot start.
	cfg := &Config{ClientNamespace: "acme", StateDir: t.TempDir()}
	deps := &Dependencies{}

	err = initializeBackends(logger, cfg, deps, nil)
	if err == nil {
		t.Fatal("a gateway with no serverless engine and no auth service was allowed to start")
	}
	if !strings.Contains(err.Error(), "serverless") {
		t.Errorf("the error does not say what failed: %v", err)
	}
	if deps.AuthService != nil {
		t.Error("an auth service was built although serverless init failed")
	}
}

// A namespace gateway handed a rotated root used to swap to it and discard the
// cache write. At its next boot with the registry unreachable it came back on
// the previous root and could not open anything written in between. It now
// refuses the root it cannot persist, and the index's fan-out reports it.
func TestHandleInternalReencrypt_refusesARootItCannotPersist(t *testing.T) {
	const clusterSecret = "a cluster secret"
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := secrets.Root{CurrentID: "1", CurrentIKM: "old-root"}
	g := &Gateway{
		cfg:       &Config{ClusterSecret: clusterSecret, StateDir: filepath.Join(blocker, "gateway")},
		encHolder: secrets.NewHolder(previous),
	}

	body := `{"root":{"CurrentID":"2","CurrentIKM":"new-root","PreviousID":"1","PreviousIKM":"old-root"}}`
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/secrets/reencrypt", strings.NewReader(body))
	r.RemoteAddr = "10.0.0.7:41000"
	key, err := nodeauth.CoordinationKey(clusterSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeauth.SignCoordination(key, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	g.handleInternalReencrypt(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 for a root that could not be persisted: %s", w.Code, w.Body.String())
	}
	if got := g.encHolder.Get().CurrentIKM; got != previous.CurrentIKM {
		t.Errorf("the gateway switched to a root it could not persist (%q)", got)
	}
}
