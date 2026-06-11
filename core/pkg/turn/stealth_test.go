package turn

import (
	"regexp"
	"strings"
	"testing"
)

func TestStealthHostForNamespace_deterministic(t *testing.T) {
	a := StealthHostForNamespace("anchat-test", "orama-devnet.network")
	b := StealthHostForNamespace("anchat-test", "orama-devnet.network")
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "cdn-") || !strings.HasSuffix(a, ".orama-devnet.network") {
		t.Errorf("unexpected shape: %q", a)
	}
	// label = "cdn-" + 12 hex chars
	label := strings.SplitN(a, ".", 2)[0]
	if len(label) != len("cdn-")+stealthHostHashBytes*2 {
		t.Errorf("label %q has wrong length", label)
	}
}

func TestStealthHostForNamespace_namespaceNotLeaked(t *testing.T) {
	h := StealthHostForNamespace("anchat-test", "orama-devnet.network")
	if strings.Contains(h, "anchat") {
		t.Errorf("stealth host %q leaks the namespace name", h)
	}
}

func TestStealthHostForNamespace_distinctPerNamespace(t *testing.T) {
	a := StealthHostForNamespace("ns-a", "example.com")
	b := StealthHostForNamespace("ns-b", "example.com")
	if a == b {
		t.Fatalf("different namespaces produced the same stealth host %q", a)
	}
}

// TestStealthHostForNamespace_matchesDNSNameAllowlist guards the contract that
// the derived host always passes the Caddyfile DNS-name allowlist
// (pkg/namespace turn_cert.go dnsNamePattern) — a legitimate stealth domain
// must never be rejected by that defense-in-depth check. Mirrors the same
// conservative pattern here to avoid an import cycle.
func TestStealthHostForNamespace_matchesDNSNameAllowlist(t *testing.T) {
	dnsName := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	for _, ns := range []string{"anchat-test", "a", "ns-with-many-dashes", "x1y2z3"} {
		h := StealthHostForNamespace(ns, "orama-devnet.network")
		if !dnsName.MatchString(h) {
			t.Errorf("derived stealth host %q for ns %q fails the DNS-name allowlist", h, ns)
		}
	}
}
