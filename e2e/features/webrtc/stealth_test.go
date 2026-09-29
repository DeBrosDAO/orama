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
		if u := stealthURI(t, fx); u != "" {
			t.Errorf("stealth enable failed (%s) but %s is advertised: no rollback", strings.TrimSpace(res.Stderr), u)
		}
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
	tenancy.Reserve(t, harness.Fleet(t), 1)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	token := member(t, n, c, "runtime")
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathCreds, Bearer: token}); r.Status == http.StatusOK {
		t.Errorf("TURN credentials were issued for a namespace without WebRTC: %.200s", r.Body)
	}
	if res, err := n.CLI.For(t).Run(t.Context(), "namespace", "enable", "webrtc-stealth", "--namespace", n.Name); err != nil || res.Exit == 0 {
		t.Errorf("stealth enabled without WebRTC: %v", err)
	}
	if out := n.CLI.MustOK(t, "namespace", "webrtc-status", "--namespace", n.Name).Stdout; !strings.Contains(out, "not enabled") {
		t.Errorf("webrtc-status: %q", out)
	}
}
