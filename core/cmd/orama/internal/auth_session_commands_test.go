package cli

import "testing"

func TestDomainOf(t *testing.T) {
	for url, want := range map[string]string{
		"https://orama-devnet.network":                 "orama-devnet.network",
		"https://orama-devnet.network/v1/auth/whoami":  "orama-devnet.network",
		"http://localhost:10104/v1/auth/sessions":      "localhost",
		"https://ns-anchat.orama-devnet.network:8443/": "ns-anchat.orama-devnet.network",
		"": "",
	} {
		if got := domainOf(url); got != want {
			t.Errorf("domainOf(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestOr(t *testing.T) {
	if got := or("value", "fallback"); got != "value" {
		t.Errorf("or with a value = %q", got)
	}
	for _, empty := range []string{"", "   ", "\t"} {
		if got := or(empty, "fallback"); got != "fallback" {
			t.Errorf("or(%q) = %q, want the fallback", empty, got)
		}
	}
}

// The saved-credential menu read EOF without a terminal, so `auth login` in a
// HOME that already had a credential failed in every script (stagenet e2e,
// 2026-09-30), and --namespace was ignored until a choice was made.
func TestOffersSavedCredentials(t *testing.T) {
	for _, tc := range []struct {
		namespace   string
		interactive bool
		want        bool
	}{
		{"", true, true},
		{"  ", true, true},
		{"anchat", true, false},
		{"", false, false},
		{"anchat", false, false},
	} {
		if got := offersSavedCredentials(tc.namespace, tc.interactive); got != tc.want {
			t.Errorf("namespace %q interactive %v: %v, want %v", tc.namespace, tc.interactive, got, tc.want)
		}
	}
}
