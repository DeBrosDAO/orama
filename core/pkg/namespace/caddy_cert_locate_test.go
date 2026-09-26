package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCert(t *testing.T, storage, issuer, name string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(storage, caddyCertificatesDir, issuer, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	crt := filepath.Join(dir, name+".crt")
	for _, p := range []string{crt, filepath.Join(dir, name+".key")} {
		if err := os.WriteFile(p, []byte("pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mod, mod)
	}
	return crt
}

// The ACME issuer is configurable, and Caddy names the storage directory after
// it; a hardcoded production path never found a staging certificate.
func TestLocateCaddyCert_FindsTheCertWhicheverIssuerHasIt(t *testing.T) {
	storage := t.TempDir()
	want := writeCert(t, storage, "acme-staging-v02.api.letsencrypt.org-directory", "wildcard_.stagenet.example", time.Now())
	crt, key := locateCaddyCert(storage, "wildcard_.stagenet.example")
	if crt != want || key != strings.TrimSuffix(want, ".crt")+".key" {
		t.Fatalf("got %s / %s, want %s", crt, key, want)
	}
}

func TestLocateCaddyCert_NewestWinsAfterACASwitch(t *testing.T) {
	storage := t.TempDir()
	writeCert(t, storage, "acme-staging-v02.api.letsencrypt.org-directory", "x.example", time.Now().Add(-48*time.Hour))
	want := writeCert(t, storage, "acme-v02.api.letsencrypt.org-directory", "x.example", time.Now())
	if crt, _ := locateCaddyCert(storage, "x.example"); crt != want {
		t.Fatalf("got %s, want the newest %s", crt, want)
	}
}

func TestLocateCaddyCert_MissingPointsAtTheDefaultIssuer(t *testing.T) {
	crt, _ := locateCaddyCert(t.TempDir(), "absent.example")
	if !strings.Contains(crt, caddyDefaultIssuerDir) {
		t.Errorf("a missing cert should name the default issuer's path, got %s", crt)
	}
	if _, err := os.Stat(crt); err == nil {
		t.Error("the path of a missing cert must not exist")
	}
}
