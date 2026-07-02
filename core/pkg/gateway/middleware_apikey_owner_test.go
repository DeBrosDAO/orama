package gateway

import "testing"

// Bugboard follow-up: api_key ownership is stored HMAC-hashed, but the
// ownership middleware presented the RAW key — so an api_key-authed owner got
// a 403 on a namespace they own (blocking function deploy / push config).
// apiKeyOwnerCandidates returns the values to check: hashed-first, raw-fallback.

func TestApiKeyOwnerCandidates_apiKeyChecksHashedThenRaw(t *testing.T) {
	got := apiKeyOwnerCandidates("api_key", "ak_raw", "HASHED")
	if len(got) != 2 || got[0] != "HASHED" || got[1] != "ak_raw" {
		t.Errorf("api_key: want [HASHED ak_raw] (hashed first, raw legacy fallback); got %v", got)
	}
}

func TestApiKeyOwnerCandidates_walletUsedAsIs(t *testing.T) {
	got := apiKeyOwnerCandidates("wallet", "0xWALLET", "")
	if len(got) != 1 || got[0] != "0xWALLET" {
		t.Errorf("wallet must be used as-is (never hashed); got %v", got)
	}
}

func TestApiKeyOwnerCandidates_noHashAvailableFallsBackToRaw(t *testing.T) {
	// When hashing is unavailable/disabled (HashAPIKey returns the key
	// unchanged), don't duplicate — just check the raw value once.
	got := apiKeyOwnerCandidates("api_key", "ak_raw", "ak_raw")
	if len(got) != 1 || got[0] != "ak_raw" {
		t.Errorf("no-op hash must yield a single raw candidate; got %v", got)
	}
	got2 := apiKeyOwnerCandidates("api_key", "ak_raw", "")
	if len(got2) != 1 || got2[0] != "ak_raw" {
		t.Errorf("empty hash must yield a single raw candidate; got %v", got2)
	}
}
