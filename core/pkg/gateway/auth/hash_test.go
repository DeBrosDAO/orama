package auth

import "testing"

func TestHashAPIKey_withSecret_differsFromRaw(t *testing.T) {
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	const raw = "ak_live_example:ns"
	got := s.HashAPIKey(raw)
	if got == raw {
		t.Fatal("hashed key must differ from raw when the HMAC secret is set")
	}
	if want := HmacSHA256Hex(raw, "test-hmac-secret"); got != want {
		t.Errorf("HashAPIKey = %q, want %q", got, want)
	}
}

func TestHashAPIKey_emptySecret_returnsRaw(t *testing.T) {
	// Current contract: no secret → store/lookup the raw key (rolling-upgrade
	// fallback). Production spawn refuses to boot without the secret file;
	// this pins the function so a later fail-loud change is explicit.
	s := &Service{}
	const raw = "ak_live_example:ns"
	if got := s.HashAPIKey(raw); got != raw {
		t.Errorf("empty secret must return the raw key, got %q", got)
	}
}
