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

	certPath, keyPath, err := s.resolveTURNSCert("ns-test", "turn.ns-test.example.com", "203.0.113.7", dir, true)
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

func TestResolveTURNSCert_existingSelfSignedReused(t *testing.T) {
	s := testSpawner(t)
	dir := t.TempDir()

	first, _, err := s.resolveTURNSCert("ns-test", "", "203.0.113.7", dir, true)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	info1, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat first cert: %v", err)
	}

	second, _, err := s.resolveTURNSCert("ns-test", "", "203.0.113.7", dir, true)
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

	_, _, err := s.resolveTURNSCert("ns-test", "cdn-abc123def456.example.com", "203.0.113.7", dir, false)
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
	_, _, err := s.resolveTURNSCert("ns-test", "", "203.0.113.7", t.TempDir(), false)
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
