//go:build e2e_fleet

package namespaces

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	pathSessionPolicy = "/v1/namespace/session-policy"
	pathRateLimit     = "/v1/namespace/rate-limit"
	pathTransfer      = tenancy.PathMembers + "/transfer"
	// rateBudget is how long a new limit may take to bite; the handler
	// invalidates the gateway's cache at once (handlers/ratelimit), so this is
	// the other gateways' reload plus a burst of requests.
	rateBudget = 2 * time.Minute
	// burstRequests is more than the burst the test sets.
	burstRequests = 12
)

// TestNamespaceSessionPolicy_ownerSetsRuntimeCannot: GET reads the policy
// (optional by default), PUT sets one of optional|required|approval, a bad
// value is refused, and a runtime member cannot change it (docs/API_SURFACE.md
// "/v1/namespace/session-policy"; docs/AUTH.md).
func TestNamespaceSessionPolicy_ownerSetsRuntimeCannot(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	read := func() string {
		var p struct {
			DevicePolicy string `json:"device_policy"`
		}
		if err := tenancy.Get(t, n.Client, pathSessionPolicy, tenancy.Owner(n)).Expect(t, http.StatusOK).Decode(&p); err != nil {
			t.Fatal(err)
		}
		return p.DevicePolicy
	}
	if got := read(); got != "optional" {
		t.Fatalf("a new namespace's device policy is %q, want optional", got)
	}
	// The member signs in while the policy is still optional: once it is
	// required a session without a device is refused, and this member has none.
	runtime := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}
	tenancy.Send(t, n.Client, http.MethodPut, pathSessionPolicy, tenancy.Owner(n), map[string]string{"device_policy": "required"}).Expect(t, http.StatusOK)
	t.Cleanup(func() {
		tenancy.Restore(t, n.Client, http.MethodPut, pathSessionPolicy, tenancy.Owner(n), map[string]string{"device_policy": "optional"}, http.StatusOK)
	})
	if got := read(); got != "required" {
		t.Fatalf("after PUT required the policy reads %q", got)
	}
	for _, bad := range []string{"", "sometimes", "REQUIRED;drop", strings.Repeat("r", 2000)} {
		if r := tenancy.Send(t, n.Client, http.MethodPut, pathSessionPolicy, tenancy.Owner(n), map[string]string{"device_policy": bad}); r.Status != http.StatusBadRequest {
			t.Errorf("policy %.20q: want 400, got %d", bad, r.Status)
		}
	}
	if r := tenancy.Send(t, n.Client, http.MethodPut, pathSessionPolicy, runtime, map[string]string{"device_policy": "optional"}); r.Status != http.StatusForbidden {
		t.Errorf("a runtime member set the session policy: %d", r.Status)
	}
	tenancy.Send(t, n.Client, http.MethodDelete, pathSessionPolicy, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
	if got := read(); got != "required" {
		t.Fatalf("refused writes changed the policy to %q", got)
	}
}

// TestNamespaceRateLimit_overrideBitesAndClears: PUT sets a per-gateway limit
// that answers 429 with Retry-After once exceeded; DELETE goes back to the
// default (handlers/ratelimit; core/pkg/gateway/rate_limiter.go).
func TestNamespaceRateLimit_overrideBitesAndClears(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	c := n.Client.PinTo(f.State.Nodes[0].PublicIP)
	// The member signs in through the index gateway, which the override does
	// not limit, and before the override bites.
	runtime := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}
	var cfg struct {
		Source            string `json:"source"`
		RequestsPerMinute int    `json:"requests_per_minute"`
		Burst             int    `json:"burst"`
	}
	if err := tenancy.Get(t, c, pathRateLimit, tenancy.Owner(n)).Expect(t, http.StatusOK).Decode(&cfg); err != nil || cfg.Source != "default" {
		t.Fatalf("a new namespace's rate limit is %+v (%v), want the default", cfg, err)
	}
	for _, bad := range []map[string]int{{"requests_per_minute": 0, "burst": 1}, {"requests_per_minute": 1, "burst": -1}, {"requests_per_minute": 1 << 30, "burst": 1 << 30}} {
		if r := tenancy.Send(t, c, http.MethodPut, pathRateLimit, tenancy.Owner(n), bad); r.Status != http.StatusBadRequest {
			t.Errorf("limit %v: want 400, got %d", bad, r.Status)
		}
	}
	tenancy.Send(t, c, http.MethodPut, pathRateLimit, tenancy.Owner(n), map[string]int{"requests_per_minute": 6, "burst": 2}).Expect(t, http.StatusOK)
	cleared := false
	t.Cleanup(func() {
		if !cleared {
			clearRateLimit(t, c, n)
		}
	})
	eventually.Require(t, pollEvery, rateBudget, "the limit to answer 429", func() (bool, error) {
		for range burstRequests {
			r := tenancy.Get(t, c, pathCacheHealth, tenancy.Owner(n))
			if r.Status == http.StatusTooManyRequests {
				if r.Header.Get("Retry-After") == "" {
					return false, eventually.Stop(fmt.Errorf("429 without Retry-After"))
				}
				return true, nil
			}
		}
		return false, fmt.Errorf("%d requests, none limited", burstRequests)
	})
	// The DELETE is itself limited until the bucket refills.
	clearRateLimit(t, c, n)
	cleared = true
	if err := tenancy.Get(t, c, pathRateLimit, tenancy.Owner(n)).Expect(t, http.StatusOK).Decode(&cfg); err != nil || cfg.Source != "default" {
		t.Fatalf("after DELETE the limit is %+v (%v)", cfg, err)
	}
	if r := tenancy.Send(t, c, http.MethodPut, pathRateLimit, runtime, map[string]int{"requests_per_minute": 1, "burst": 1}); r.Status != http.StatusForbidden {
		t.Errorf("a runtime member set the rate limit: %d", r.Status)
	}
}

// clearRateLimit DELETEs the override, waiting out the 429s the override itself
// answers while the bucket refills, on a context of its own because it runs in
// cleanups too.
func clearRateLimit(t testing.TB, c *gw.Client, n *ns.Namespace) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), rateBudget)
	defer cancel()
	err := eventually.Poll(ctx, pollEvery, rateBudget, "the rate limit override of "+n.Name+" to be deleted", func() (bool, error) {
		r, err := c.Send(ctx, gw.Req{Method: http.MethodDelete, Path: pathRateLimit, Bearer: n.Owner.Token()})
		switch {
		case err != nil:
			return false, err
		case r.Status == http.StatusOK:
			return true, nil
		case r.Status == http.StatusTooManyRequests:
			return false, fmt.Errorf("HTTP 429")
		}
		return false, eventually.Stop(fmt.Errorf("HTTP %d: %.200s", r.Status, r.Body))
	})
	if err != nil {
		t.Errorf("failed to restore the default rate limit: %v", err)
	}
}

// TestNamespaceMembers_transferIsOwnerOnly: the owner hands the namespace to
// another wallet in one step and keeps an admin grant; an admin cannot
// transfer (OWNERSHIP_REQUIRED); the new owner can hand it back
// (docs/CLI_REFERENCE.md "orama members transfer").
func TestNamespaceMembers_transferIsOwnerOnly(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	heir, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	admin := tenancy.Member(t, n, tenancy.RoleAdmin)
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathTransfer, tenancy.Cred{Bearer: admin.Token()}, map[string]string{"wallet": heir.Address()}),
		http.StatusForbidden, tenancy.CodeOwnership)
	tenancy.Post(t, n.Client, pathTransfer, tenancy.Owner(n), map[string]string{"wallet": heir.Address()}).Expect(t, http.StatusOK)
	// Registered at once, so a failure below still gives the namespace back
	// to the original owner, who deletes it in the teardown.
	t.Cleanup(func() { giveBack(t, n, heir) })
	s, err := n.Owner.Client.For(t).SignIn(t.Context(), heir, n.Name, nil)
	if err != nil {
		t.Fatalf("the new owner cannot sign in: %v", err)
	}
	newOwner := &gw.User{Wallet: heir, Session: s, Namespace: n.Name, Client: n.Owner.Client}
	t.Cleanup(func() { endSession(t, newOwner) })
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathTransfer, tenancy.Owner(n), map[string]string{"wallet": admin.Wallet.Address()}),
		http.StatusForbidden, tenancy.CodeOwnership)
	tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
	for _, bad := range []string{"", "not-a-wallet", "0x123"} {
		if r := tenancy.Post(t, n.Client, pathTransfer, tenancy.Cred{Bearer: s.AccessToken}, map[string]string{"wallet": bad}); r.Status != http.StatusBadRequest {
			t.Errorf("transfer to %q: want 400, got %d", bad, r.Status)
		}
	}
}

// giveBack signs heir in on a context of its own (the test's is cancelled in
// a cleanup) and transfers n back to its original owner.
func giveBack(t testing.TB, n *ns.Namespace, heir *wallet.EVM) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	s, err := n.Owner.Client.SignIn(ctx, heir, n.Name, nil)
	if err != nil {
		t.Errorf("cleanup: the heir of %s cannot sign in to give it back, the teardown may leak it: %v", n.Name, err)
		return
	}
	tenancy.Restore(t, n.Client, http.MethodPost, pathTransfer, tenancy.Cred{Bearer: s.AccessToken},
		map[string]string{"wallet": n.Owner.Wallet.Address()}, http.StatusOK)
	endSession(t, &gw.User{Wallet: heir, Session: s, Namespace: n.Name, Client: n.Owner.Client})
}
