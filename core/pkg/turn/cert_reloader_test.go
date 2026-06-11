package turn

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// feat-41: TURNS cert hot-reload lets a Caddy-renewed certificate be picked up
// without restarting the TURN server (a restart drops every active relay). These
// pin: initial load, in-process reload when the file changes, resilience (a bad
// reload keeps the previous cert serving), and the missing-file failure.

func writeTestCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := GenerateSelfSignedCert(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatalf("GenerateSelfSignedCert: %v", err)
	}
	return certPath, keyPath
}

func leafDER(t *testing.T, r *certReloader) []byte {
	t.Helper()
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if c == nil || len(c.Certificate) == 0 {
		t.Fatal("GetCertificate returned an empty certificate")
	}
	return c.Certificate[0]
}

func TestNewCertReloader_failsOnMissingFiles(t *testing.T) {
	if _, err := newCertReloader("/no/such/cert.pem", "/no/such/key.pem", zap.NewNop()); err == nil {
		t.Fatal("expected an error when the cert/key files do not exist")
	}
}

func TestCertReloader_loadsAndServesCert(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	r, err := newCertReloader(certPath, keyPath, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	if got := leafDER(t, r); len(got) == 0 {
		t.Fatal("served certificate has no leaf")
	}
}

func TestCertReloader_hotReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCert(t, dir)
	r, err := newCertReloader(certPath, keyPath, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	before := leafDER(t, r)

	// Renew: overwrite with a freshly-generated cert/key pair (different serial
	// + key → different leaf) and advance the mtime so the watcher detects it.
	if err := GenerateSelfSignedCert(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatalf("regenerate cert: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go r.watch(5*time.Millisecond, stop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !bytes.Equal(leafDER(t, r), before) {
			return // hot-reloaded — the served cert changed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("certificate was not hot-reloaded after the file changed")
}

func TestCertReloader_keepsOldCertOnReloadError(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	r, err := newCertReloader(certPath, keyPath, zap.NewNop())
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}
	before := leafDER(t, r)

	// Corrupt the cert file (simulates a half-written renewal).
	if err := os.WriteFile(certPath, []byte("not a pem cert"), 0o644); err != nil {
		t.Fatalf("corrupt cert: %v", err)
	}
	if err := r.reload(); err == nil {
		t.Fatal("expected reload to fail on a corrupt cert file")
	}

	// The previously-loaded cert must still be served (TURNS must not go down).
	if got := leafDER(t, r); !bytes.Equal(got, before) {
		t.Error("a failed reload must keep serving the previous certificate")
	}
}
