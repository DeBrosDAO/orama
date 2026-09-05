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
