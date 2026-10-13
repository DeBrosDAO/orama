package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func testSpawner(t *testing.T) *SystemdSpawner {
	t.Helper()
	return &SystemdSpawner{logger: zap.NewNop()}
}

// writeWildcard puts a *.<base> wildcard pair where the gateway exports it.
func writeWildcard(t *testing.T, s *SystemdSpawner, base string) (string, string) {
	t.Helper()
	crt, key := s.wildcardCertPaths()
	if err := os.MkdirAll(filepath.Dir(crt), 0o755); err != nil {
		t.Fatalf("mkdir wildcard dir: %v", err)
	}
	for _, p := range []string{crt, key} {
		if err := os.WriteFile(p, []byte("pem"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return crt, key
}

// The wildcard covers every TURNS host of the shared listener; it is the cert.
func TestResolveTURNSCert_usesTheWildcard(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), oramaDirOverride: t.TempDir()}
	base := "orama-devnet.network"
	wantCrt, wantKey := writeWildcard(t, s, base)

	crt, key, err := s.resolveTURNSCert(base)
	if err != nil {
		t.Fatalf("resolveTURNSCert: %v", err)
	}
	if crt != wantCrt || key != wantKey {
		t.Errorf("got %s/%s, want the wildcard %s/%s", crt, key, wantCrt, wantKey)
	}
}

// No wildcard is an error naming where it was looked for — never a self-signed
// pair, which clients reject.
func TestResolveTURNSCert_missingWildcardErrors(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), oramaDirOverride: t.TempDir()}

	_, _, err := s.resolveTURNSCert("example.com")
	if err == nil {
		t.Fatal("a missing wildcard must be an error")
	}
	if crt, _ := s.wildcardCertPaths(); !strings.Contains(err.Error(), crt) || !strings.Contains(err.Error(), "*.example.com") {
		t.Errorf("the error should name the missing certificate and where it was looked for; got: %v", err)
	}
}

// A cert without its key is not a usable pair.
func TestResolveTURNSCert_missingKeyErrors(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), oramaDirOverride: t.TempDir()}
	_, key := writeWildcard(t, s, "example.com")
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.resolveTURNSCert("example.com"); err == nil {
		t.Fatal("a wildcard cert without its key must be an error")
	}
}

func TestResolveTURNSCert_noBaseDomainErrors(t *testing.T) {
	if _, _, err := testSpawner(t).resolveTURNSCert(""); err == nil {
		t.Fatal("no base domain means no wildcard; it must be an error")
	}
}

// feat-124 stealth cert reuse: the stealth TURNS host reuses Caddy's existing
// *.<base> wildcard cert. These pin the validation logic.

func TestIsSingleLabelSubdomain(t *testing.T) {
	cases := []struct {
		host, base string
		want       bool
	}{
		{"cdn-a1b2c3d4e5f6.orama-devnet.network", "orama-devnet.network", true},
		{"turn.ns-anchat-test.orama-devnet.network", "orama-devnet.network", false}, // multi-label
		{"orama-devnet.network", "orama-devnet.network", false},                     // empty label
		{"cdn-x.other.network", "orama-devnet.network", false},                      // wrong base
		{"cdn-x.example.com", "example.com", true},
	}
	for _, c := range cases {
		if got := isSingleLabelSubdomain(c.host, c.base); got != c.want {
			t.Errorf("isSingleLabelSubdomain(%q, %q) = %v; want %v", c.host, c.base, got, c.want)
		}
	}
}

// The exported pair's path does not depend on the CA that issued it, so the
// TURN server's reloader keeps watching the right file across a change of CA.
func TestWildcardCertPaths_productionShape(t *testing.T) {
	crt, key := testSpawner(t).wildcardCertPaths()
	if crt != "/opt/orama/.orama/data/tls/wildcard.crt" || key != "/opt/orama/.orama/data/tls/wildcard.key" {
		t.Errorf("paths = %q, %q", crt, key)
	}
}

func TestResolveStealthCert_rejectsMultiLabelHost(t *testing.T) {
	s := testSpawner(t)
	// A host that needs *.ns-x.<base> (multi-label) is NOT covered by the
	// *.<base> wildcard — must error rather than present a mismatched cert.
	_, _, err := s.resolveStealthCert("turn.ns-x.orama-devnet.network", "orama-devnet.network")
	if err == nil {
		t.Fatal("multi-label host must be rejected (wildcard wouldn't cover it)")
	}
	if !strings.Contains(err.Error(), "single-label") {
		t.Errorf("error should explain the single-label requirement; got: %v", err)
	}
}

func TestResolveStealthCert_missingWildcardErrors(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), oramaDirOverride: t.TempDir()}
	// Valid single-label host but no exported wildcard → hard error naming
	// the path, never a self-signed fallback.
	_, _, err := s.resolveStealthCert("cdn-deadbeef0000.test-nonexistent-base.invalid", "test-nonexistent-base.invalid")
	if err == nil {
		t.Fatal("missing wildcard cert must hard-fail")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("error should reference the missing wildcard cert; got: %v", err)
	}
}

func TestResolveStealthCert_emptyBaseErrors(t *testing.T) {
	s := testSpawner(t)
	if _, _, err := s.resolveStealthCert("cdn-x.example.com", ""); err == nil {
		t.Fatal("empty base domain must error")
	}
}

// The stealth host reuses the same wildcard resolveTURNSCert does, from the
// same place.
func TestResolveStealthCert_usesTheWildcard(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), oramaDirOverride: t.TempDir()}
	wantCrt, wantKey := writeWildcard(t, s, "example.com")

	crt, key, err := s.resolveStealthCert("cdn-abc123.example.com", "example.com")
	if err != nil {
		t.Fatalf("resolveStealthCert: %v", err)
	}
	if crt != wantCrt || key != wantKey {
		t.Errorf("got %s/%s, want %s/%s", crt, key, wantCrt, wantKey)
	}
}
