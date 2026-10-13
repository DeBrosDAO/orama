//go:build e2e_fleet

package authdevices

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Session policies (docs/whitepaper/technical-reference/vol1/13-identity.md#linking-a-device-from-a-device).
const (
	policyOptional = "optional"
	policyRequired = "required"
	policyApproval = "approval"
	// policyCacheBudget: a refresh may be judged by a policy read up to 10
	// seconds earlier (docs/whitepaper/technical-reference/vol1/14-authorization.md#consistency-and-caching).
	policyCacheBudget = revocationStaleness + stalenessSlack
	restoreBudget     = time.Minute
)

type policyAnswer struct {
	Namespace         string `json:"namespace"`
	DevicePolicy      string `json:"device_policy"`
	RevokedSignInKeys int    `json:"revoked_sign_in_keys"`
}

// setPolicy sets n's session policy as its owner and restores optional at
// cleanup.
func setPolicy(t testing.TB, n *ns.Namespace, policy string) policyAnswer {
	t.Helper()
	var out policyAnswer
	resp := send(t, n.Owner.Client, http.MethodPut, pathPolicy, n.Owner.Token(), map[string]string{"device_policy": policy})
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), restoreBudget)
		defer cancel()
		if _, err := n.Owner.Client.JSON(ctx, http.MethodPut, pathPolicy, n.Owner.Token(), map[string]string{"device_policy": policyOptional}, nil); err != nil {
			t.Errorf("cleanup: failed to restore %s's session policy: %v", n.Name, err)
		}
	})
	return out
}

// TestSessionPolicy_readWriteAndWhoMay: optional by default; only the
// namespace-write permission (owner, admin) sets it; values are checked.
func TestSessionPolicy_readWriteAndWhoMay(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	var got policyAnswer
	if err := c.MustSend(t, gw.Req{Path: pathPolicy, Bearer: n.Owner.Token()}).Expect(t, http.StatusOK).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DevicePolicy != policyOptional || got.Namespace != n.Name {
		t.Fatalf("a new namespace's policy is %+v, want optional", got)
	}
	for _, bad := range []string{"sometimes", "", "required; DROP TABLE x"} {
		if resp := send(t, c, http.MethodPut, pathPolicy, n.Owner.Token(), map[string]string{"device_policy": bad}); resp.Status != http.StatusBadRequest {
			t.Errorf("policy %q: want 400, got %d", bad, resp.Status)
		}
	}
	for _, role := range []string{roleRuntime, roleDeveloper} {
		tok := signInSession(t, c, member(t, n, role), n.Name, nil).AccessToken
		resp := send(t, c, http.MethodPut, pathPolicy, tok, map[string]string{"device_policy": policyRequired})
		expectCode(t, resp, http.StatusForbidden, "INSUFFICIENT_SCOPE")
	}
	lobby := signInSession(t, c, newWallet(t), "", nil).AccessToken
	if resp := send(t, c, http.MethodPut, pathPolicy, lobby, map[string]string{"device_policy": policyRequired}); resp.Status != http.StatusForbidden {
		t.Errorf("the lobby set a session policy: %d", resp.Status)
	}
	if set := setPolicy(t, n, " Approval "); set.DevicePolicy != policyApproval {
		t.Errorf("a padded, capitalised value was stored as %q", set.DevicePolicy)
	}
}

// TestSessionPolicy_requiredRefusesUnboundEndUsers: under `required` an end
// user's sign-in, key and refresh without a device are DEVICE_REQUIRED, the
// keys their sign-ins minted are revoked and counted, and the operating roles
// (owner, admin, developer) are held to optional.
func TestSessionPolicy_requiredRefusesUnboundEndUsers(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	user := member(t, n, roleRuntime)
	before := signInSession(t, c, user, n.Name, nil)
	if before.APIKey == "" {
		t.Fatal("an end user's sign-in under optional handed out no key to be swept")
	}
	set := setPolicy(t, n, policyRequired)
	if set.RevokedSignInKeys < 1 {
		t.Errorf("setting required revoked %d sign-in keys, want the end user's", set.RevokedSignInKeys)
	}
	if _, resp, err := c.For(t).Token(t.Context(), before.APIKey); err == nil || resp == nil || resp.Status != http.StatusUnauthorized {
		t.Errorf("the swept sign-in key still exchanges (want 401): %v", err)
	}
	expectCode(t, postJSON(t, c, gw.PathVerify, "", signInReq(t, c, user, n.Name, nil)), http.StatusForbidden, "DEVICE_REQUIRED")
	req := signInReq(t, c, user, n.Name, nil)
	expectCode(t, postJSON(t, c, gw.PathAPIKey, "", map[string]string{"message": req.Message, "signature": req.Signature}), http.StatusForbidden, "DEVICE_REQUIRED")
	eventually.Require(t, pollEvery, policyCacheBudget, "the unbound session's refresh to be refused", func() (bool, error) {
		resp := refreshWith(t, c, before.RefreshToken, n.Name, nil)
		if resp.Status == http.StatusForbidden && resp.ErrorCode() == "DEVICE_REQUIRED" {
			return true, nil
		}
		return false, fmt.Errorf("HTTP %d %s", resp.Status, resp.ErrorCode())
	})
	signInSession(t, c, user, n.Name, gw.NewDevice(t, wallet.AlgEd25519))
	for _, role := range []string{roleAdmin, roleDeveloper} {
		signInSession(t, c, member(t, n, role), n.Name, nil)
	}
	signInSession(t, c, n.Owner.Wallet, n.Name, nil)
}

// TestSessionPolicy_plainDeviceLoginNeedsDeviceUnderRequired: approving a
// plain `orama auth login` code with a wallet is DEVICE_REQUIRED for an end
// user once the namespace requires devices.
func TestSessionPolicy_plainDeviceLoginNeedsDeviceUnderRequired(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	user := member(t, n, roleRuntime)
	setPolicy(t, n, policyRequired)
	d := startLogin(t, c, map[string]any{"namespace": n.Name})
	expectCode(t, approveWithWallet(t, c, user, n.Name, d.UserCode, false), http.StatusForbidden, "DEVICE_REQUIRED")
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "authorization_pending")
}
