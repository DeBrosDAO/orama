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

// TURNS cert fix: the TLS host must be a SINGLE-label subdomain of the base so
// the *.<base> wildcard cert covers it (the legacy two-label turn.ns-<ns>.<base>
// host can only present a browser-rejected self-signed cert).
func TestTLSHostForNamespace_singleLabelUnderBase(t *testing.T) {
	got := TLSHostForNamespace("anchat-test", "orama-devnet.network")
	want := "turn-anchat-test.orama-devnet.network"
	if got != want {
		t.Fatalf("TLSHostForNamespace = %q, want %q", got, want)
	}
	// The label under the base domain must contain no dots (single-label),
	// otherwise the wildcard would not cover it.
	label := strings.TrimSuffix(got, ".orama-devnet.network")
	if strings.Contains(label, ".") {
		t.Errorf("TLS host label %q is multi-label; wildcard cert would not cover it", label)
	}
}

// TLSHostFromLegacyTURNHost must derive the exact same single-label host from
// the legacy plain-TURN host, so the creds handler (which only has TURNDomain)
// and the DNS layer (which has ns+base) never disagree on the base domain.
func TestTLSHostFromLegacyTURNHost(t *testing.T) {
	legacy := "turn.ns-anchat-test.orama-devnet.network"
	got := TLSHostFromLegacyTURNHost(legacy)
	want := TLSHostForNamespace("anchat-test", "orama-devnet.network")
	if got != want {
		t.Errorf("TLSHostFromLegacyTURNHost(%q) = %q, want %q (must match TLSHostForNamespace)", legacy, got, want)
	}
	// Unexpected shapes return "" so the caller falls back to the legacy host.
	for _, bad := range []string{"", "turn-already-single.orama-devnet.network", "sfu.ns-x.base", "turnXns-x.base"} {
		if out := TLSHostFromLegacyTURNHost(bad); out != "" {
			t.Errorf("TLSHostFromLegacyTURNHost(%q) = %q, want \"\"", bad, out)
		}
	}
}
