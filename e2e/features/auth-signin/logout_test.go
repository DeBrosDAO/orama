//go:build e2e_fleet

package authsignin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// auditPollBudget bounds waiting for an audit row to be readable.
	auditPollBudget = 30 * time.Second
	// hugeFieldBytes fits under the logout body limit (64 KiB); overLimitBytes does not.
	hugeFieldBytes = 60 << 10
	overLimitBytes = 70 << 10
)

// TestLogout_endsAccessAndRefresh: logging out revokes the refresh token and
// the access token presented with it, so "log me out" stops the token in hand
// too (docs/AUTH.md#revoking). The revocation reaches every gateway within the
// list's staleness.
func TestLogout_endsAccessAndRefresh(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	nodes := perNode(t, f, c)
	if _, err := nodes[0].Client.For(t).Logout(t.Context(), s.AccessToken, s.RefreshToken, lobby, false); err != nil {
		t.Fatalf("logout: %v", err)
	}
	expectRefusal(t, whoami(t, nodes[0].Client, s.AccessToken), http.StatusUnauthorized, "AUTH_REVOKED")
	refusedEverywhere(t, nodes, s.AccessToken)
	expectRefreshRefused(t, refresh(t, c, s.RefreshToken, lobby), "refresh after logout")
}

// TestLogout_allEndsEverySession: all=true ends every session of the wallet,
// not only the one presenting it; another wallet is untouched.
func TestLogout_allEndsEverySession(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	w := newWallet(t)
	first, second := signIn(t, c, w, ""), signIn(t, c, w, "")
	bystander := signIn(t, c, newWallet(t), "")
	if _, err := c.For(t).Logout(t.Context(), first.AccessToken, first.RefreshToken, lobby, true); err != nil {
		t.Fatalf("logout all: %v", err)
	}
	nodes := perNode(t, f, c)
	refusedEverywhere(t, nodes, second.AccessToken)
	refusedEverywhere(t, nodes, first.AccessToken)
	expectRefreshRefused(t, refresh(t, c, second.RefreshToken, lobby), "the other session's refresh token")
	whoami(t, c, bystander.AccessToken).Expect(t, http.StatusOK)
}

// TestLogout_allNeedsAToken: all=true names every session of whoever asks, so
// it needs a signed-in caller; a bare refresh token cannot do it.
func TestLogout_allNeedsAToken(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	s := signIn(t, c, newWallet(t), "")
	resp := postJSON(t, c, gw.PathLogout, "", map[string]any{"refresh_token": s.RefreshToken, "namespace": lobby, "all": true})
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("logout all without a token: want 401, got %d: %s", resp.Status, resp.Body)
	}
	whoami(t, c, s.AccessToken).Expect(t, http.StatusOK)
}

// TestLogout_malformedRequests: a request that names nothing to end, bad JSON
// and a wrong method are client errors, never a server error.
func TestLogout_malformedRequests(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	cases := map[string]gw.Req{
		"GET":          {Path: gw.PathLogout},
		"not JSON":     jsonBody(gw.PathLogout, `{"refresh_token":`),
		"nothing":      jsonBody(gw.PathLogout, `{}`),
		"wrong types":  jsonBody(gw.PathLogout, `{"refresh_token":1,"all":"yes"}`),
		"huge garbage": jsonBody(gw.PathLogout, `{"refresh_token":"`+strings.Repeat("A", hugeFieldBytes)+`"}`),
		"over limit":   jsonBody(gw.PathLogout, `{"refresh_token":"`+strings.Repeat("A", overLimitBytes)+`"}`),
	}
	for name, req := range cases {
		resp := c.MustSend(t, req)
		if resp.Status >= http.StatusInternalServerError {
			t.Errorf("%s: the gateway answered %d to a client mistake: %s", name, resp.Status, resp.Body)
		}
	}
}

// TestRefresh_replayIsAudited: presenting a spent refresh token is refused and
// recorded in the namespace's trail as auth.refresh.replay (docs/AUTH.md,
// "presenting one twice is a replay, and the second attempt fails and is
// recorded").
func TestRefresh_replayIsAudited(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	s := signIn(t, c, n.Owner.Wallet, n.Name)
	var next gw.Session
	if err := refresh(t, c, s.RefreshToken, n.Name).Expect(t, http.StatusOK).Decode(&next); err != nil {
		t.Fatal(err)
	}
	refresh(t, c, s.RefreshToken, n.Name).Expect(t, http.StatusOK) // the grace slot
	expectRefreshRefused(t, refresh(t, c, s.RefreshToken, n.Name), "a replayed refresh token")
	q := url.Values{"action": {"auth.refresh.replay"}}
	eventually.Require(t, pollEvery, auditPollBudget, "the replay to reach the audit trail", func() (bool, error) {
		var out struct {
			Events []struct{ Action, Result string } `json:"events"`
		}
		resp := c.MustSend(t, gw.Req{Path: "/v1/audit", Query: q, Bearer: next.AccessToken})
		if resp.Status != http.StatusOK {
			return false, eventually.Stop(fmt.Errorf("GET /v1/audit answered %d: %s", resp.Status, resp.Body))
		}
		if err := resp.Decode(&out); err != nil {
			return false, eventually.Stop(err)
		}
		return len(out.Events) > 0, fmt.Errorf("%d replay events", len(out.Events))
	})
}
