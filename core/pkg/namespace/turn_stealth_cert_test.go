package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// feat-124 — resolveTURNSCert semantics.
//
// On machines without a Caddyfile (tests, dev laptops) the Let's Encrypt
// branch fails fast with "failed to read Caddyfile", exercising exactly the
// fallback decision this function owns: primary domains degrade to a
// self-signed pair, the stealth domain must hard-fail instead.

func testSpawner(t *testing.T) *SystemdSpawner {
	t.Helper()
	return &SystemdSpawner{logger: zap.NewNop()}
}

func TestResolveTURNSCert_primaryFallsBackToSelfSigned(t *testing.T) {
	s := testSpawner(t)
	dir := t.TempDir()

	certPath, keyPath, err := s.resolveTURNSCert("ns-test", "turn.ns-test.example.com", "", "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("expected self-signed fallback, got error: %v", err)
	}
	if certPath != filepath.Join(dir, "turn-cert.pem") || keyPath != filepath.Join(dir, "turn-key.pem") {
		t.Errorf("unexpected fallback paths: %s / %s", certPath, keyPath)
	}
	if _, statErr := os.Stat(certPath); statErr != nil {
		t.Errorf("self-signed cert not written: %v", statErr)
	}
}

// TURNS cert fix: when the *.<base> wildcard cert exists, resolveTURNSCert must
// return it (browser-valid) BEFORE any Caddy provisioning or self-signed
// fallback — this is the core of the fix.
func TestResolveTURNSCert_prefersWildcard(t *testing.T) {
	storage := t.TempDir()
	s := &SystemdSpawner{logger: zap.NewNop(), caddyStorageDirOverride: storage}

	base := "orama-devnet.network"
	wcCert, wcKey := s.wildcardCertPaths(base)
	if err := os.MkdirAll(filepath.Dir(wcCert), 0o755); err != nil {
		t.Fatalf("mkdir wildcard dir: %v", err)
	}
	if err := os.WriteFile(wcCert, []byte("dummy-cert"), 0o600); err != nil {
		t.Fatalf("write wildcard cert: %v", err)
	}
	if err := os.WriteFile(wcKey, []byte("dummy-key"), 0o600); err != nil {
		t.Fatalf("write wildcard key: %v", err)
	}

	dir := t.TempDir()
	gotCert, gotKey, err := s.resolveTURNSCert("ns-test", "turn.ns-test."+base, base, "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("resolveTURNSCert: %v", err)
	}
	if gotCert != wcCert || gotKey != wcKey {
		t.Errorf("expected wildcard pair %s/%s, got %s/%s", wcCert, wcKey, gotCert, gotKey)
	}
	// Must NOT have fallen through to writing a self-signed pair.
	if _, statErr := os.Stat(filepath.Join(dir, "turn-cert.pem")); !os.IsNotExist(statErr) {
		t.Error("wildcard was available — must not generate a self-signed cert")
	}
}

// With a baseDomain set but the wildcard absent, resolveTURNSCert must fall
// through to the existing behavior (self-signed for the primary domain), never
// error out — locks the branch ordering wildcard -> provision -> self-signed.
func TestResolveTURNSCert_missingWildcardFallsThrough(t *testing.T) {
	s := &SystemdSpawner{logger: zap.NewNop(), caddyStorageDirOverride: t.TempDir()}
	dir := t.TempDir()
	certPath, _, err := s.resolveTURNSCert("ns-test", "turn.ns-test.example.com", "example.com", "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("expected self-signed fallback, got: %v", err)
	}
	if certPath != filepath.Join(dir, "turn-cert.pem") {
		t.Errorf("expected self-signed fallback path, got %s", certPath)
	}
}

func TestResolveTURNSCert_existingSelfSignedReused(t *testing.T) {
	s := testSpawner(t)
	dir := t.TempDir()

	first, _, err := s.resolveTURNSCert("ns-test", "", "", "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	info1, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat first cert: %v", err)
	}

	second, _, err := s.resolveTURNSCert("ns-test", "", "", "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	info2, err := os.Stat(second)
	if err != nil {
		t.Fatalf("stat second cert: %v", err)
	}
	if first != second || info1.ModTime() != info2.ModTime() {
		t.Error("existing self-signed pair was regenerated instead of reused")
	}
}

func TestResolveTURNSCert_stealthNeverFallsBackToSelfSigned(t *testing.T) {
	s := testSpawner(t)
	dir := t.TempDir()

	_, _, err := s.resolveTURNSCert("ns-test", "cdn-abc123def456.example.com", "", "203.0.113.7", dir, false)
	if err == nil {
		t.Fatal("stealth cert resolution must hard-fail without Let's Encrypt — a self-signed stealth cert is indistinguishable from being blocked")
	}
	if !strings.Contains(err.Error(), "cdn-abc123def456.example.com") {
		t.Errorf("error must name the stealth domain for the operator; got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "turn-cert.pem")); !os.IsNotExist(statErr) {
		t.Error("stealth failure must not write a self-signed pair")
	}
}

func TestResolveTURNSCert_noDomainNoFallbackErrors(t *testing.T) {
	s := testSpawner(t)
	_, _, err := s.resolveTURNSCert("ns-test", "", "", "203.0.113.7", t.TempDir(), false)
	if err == nil {
		t.Fatal("empty domain with self-signed disallowed must error")
	}
}

// Security (feat-124): the Caddyfile sink must refuse any domain that isn't a
// clean DNS name, so a crafted value can't break out of the generated block
// and inject Caddy directives.
func TestProvisionTURNCertViaCaddy_rejectsNonDNSName(t *testing.T) {
	bad := []string{
		"example.com {\n  reverse_proxy evil:1234\n}\n#",
		"has space.com",
		"UPPER.example.com",
		"nodots",
		"trailing-.example.com",
		"",
	}
	for _, d := range bad {
		if _, _, err := provisionTURNCertViaCaddy(d, "http://localhost:6001/v1/internal/acme", time.Second); err == nil {
			t.Errorf("provisionTURNCertViaCaddy(%q) accepted a non-DNS-name domain", d)
		}
	}
}

// feat-124 stealth cert reuse: the stealth TURNS host reuses Caddy's existing
// *.<base> wildcard cert instead of writing the Caddyfile (the orama-node
// service can't, ProtectSystem=strict). These pin the validation logic.

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

func TestCaddyWildcardCertPaths_shape(t *testing.T) {
	crt, key := caddyWildcardCertPaths("orama-devnet.network")
	wantCrt := "/var/lib/caddy/caddy/certificates/acme-v02.api.letsencrypt.org-directory/wildcard_.orama-devnet.network/wildcard_.orama-devnet.network.crt"
	if crt != wantCrt {
		t.Errorf("cert path = %q; want %q", crt, wantCrt)
	}
	if !strings.HasSuffix(key, "wildcard_.orama-devnet.network.key") {
		t.Errorf("key path = %q; want a wildcard .key", key)
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
	s := testSpawner(t)
	// Valid single-label host but the wildcard cert almost certainly does not
	// exist at the absolute Caddy storage path during tests → hard error
	// naming the path, never a self-signed fallback.
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
