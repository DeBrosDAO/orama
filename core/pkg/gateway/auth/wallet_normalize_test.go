package auth

import "testing"

// The same wallet reaches the gateway checksummed from one client and lowercase
// from another. Every row keyed by a wallet is matched by exact string
// equality, so both sides must normalise identically or a wallet ends up owning
// a namespace under a spelling no later login can find.
func TestNormalizeWallet(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0xAbCdEf0123456789", "0xabcdef0123456789"},
		{"0xabcdef0123456789", "0xabcdef0123456789"},
		{"  0xABCDEF0123456789  ", "0xabcdef0123456789"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeWallet(tc.in); got != tc.want {
			t.Errorf("NormalizeWallet(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Normalisation must be idempotent: an already-normalised address is left
// alone, so re-running it on stored values cannot drift.
func TestNormalizeWallet_IsIdempotent(t *testing.T) {
	once := NormalizeWallet("0xAbCdEf")
	if twice := NormalizeWallet(once); twice != once {
		t.Fatalf("normalising twice changed the value: %q then %q", once, twice)
	}
}
