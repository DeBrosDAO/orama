//go:build e2e_fleet

package authsignin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// TestVerify_lobbySessionShape: a real signature over the issued message buys
// a 15-minute access token and a refresh token, no API key in the lobby
// (docs/AUTH.md#signing-in, "#the-lobby").
func TestVerify_lobbySessionShape(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	s := signIn(t, c, w, "")
	if s.TokenType != "Bearer" || s.RefreshToken == "" || s.Namespace != lobby {
		t.Fatalf("session type %q, refresh set %v, namespace %q", s.TokenType, s.RefreshToken != "", s.Namespace)
	}
	if !strings.EqualFold(s.Subject, w.Address()) {
		t.Errorf("subject %q, want the wallet %s", s.Subject, w.Address())
	}
	if s.APIKey != "" {
		t.Error("a lobby sign-in was handed an API key: the lobby has no keys")
	}
	if d := time.Duration(s.ExpiresIn) * time.Second; d > accessTokenLifetime || d < accessTokenLifetime-lifetimeTolerance {
		t.Errorf("expires_in %s, want about %s", d, accessTokenLifetime)
	}
	_, claims := jwtClaims(t, s.AccessToken)
	if got := claimTime(t, claims, "exp").Sub(claimTime(t, claims, "iat")); got != accessTokenLifetime {
		t.Errorf("access token lives %s (exp-iat), want %s", got, accessTokenLifetime)
	}
	if claims["namespace"] != lobby || claims["sid"] == nil || claims["jti"] == nil {
		t.Errorf("access token claims lack namespace/sid/jti: %v", claims)
	}
	var who struct {
		Authenticated bool   `json:"authenticated"`
		Method        string `json:"method"`
		Subject       string `json:"subject"`
	}
	if err := whoami(t, c, s.AccessToken).Expect(t, http.StatusOK).Decode(&who); err != nil {
		t.Fatal(err)
	}
	if !who.Authenticated || who.Method != "jwt" || !strings.EqualFold(who.Subject, w.Address()) {
		t.Errorf("whoami says %+v", who)
	}
}

// TestVerify_refreshLivesThirtyDays reads the session list: the refresh token
// expires 30 days after it was issued, and the list never shows the token.
func TestVerify_refreshLivesThirtyDays(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	resp := c.MustSend(t, gw.Req{Path: gw.PathSessions, Bearer: s.AccessToken}).Expect(t, http.StatusOK)
	if strings.Contains(string(resp.Body), s.RefreshToken) {
		t.Fatal("GET /v1/auth/sessions returned the refresh token itself")
	}
	var out struct {
		Sessions []gw.SessionView `json:"sessions"`
	}
	if err := resp.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sessions) != 1 {
		t.Fatalf("a wallet signed in once has %d sessions: %s", len(out.Sessions), resp.Body)
	}
	created, err1 := parseDBTime(out.Sessions[0].CreatedAt)
	expires, err2 := parseDBTime(out.Sessions[0].ExpiresAt)
	if err1 != nil || err2 != nil {
		t.Fatalf("session times unreadable: %v %v", err1, err2)
	}
	if got := expires.Sub(created); got < refreshTokenLifetime-lifetimeTolerance || got > refreshTokenLifetime+lifetimeTolerance {
		t.Fatalf("refresh token lives %s, want %s", got, refreshTokenLifetime)
	}
}

// TestVerify_solanaSignIn: a SIWS signature (base64 Ed25519) signs in.
func TestVerify_solanaSignIn(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	sol, err := wallet.NewSolana()
	if err != nil {
		t.Fatal(err)
	}
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: sol.Address(), ChainType: "SOL"})
	s, resp, err := c.For(t).Verify(t.Context(), gw.VerifyRequest{Message: ch.Message, Signature: sol.Sign(ch.Message)})
	if err != nil {
		t.Fatalf("SIWS verify: %v", err)
	}
	if resp.Status != http.StatusOK || s.AccessToken == "" || !strings.EqualFold(s.Subject, sol.Address()) {
		t.Fatalf("SIWS sign-in answered %d subject %q", resp.Status, s.Subject)
	}
	other, err := wallet.NewSolana()
	if err != nil {
		t.Fatal(err)
	}
	ch2 := challengeFor(t, c, gw.ChallengeRequest{Wallet: sol.Address(), ChainType: "SOL"})
	bad := postJSON(t, c, gw.PathVerify, "", gw.VerifyRequest{Message: ch2.Message, Signature: other.Sign(ch2.Message)})
	expectRefusal(t, bad, http.StatusUnauthorized, "AUTH_SIGNATURE_INVALID")
}

// TestVerify_domainMismatch: a message naming another site is refused even
// with a valid signature and a live nonce (docs/AUTH.md#signing-in).
func TestVerify_domainMismatch(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	for _, domain := range []string{"evil.example", "e2e.evil.example", "localhost"} {
		req := mutated(t, w, ch.Message, func(m *wallet.SIWEMessage) { m.Domain = domain })
		expectRefusal(t, postJSON(t, c, gw.PathVerify, "", req), http.StatusUnauthorized, "AUTH_DOMAIN_MISMATCH")
	}
	// The refusals spent nothing: the genuine message still signs in.
	if _, _, err := c.For(t).Verify(t.Context(), signed(t, w, ch.Message)); err != nil {
		t.Fatalf("the genuine message no longer verifies after domain refusals: %v", err)
	}
}

// TestVerify_messageOutsideItsWindow: expired, issued in the future and not
// yet valid are all AUTH_MESSAGE_EXPIRED.
func TestVerify_messageOutsideItsWindow(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	now := time.Now().UTC().Truncate(time.Second)
	cases := map[string]func(*wallet.SIWEMessage){
		"expired": func(m *wallet.SIWEMessage) {
			m.IssuedAt, m.ExpirationTime = now.Add(-20*time.Minute), now.Add(-10*time.Minute)
		},
		"issued in the future": func(m *wallet.SIWEMessage) {
			m.IssuedAt, m.ExpirationTime = now.Add(10*time.Minute), now.Add(15*time.Minute)
		},
		"not yet valid": func(m *wallet.SIWEMessage) { m.NotBefore = now.Add(10 * time.Minute) },
	}
	for name, change := range cases {
		resp := postJSON(t, c, gw.PathVerify, "", mutated(t, w, ch.Message, change))
		if resp.Status != http.StatusUnauthorized || resp.ErrorCode() != "AUTH_MESSAGE_EXPIRED" {
			t.Errorf("%s: want 401 AUTH_MESSAGE_EXPIRED, got %d %s", name, resp.Status, resp.Body)
		}
	}
}

// TestVerify_malformedMessage: text that is not a Sign-In-With message, or one
// that names no namespace or two, is AUTH_MESSAGE_MALFORMED.
func TestVerify_malformedMessage(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	bodies := []gw.VerifyRequest{
		signed(t, w, "hello, please sign this"),
		signed(t, w, strings.Replace(ch.Message, "\n", "\r\n", -1)),
		mutated(t, w, ch.Message, func(m *wallet.SIWEMessage) { m.Resources = nil }),
		mutated(t, w, ch.Message, func(m *wallet.SIWEMessage) {
			m.Resources = append(m.Resources, "urn:orama:namespace:other")
		}),
	}
	for _, b := range bodies {
		expectRefusal(t, postJSON(t, c, gw.PathVerify, "", b), http.StatusUnauthorized, "AUTH_MESSAGE_MALFORMED")
	}
}

// TestVerify_signatureInvalid: another wallet's signature, a truncated one and
// garbage are AUTH_SIGNATURE_INVALID, and none of them spends the nonce.
func TestVerify_signatureInvalid(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	good := signed(t, w, ch.Message)
	sigs := []string{signed(t, newWallet(t), ch.Message).Signature, good.Signature[:len(good.Signature)-4],
		"0x00", "not hex at all", good.Signature + "00"}
	for _, sig := range sigs {
		expectRefusal(t, postJSON(t, c, gw.PathVerify, "", gw.VerifyRequest{Message: ch.Message, Signature: sig}),
			http.StatusUnauthorized, "AUTH_SIGNATURE_INVALID")
	}
	if _, _, err := c.For(t).Verify(t.Context(), good); err != nil {
		t.Fatalf("bad signatures spent the nonce: the genuine one no longer verifies: %v", err)
	}
}

// TestVerify_malformedRequests: wrong method, bad JSON, missing fields.
func TestVerify_malformedRequests(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	cases := map[string]struct {
		req  gw.Req
		want int
	}{
		"GET":               {gw.Req{Path: gw.PathVerify}, http.StatusMethodNotAllowed},
		"not JSON":          {jsonBody(gw.PathVerify, `{"message":`), http.StatusBadRequest},
		"missing signature": {jsonBody(gw.PathVerify, `{"message":"x"}`), http.StatusBadRequest},
		"missing message":   {jsonBody(gw.PathVerify, `{"signature":"0x00"}`), http.StatusBadRequest},
		"wrong types":       {jsonBody(gw.PathVerify, `{"message":1,"signature":true}`), http.StatusBadRequest},
	}
	for name, tc := range cases {
		if resp := c.MustSend(t, tc.req); resp.Status != tc.want {
			t.Errorf("%s: want %d, got %d: %.300s", name, tc.want, resp.Status, resp.Body)
		}
	}
}

// parseDBTime reads the registry's timestamps (RFC3339 or SQLite's format).
func parseDBTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}
