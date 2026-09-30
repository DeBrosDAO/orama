//go:build e2e_fleet

package webrtc

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const stealthRung = ":443"

// What a failed `namespace enable webrtc-stealth` may say: the documented
// failure (namespace/cluster_manager_stealth.go: the TURN re-spawn with the
// stealth certificate failed and the enable was rolled back), or a gateway
// without WebRTC management (gateway.go namespaceWebRTCStealthPublicHandler).
const (
	stealthRolledBack  = "stealth rolled back"
	noWebRTCManagement = "WebRTC management not enabled"
)

// requireRolledBack accepts a failed stealth enable only for the documented
// reason, and then only when no stealth rung is advertised; a gateway
// without WebRTC management is a missing prerequisite (not applicable).
func requireRolledBack(t *testing.T, fx *fixture, out string) {
	t.Helper()
	switch {
	case strings.Contains(out, noWebRTCManagement):
		harness.SkipNotApplicable(t, "the namespace gateway has no WebRTC management, so stealth cannot be toggled: "+out)
	case !strings.Contains(out, stealthRolledBack):
		t.Fatalf("stealth enable failed for a reason other than the documented rollback: %s", out)
	}
	if u := stealthURI(t, fx); u != "" {
		t.Errorf("stealth enable failed (%s) but %s is advertised: no rollback", out, u)
	}
}

// stealthURI is the credentials' turns:cdn-<hash>.<base>:443 rung, or "".
func stealthURI(t testing.TB, fx *fixture) string {
	t.Helper()
	_, cr := restCreds(t, fx.c, fx.token)
	for _, u := range cr.URIs {
		if strings.HasPrefix(u, "turns:cdn-") && strings.HasSuffix(u, stealthRung) {
			return u
		}
	}
	return ""
}

// TestStealth_enableDisableOrRollBack: enabling stealth either adds the
// turns:cdn-<hash>.<base>:443 rung to the credentials, or fails and is rolled
// back so no rung is advertised; disabling removes the rung and keeps the
// baseline ladder (docs/STEALTH_TURN.md#enabling-stealth-for-a-namespace).
func TestStealth_enableDisableOrRollBack(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	if u := stealthURI(t, fx); u != "" {
		t.Fatalf("a new WebRTC namespace advertises %s", u)
	}
	res, err := fx.n.CLI.For(t).Run(t.Context(), "namespace", "enable", "webrtc-stealth", "--namespace", fx.n.Name)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 0 {
		requireRolledBack(t, fx, strings.TrimSpace(res.Stdout+res.Stderr))
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if r, err := fx.n.CLI.Run(ctx, "namespace", "disable", "webrtc-stealth", "--namespace", fx.n.Name); err != nil || r.Exit != 0 {
			t.Errorf("cleanup: disabling stealth: %v %s", err, r.Stderr)
		}
	})
	eventually.Require(t, pollEvery, readyBudget, "the stealth rung advertised", func() (bool, error) {
		return stealthURI(t, fx) != "", nil
	})
	fx.n.CLI.MustOK(t, "namespace", "disable", "webrtc-stealth", "--namespace", fx.n.Name)
	eventually.Require(t, pollEvery, readyBudget, "the stealth rung withdrawn", func() (bool, error) {
		return stealthURI(t, fx) == "", nil
	})
	if _, cr := restCreds(t, fx.c, fx.token); len(cr.URIs) < 3 {
		t.Errorf("disabling stealth removed the baseline ladder: %v", cr.URIs)
	}
}

// TestWebRTC_notEnabledAndPrerequisites: without WebRTC the credential and
// signalling routes do not serve, and stealth cannot be enabled
// (docs/STEALTH_TURN.md: requires WebRTC).
func TestWebRTC_notEnabledAndPrerequisites(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	tenancy.Reserve(t, f, 1)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	token := member(t, n, "runtime")
	// Without WebRTC the gateway registers no credentials route (routes.go).
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathCreds, Bearer: token}); r.Status != http.StatusNotFound {
		t.Errorf("TURN credentials for a namespace without WebRTC: want 404 (no route), got %d %.200s", r.Status, r.Body)
	}
	if res, err := n.CLI.For(t).Run(t.Context(), "namespace", "enable", "webrtc-stealth", "--namespace", n.Name); err != nil || res.Exit == 0 {
		t.Errorf("stealth enabled without WebRTC: %v", err)
	}
	if out := n.CLI.MustOK(t, "namespace", "webrtc-status", "--namespace", n.Name).Stdout; !strings.Contains(out, "not enabled") {
		t.Errorf("webrtc-status: %q", out)
	}
}
