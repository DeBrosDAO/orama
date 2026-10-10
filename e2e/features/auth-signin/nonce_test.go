//go:build e2e_fleet

package authsignin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// nonceBytes is the size of a gateway nonce before hex encoding.
const nonceBytes = 32

// TestVerify_challengeInvalidIsIdenticalForUnknownUsedExpired: an unknown
// nonce, a used one and an expired one get the same AUTH_CHALLENGE_INVALID
// body, so a caller learns nothing about which it was (docs/whitepaper/technical-reference/vol1/13-identity.md,
// "Signing in has its own"). The expired case waits out the five-minute nonce.
func TestVerify_challengeInvalidIsIdenticalForUnknownUsedExpired(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)

	// Expired: a message whose own expiry is pushed an hour out, so only the
	// nonce row's five minutes can refuse it.
	stale := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	lateReq := mutated(t, w, stale.Message, func(m *wallet.SIWEMessage) { m.ExpirationTime = m.IssuedAt.Add(time.Hour) })

	unknown := mutated(t, w, challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()}).Message,
		func(m *wallet.SIWEMessage) { m.Nonce = randomNonce(t) })
	unknownBody := expectRefusal(t, postJSON(t, c, gw.PathVerify, "", unknown), http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")

	used := signed(t, w, challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()}).Message)
	postJSON(t, c, gw.PathVerify, "", used).Expect(t, http.StatusOK)
	usedBody := expectRefusal(t, postJSON(t, c, gw.PathVerify, "", used), http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")

	expiresAt, err := time.Parse(time.RFC3339, stale.ExpiresAt)
	if err != nil {
		t.Fatalf("challenge expires_at %q: %v", stale.ExpiresAt, err)
	}
	if got := time.Until(expiresAt); got > challengeTTL {
		t.Fatalf("challenge expires in %s, more than the %s a nonce lives", got, challengeTTL)
	}
	waitUntil(t, expiresAt.Add(stalenessSlack), "the challenge's nonce to expire")
	expiredBody := expectRefusal(t, postJSON(t, c, gw.PathVerify, "", lateReq), http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")

	a, b, e := mustJSON(t, unknownBody), mustJSON(t, usedBody), mustJSON(t, expiredBody)
	if !bytes.Equal(a, b) || !bytes.Equal(a, e) {
		t.Fatalf("the three refusals differ, which tells a caller why:\nunknown %s\nused    %s\nexpired %s", a, b, e)
	}
}

// TestVerify_nonceBoundToItsWallet: another wallet cannot spend a nonce issued
// to someone else, even signing a message that names itself.
func TestVerify_nonceBoundToItsWallet(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	victim, thief := newWallet(t), newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: victim.Address()})
	stolen := mutated(t, thief, ch.Message, func(m *wallet.SIWEMessage) { m.Address = thief.Address() })
	expectRefusal(t, postJSON(t, c, gw.PathVerify, "", stolen), http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")
	if _, _, err := c.For(t).Verify(t.Context(), signed(t, victim, ch.Message)); err != nil {
		t.Fatalf("the thief's attempt spent the victim's nonce: %v", err)
	}
}

// TestVerify_nonceBoundToItsNamespace: a lobby nonce cannot be moved to a
// namespace by editing the message's resource.
func TestVerify_nonceBoundToItsNamespace(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	moved := mutated(t, w, ch.Message, func(m *wallet.SIWEMessage) {
		m.Resources = []string{"urn:orama:namespace:e2e-not-the-lobby"}
	})
	expectRefusal(t, postJSON(t, c, gw.PathVerify, "", moved), http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")
}

func randomNonce(t testing.TB) string {
	t.Helper()
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
