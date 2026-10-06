package gateway

import "testing"

// Bugboard follow-up: api_key ownership is stored HMAC-hashed, but the
// ownership middleware presented the RAW key — so an api_key-authed owner got
// a 403 on a namespace they own (blocking function deploy / push config).
// principalIdentifierCandidates returns the values to check: hashed-first, raw-fallback.

func TestApiKeyOwnerCandidates_apiKeyChecksHashedThenRaw(t *testing.T) {
	got := principalIdentifierCandidates("api_key", "ak_raw", "HASHED")
	if len(got) != 2 || got[0] != "HASHED" || got[1] != "ak_raw" {
		t.Errorf("api_key: want [HASHED ak_raw] (hashed first, raw legacy fallback); got %v", got)
	}
}

// A wallet is never hashed. It may be case-normalised (bug-329), so assert the
// property that matters rather than the candidate count: the hashed form of the
// key must never appear among the values checked for a wallet owner.
func TestApiKeyOwnerCandidates_walletIsNeverHashed(t *testing.T) {
	got := principalIdentifierCandidates("wallet", "0xWALLET", "HASHED")
	if len(got) == 0 {
		t.Fatal("wallet must yield at least one candidate")
	}
	for _, c := range got {
		if c == "HASHED" {
			t.Errorf("wallet candidates must never include the hashed form; got %v", got)
		}
	}
	if got[len(got)-1] != "0xWALLET" {
		t.Errorf("the presented spelling must remain a candidate; got %v", got)
	}
}

func TestApiKeyOwnerCandidates_noHashAvailableFallsBackToRaw(t *testing.T) {
	// When hashing is unavailable/disabled (HashAPIKey returns the key
	// unchanged), don't duplicate — just check the raw value once.
	got := principalIdentifierCandidates("api_key", "ak_raw", "ak_raw")
	if len(got) != 1 || got[0] != "ak_raw" {
		t.Errorf("no-op hash must yield a single raw candidate; got %v", got)
	}
	got2 := principalIdentifierCandidates("api_key", "ak_raw", "")
	if len(got2) != 1 || got2[0] != "ak_raw" {
		t.Errorf("empty hash must yield a single raw candidate; got %v", got2)
	}
}

// A wallet stored before normalisation kept whatever case the client sent.
// Ownership lookups must therefore try the normalised spelling first and the
// presented one second, so those rows keep working until migration 042 has run.
func TestOwnerCandidates_WalletChecksTheNormalisedFormFirst(t *testing.T) {
	got := principalIdentifierCandidates("wallet", "0xAbCdEf", "")
	want := []string{"0xabcdef", "0xAbCdEf"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// An already-lowercase wallet needs only one candidate.
func TestOwnerCandidates_LowercaseWalletIsASingleCandidate(t *testing.T) {
	got := principalIdentifierCandidates("wallet", "0xabcdef", "")
	if len(got) != 1 || got[0] != "0xabcdef" {
		t.Fatalf("got %v, want a single normalised candidate", got)
	}
}
