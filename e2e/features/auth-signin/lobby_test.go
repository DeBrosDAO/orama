//go:build e2e_fleet

package authsignin

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// removedAuthRoutes were deleted (docs/SECURITY.md: simple-key, register and
// the Phantom flow). A removed route must not come back under any method.
var removedAuthRoutes = []gw.Req{
	{Method: http.MethodPost, Path: "/v1/auth/simple-key"},
	{Method: http.MethodPost, Path: "/v1/auth/register"},
	{Method: http.MethodPost, Path: "/v1/auth/phantom/session"},
	{Method: http.MethodGet, Path: "/v1/auth/phantom/session/e2e"},
	{Method: http.MethodPost, Path: "/v1/auth/phantom/complete"},
}

// TestRemovedEndpoints_goneForEveryCaller: with a credential the mux answers
// 404; without one the default policy asks for a credential first (401
// AUTH_MISSING), so an anonymous probe learns nothing either way.
func TestRemovedEndpoints_goneForEveryCaller(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	tok := signIn(t, c, newWallet(t), "").AccessToken
	for _, r := range removedAuthRoutes {
		anon := c.MustSend(t, r)
		expectRefusal(t, anon, http.StatusUnauthorized, "AUTH_MISSING")
		authed := r
		authed.Bearer = tok
		if resp := c.MustSend(t, authed); resp.Status != http.StatusNotFound {
			t.Errorf("%s %s with a credential: want 404, got %d: %.200s", r.Method, r.Path, resp.Status, resp.Body)
		}
	}
}

// TestLobby_reachesOnlyNamespaceCreation: a lobby session holds no grant, and
// the one thing it reaches is POST /v1/namespaces (docs/AUTH.md#the-lobby).
// Every other route refuses it with a 401/403 carrying {error, code, hint}.
func TestLobby_reachesOnlyNamespaceCreation(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	tok := signIn(t, c, newWallet(t), "").AccessToken
	probes := []gw.Req{
		jsonBody("/v1/rqlite/query", `{"sql":"SELECT 1"}`),
		jsonBody("/v1/cache/put", `{"dmap":"e2e","key":"k","value":"v"}`),
		jsonBody("/v1/pubsub/publish", `{"topic":"e2e","data_base64":"aGk="}`),
		{Path: "/v1/namespace/keys"},
		{Path: "/v1/namespace/members"},
		{Path: "/v1/audit"},
		{Path: "/v1/functions"},
		{Path: "/v1/deployments/list"},
		{Path: "/v1/operator/settings"},
	}
	for _, r := range probes {
		r.Bearer = tok
		resp := c.MustSend(t, r)
		// The token is valid, so the refusal is the scope gate's (a lobby
		// session holds no permission) or, on the operator routes, the
		// operator list's; never a 401, which would mean the token was not
		// read at all.
		code := resp.ErrorCode()
		if resp.Status != http.StatusForbidden || (code != "INSUFFICIENT_SCOPE" && code != "NOT_AN_OPERATOR") {
			t.Errorf("lobby token at %s %s: HTTP %d %s, want 403 INSUFFICIENT_SCOPE (or NOT_AN_OPERATOR): %.200s", r.Method, r.Path, resp.Status, code, resp.Body)
			continue
		}
		expectRefusal(t, resp, http.StatusForbidden, code)
	}
}

// TestAPIKey_lobbyHasNoKeys: exchanging a lobby signature for a key answers
// NAMESPACE_HAS_NO_KEYS (docs/AUTH.md, sign-in codes).
func TestAPIKey_lobbyHasNoKeys(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	w := newWallet(t)
	req := signed(t, w, challengeFor(t, c, gw.ChallengeRequest{Wallet: w.Address()}).Message)
	resp := postJSON(t, c, gw.PathAPIKey, "", map[string]string{"message": req.Message, "signature": req.Signature})
	if resp.Status != http.StatusForbidden || resp.ErrorCode() != "NAMESPACE_HAS_NO_KEYS" {
		t.Fatalf("want 403 NAMESPACE_HAS_NO_KEYS, got %d %s", resp.Status, resp.Body)
	}
}

// TestVerify_namespaceNotOwned: a wallet with no grant in another wallet's
// namespace cannot sign in to it, and nothing about the owner leaks.
func TestVerify_namespaceNotOwned(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	stranger := newWallet(t)
	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: stranger.Address(), Namespace: n.Name})
	for _, path := range []string{gw.PathVerify, gw.PathAPIKey} {
		if path == gw.PathAPIKey {
			ch = challengeFor(t, c, gw.ChallengeRequest{Wallet: stranger.Address(), Namespace: n.Name})
		}
		req := signed(t, stranger, ch.Message)
		resp := postJSON(t, c, path, "", map[string]string{"message": req.Message, "signature": req.Signature})
		var body struct{ Code, Namespace, Error string }
		if err := resp.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if resp.Status != http.StatusForbidden || body.Code != "NAMESPACE_NOT_OWNED" || body.Namespace != n.Name {
			t.Errorf("%s: want 403 NAMESPACE_NOT_OWNED for %s, got %d %s", path, n.Name, resp.Status, resp.Body)
		}
		if containsFold(string(resp.Body), n.Owner.Wallet.Address()) {
			t.Errorf("%s: the refusal names the owner's wallet", path)
		}
	}
}
