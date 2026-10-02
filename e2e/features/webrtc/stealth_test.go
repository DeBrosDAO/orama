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

// TestStealth_enableDisableOrRollBack: enabling stealth adds the
// turns:cdn-<hash>.<base>:443 rung to the credentials; disabling removes the
// rung and keeps the baseline ladder (docs/STEALTH_TURN.md#enabling-stealth-for-a-namespace).
// The documented rollback (the TURN re-spawn with the stealth certificate
// failing) is a failure here: the cluster has a valid wildcard certificate, so
// a run that always rolls back must not pass.
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
		t.Fatalf("enabling stealth failed (%d): %s", res.Exit, strings.TrimSpace(res.Stdout+res.Stderr))
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
	// A 503 while the gateway restarts carries no URIs either; only a served
	// ladder without the rung counts as withdrawn.
	eventually.Require(t, pollEvery, readyBudget, "the stealth rung withdrawn from a served ladder", func() (bool, error) {
		r, cr := restCreds(t, fx.c, fx.token)
		return r.Status == http.StatusOK && len(cr.URIs) > 0 && stealthURI(t, fx) == "", nil
	})
	if r, cr := restCreds(t, fx.c, fx.token); r.Status != http.StatusOK || len(cr.URIs) < 3 {
		t.Errorf("disabling stealth removed the baseline ladder (HTTP %d): %v", r.Status, cr.URIs)
	}
	// Disabling what is already off is idempotent.
	fx.n.CLI.MustOK(t, "namespace", "disable", "webrtc-stealth", "--namespace", fx.n.Name)
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
