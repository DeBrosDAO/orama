//go:build e2e_fleet

package authsignin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// pathSessionPolicy reads and sets a namespace's sign-in and device policy
	// (docs/AUTH.md#sign-in-policy).
	pathSessionPolicy = "/v1/namespace/session-policy"
	signInMembers     = "members"
	signInOpen        = "open"
	// signInPolicyBudget is how long a gateway may judge a refresh by a policy
	// it read before the change: the revocation list's bound plus the round
	// trip (docs/AUTH.md#how-long-a-change-takes-to-land).
	signInPolicyBudget = revocationStaleness + stalenessSlack
)

// setSignIn sets n's sign-in policy as its owner, and restores members when
// the test ends so a failure leaves nothing open.
func setSignIn(t testing.TB, n *ns.Namespace, policy string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"sign_in": policy})
	if err != nil {
		t.Fatal(err)
	}
	resp := n.Owner.Client.MustSend(t, gw.Req{Method: http.MethodPut, Path: pathSessionPolicy, Bearer: n.Owner.Token(),
		Header: http.Header{"Content-Type": {"application/json"}}, Body: body})
	var out map[string]any
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSignInPolicy_openLetsAGrantlessWalletInAndClosingEndsIt walks the opt-in:
// a wallet with no grant is refused a members namespace (NAMESPACE_NOT_OWNED);
// once the owner opens sign-in it gets a session and no key, is refused what a
// grantless wallet may not do, and when the owner closes it again its refresh
// and any new sign-in are refused (docs/AUTH.md#sign-in-policy).
func TestSignInPolicy_openLetsAGrantlessWalletInAndClosingEndsIt(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	t.Cleanup(func() { setSignIn(t, n, signInMembers) })
	c := harness.GW(t)
	endUser := newWallet(t)

	ch := challengeFor(t, c, gw.ChallengeRequest{Wallet: endUser.Address(), Namespace: n.Name})
	expectRefusal(t, postJSON(t, c, gw.PathVerify, "", signed(t, endUser, ch.Message)), http.StatusForbidden, "NAMESPACE_NOT_OWNED")

	if got := setSignIn(t, n, signInOpen); got["sign_in"] != signInOpen || got["device_policy"] == nil {
		t.Fatalf("opening sign-in answered %v", got)
	}
	var session *gw.Session
	eventually.Require(t, pollEvery, signInPolicyBudget, "the open namespace to admit a grantless wallet", func() (bool, error) {
		s, err := c.For(t).SignIn(t.Context(), endUser, n.Name, nil)
		session = s
		return err == nil, err
	})
	if session.APIKey != "" {
		t.Errorf("an end user's sign-in handed back a key: %q", session.APIKey)
	}

	// The session is the wallet's whole credential: it reaches what a grantless
	// wallet reaches and not the control plane or a write the role withholds.
	if resp := n.Owner.Client.MustSend(t, gw.Req{Path: "/v1/namespace/keys", Bearer: session.AccessToken}); resp.Status != http.StatusForbidden {
		t.Errorf("an end user listed the namespace's keys: %d %.200s", resp.Status, resp.Body)
	}
	if resp := n.Owner.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/pubsub/publish", Bearer: session.AccessToken,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"topic":"e2e","data_base64":"aGk="}`)}); resp.Status != http.StatusForbidden {
		t.Errorf("an end user published to a topic: %d %.200s", resp.Status, resp.Body)
	}
	req := signed(t, endUser, challengeFor(t, c, gw.ChallengeRequest{Wallet: endUser.Address(), Namespace: n.Name}).Message)
	expectRefusal(t, postJSON(t, c, gw.PathAPIKey, "", map[string]string{"message": req.Message, "signature": req.Signature}),
		http.StatusForbidden, "ROLE_HAS_NO_KEY")

	setSignIn(t, n, signInMembers)
	eventually.Require(t, pollEvery, signInPolicyBudget, "the end user's refresh to be refused", func() (bool, error) {
		resp := postJSON(t, c, gw.PathRefresh, "", map[string]string{"refresh_token": session.RefreshToken, "namespace": n.Name})
		if resp.Status == http.StatusForbidden && resp.ErrorCode() == "SIGN_IN_CLOSED" {
			return true, nil
		}
		return false, fmt.Errorf("HTTP %d %s", resp.Status, resp.ErrorCode())
	})
	ch = challengeFor(t, c, gw.ChallengeRequest{Wallet: endUser.Address(), Namespace: n.Name})
	expectRefusal(t, postJSON(t, c, gw.PathVerify, "", signed(t, endUser, ch.Message)), http.StatusForbidden, "NAMESPACE_NOT_OWNED")

	// The owner is a member either way.
	signIn(t, c, n.Owner.Wallet, n.Name)
}

// TestSignInPolicy_cliShowsAndSets: `orama namespace session-policy` reads with
// no flags, sets with --sign-in, and keeps the field it was not given.
func TestSignInPolicy_cliShowsAndSets(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	read := func(args ...string) map[string]any {
		var out map[string]any
		res := n.CLI.MustOK(t, append([]string{"namespace", "session-policy", "--json"}, args...)...)
		if err := oramacli.DecodeJSON(res, &out); err != nil {
			t.Fatalf("session-policy %v printed no JSON: %v", args, err)
		}
		return out
	}
	if got := read(); got["sign_in"] != signInMembers || got["device_policy"] != "optional" {
		t.Fatalf("a new namespace's policy is %v, want members and optional", got)
	}
	if got := read("--sign-in", signInOpen); got["sign_in"] != signInOpen {
		t.Errorf("--sign-in open answered %v", got)
	}
	if got := read("--device-policy", "optional"); got["sign_in"] != signInOpen {
		t.Errorf("setting only the device policy changed sign-in: %v", got)
	}
	if got := read("--sign-in", signInMembers); got["sign_in"] != signInMembers {
		t.Errorf("--sign-in members answered %v", got)
	}
}
