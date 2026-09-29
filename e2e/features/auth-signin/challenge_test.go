//go:build e2e_fleet

package authsignin

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// nonceHexLength: the nonce is 32 random bytes, hex encoded.
const nonceHexLength = 64

// lobbyResource is what a lobby challenge's Resources name.
const lobbyResource = "urn:orama:namespace:" + lobby

// TestChallenge_messageShape checks the EIP-4361 message the gateway issues:
// its own domain, the wallet, a single-use nonce, a five-minute window and the
// namespace it signs in to (docs/AUTH.md#signing-in, "#the-lobby").
func TestChallenge_messageShape(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	w := newWallet(t)
	ch := challengeFor(t, harness.GW(t), gw.ChallengeRequest{Wallet: w.Address()})
	m, err := wallet.ParseSIWE(ch.Message)
	if err != nil {
		t.Fatalf("the gateway issued a message its own grammar cannot parse: %v\n%s", err, ch.Message)
	}
	u, err := url.Parse(f.State.GatewayURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(m.Domain, u.Hostname()) {
		t.Errorf("message domain %q, want the gateway's own host %q", m.Domain, u.Hostname())
	}
	if m.Chain != wallet.ChainEthereum || m.Address != w.Address() {
		t.Errorf("message is for %s %s, want ETH %s", m.Chain, m.Address, w.Address())
	}
	if _, err := hex.DecodeString(m.Nonce); err != nil || len(m.Nonce) != nonceHexLength || m.Nonce != ch.Nonce {
		t.Errorf("nonce %q (response %q): want %d hex characters, the same in both", m.Nonce, ch.Nonce, nonceHexLength)
	}
	if got := m.ExpirationTime.Sub(m.IssuedAt); got != challengeTTL {
		t.Errorf("message is valid for %s, want %s", got, challengeTTL)
	}
	if len(m.Resources) != 1 || m.Resources[0] != lobbyResource || ch.Namespace != lobby {
		t.Errorf("a challenge without a namespace must sign in to the lobby: resources %v, namespace %q", m.Resources, ch.Namespace)
	}
}

// TestChallenge_twoChallengesNeverShareANonce proves nonces are fresh per
// challenge, including for the same wallet.
func TestChallenge_twoChallengesNeverShareANonce(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	a := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	b := challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()})
	if a.Nonce == b.Nonce {
		t.Fatalf("two challenges for %s share nonce %s", w.Address(), a.Nonce)
	}
}

// TestChallenge_solanaMessage checks SIWS: chain_type SOL yields a Solana
// message for the base58 address (docs/AUTH.md#the-two-identities).
func TestChallenge_solanaMessage(t *testing.T) {
	t.Parallel()
	sol, err := wallet.NewSolana()
	if err != nil {
		t.Fatal(err)
	}
	ch := challengeFor(t, harness.GW(t), gw.ChallengeRequest{Wallet: sol.Address(), ChainType: "SOL"})
	m, err := wallet.ParseSIWE(ch.Message)
	if err != nil {
		t.Fatalf("SIWS message does not parse: %v", err)
	}
	if m.Chain != wallet.ChainSolana || m.Address != sol.Address() {
		t.Fatalf("message is for %s %s, want SOL %s", m.Chain, m.Address, sol.Address())
	}
}

// TestChallenge_unknownNamespace: a challenge for a namespace that does not
// exist answers NAMESPACE_UNKNOWN (404) naming it, and creates nothing
// ("resolving a name does not create it", docs/AUTH.md#where-this-is-all-kept).
func TestChallenge_unknownNamespace(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	name := "e2e-missing-" + strings.ToLower(newWallet(t).Address()[2:12])
	for i := 0; i < 2; i++ {
		resp := postJSON(t, c, gw.PathChallenge, "", gw.ChallengeRequest{Wallet: newWallet(t).Address(), Namespace: name})
		var body struct{ Error, Code, Namespace string }
		if err := resp.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if resp.Status != http.StatusNotFound || body.Code != "NAMESPACE_UNKNOWN" || body.Namespace != name || body.Error == "" {
			t.Fatalf("attempt %d: want 404 NAMESPACE_UNKNOWN for %s, got %d %s", i, name, resp.Status, resp.Body)
		}
	}
}

// TestChallenge_malformedRequestsRefused covers the request edge cases: wrong
// method, bad JSON, missing wallet, unknown chain. None may issue a message.
func TestChallenge_malformedRequestsRefused(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t).Address()
	cases := map[string]struct {
		req  gw.Req
		want int
	}{
		"GET":             {gw.Req{Path: gw.PathChallenge}, http.StatusMethodNotAllowed},
		"not JSON":        {jsonBody(gw.PathChallenge, `{"wallet":`), http.StatusBadRequest},
		"empty object":    {jsonBody(gw.PathChallenge, `{}`), http.StatusBadRequest},
		"wrong type":      {jsonBody(gw.PathChallenge, `{"wallet":42}`), http.StatusBadRequest},
		"unknown chain":   {jsonBody(gw.PathChallenge, `{"wallet":"`+w+`","chain_type":"BTC"}`), http.StatusBadRequest},
		"bad device id":   {jsonBody(gw.PathChallenge, `{"wallet":"`+w+`","device_id":"not a thumbprint"}`), http.StatusBadRequest},
		"over body limit": {jsonBody(gw.PathChallenge, `{"wallet":"`+w+`","purpose":"`+strings.Repeat("x", 70<<10)+`"}`), http.StatusBadRequest},
	}
	for name, tc := range cases {
		resp := c.MustSend(t, tc.req)
		if resp.Status != tc.want {
			t.Errorf("%s: want %d, got %d: %.300s", name, tc.want, resp.Status, resp.Body)
		}
	}
}

// TestChallenge_hostileWalletNeverServerError: a wallet string that is not an
// address is the client's mistake. The gateway must refuse it with a 4xx and
// never answer 5xx or issue a message for it (validate at the boundary).
func TestChallenge_hostileWalletNeverServerError(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	hostile := []string{
		"0x123", "not-a-wallet", "' OR 1=1 --", "../../etc/passwd",
		"0x" + strings.Repeat("g", 40), "‮0x0000000000000000000000000000000000000000",
		"0xabc\u0000def", "é" + strings.Repeat("a", 40), strings.Repeat("0x", 4000),
	}
	for _, wlt := range hostile {
		resp := postJSON(t, c, gw.PathChallenge, "", gw.ChallengeRequest{Wallet: wlt})
		if resp.Status < http.StatusBadRequest || resp.Status >= http.StatusInternalServerError {
			t.Errorf("wallet %q: want a 4xx refusal, got %d: %.300s", wlt, resp.Status, resp.Body)
		}
	}
}

// jsonBody is a POST with a raw JSON body.
func jsonBody(path, body string) gw.Req {
	return gw.Req{Method: http.MethodPost, Path: path, Body: []byte(body),
		Header: http.Header{"Content-Type": {"application/json"}}}
}
