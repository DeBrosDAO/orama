package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/invite"
)

// A join used to need the gateway URL, the token and the certificate
// fingerprint transcribed separately, and the fingerprint was the one people
// left out — which silently dropped the join to trust-on-first-use.

func encodedInvite(t *testing.T) string {
	t.Helper()
	s, err := invite.Encode(invite.Invite{
		JoinURL:       "https://node1.example.com",
		Token:         strings.Repeat("a", 64),
		CAFingerprint: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return s
}

func TestApplyInvite_fillsInTheJoinAndTheFingerprint(t *testing.T) {
	f := &Flags{Token: encodedInvite(t)}

	if err := f.applyInvite(); err != nil {
		t.Fatalf("applyInvite: %v", err)
	}
	if f.JoinAddress != "https://node1.example.com" {
		t.Errorf("JoinAddress = %q", f.JoinAddress)
	}
	if f.CAFingerprint != strings.Repeat("b", 64) {
		t.Errorf("CAFingerprint = %q; without it the join falls back to trust-on-first-use", f.CAFingerprint)
	}
	if f.Token != strings.Repeat("a", 64) {
		t.Errorf("Token = %q, want the secret unwrapped from the invite", f.Token)
	}
}

// An operator naming a different gateway or a different fingerprint means it.
func TestApplyInvite_doesNotOverrideExplicitFlags(t *testing.T) {
	f := &Flags{
		Token:         encodedInvite(t),
		JoinAddress:   "https://other.example.com",
		CAFingerprint: "explicit",
	}

	if err := f.applyInvite(); err != nil {
		t.Fatalf("applyInvite: %v", err)
	}
	if f.JoinAddress != "https://other.example.com" {
		t.Errorf("JoinAddress = %q, want the explicit flag to win", f.JoinAddress)
	}
	if f.CAFingerprint != "explicit" {
		t.Errorf("CAFingerprint = %q, want the explicit flag to win", f.CAFingerprint)
	}
}

// A cluster that has not been upgraded yet still hands out bare tokens.
func TestApplyInvite_leavesABareTokenAlone(t *testing.T) {
	bare := strings.Repeat("cd", 32)
	f := &Flags{Token: bare, JoinAddress: "https://node1.example.com"}

	if err := f.applyInvite(); err != nil {
		t.Fatalf("a bare token must still work: %v", err)
	}
	if f.Token != bare {
		t.Errorf("Token = %q, want it unchanged", f.Token)
	}
	if f.CAFingerprint != "" {
		t.Errorf("a bare token carries no fingerprint, got %q", f.CAFingerprint)
	}
}

func TestApplyInvite_withNoToken(t *testing.T) {
	f := &Flags{}
	if err := f.applyInvite(); err != nil {
		t.Fatalf("a genesis install has no token: %v", err)
	}
}

// A mistyped invite must fail here, not produce a join attempt with a token
// that was never issued.
func TestApplyInvite_rejectsAnInvalidInvite(t *testing.T) {
	f := &Flags{Token: "orama1_not-valid-base64!!!"}
	if err := f.applyInvite(); err == nil {
		t.Fatal("an invalid invite must be refused")
	}
}
