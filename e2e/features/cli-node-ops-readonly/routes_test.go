//go:build e2e_fleet

package clinodeopsreadonly

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// operatorReads are the operator routes the CLI reads (monitor/api_http.go
// telemetryPath, noderesolver/resolver.go).
func operatorReads(env string) []gw.Req {
	return []gw.Req{
		{Method: http.MethodGet, Path: "/v1/operator/telemetry"},
		{Method: http.MethodGet, Path: "/v1/operator/nodes", Query: url.Values{"env": {env}}},
	}
}

// TestOperatorReadRoutes_refuseNonOperators: the cluster's telemetry and node
// inventory are for its operators only (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-monitor
// "Only the cluster's operators may read it"): no credential is 401, garbage
// is 401, and a signed-in wallet that is not an operator is 403 NOT_AN_OPERATOR.
// The operator's own reads are the CLI tests above.
func TestOperatorReadRoutes_refuseNonOperators(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	stranger := gw.NewUser(t, f, gw.LobbyNamespace)
	for _, req := range operatorReads(f.State.Env) {
		if resp := c.MustSend(t, req); resp.Status != http.StatusUnauthorized {
			t.Errorf("%s with no credential: %d, want 401: %.200s", req.Path, resp.Status, resp.Body)
		}
		req.Bearer = "x.y.z"
		if resp := c.MustSend(t, req); resp.Status != http.StatusUnauthorized {
			t.Errorf("%s with a garbage token: %d, want 401: %.200s", req.Path, resp.Status, resp.Body)
		}
		req.Bearer = stranger.Token()
		// A lobby session holds no permission at all, so the scope gate may
		// refuse it before the operator-list check gets to: both are the same
		// refusal of the same caller.
		resp := c.MustSend(t, req)
		if code := resp.ErrorCode(); resp.Status != http.StatusForbidden || (code != tenancy.CodeNotOperator && code != tenancy.CodeScope) {
			t.Errorf("%s as a non-operator wallet: %d %s, want 403 %s or %s", req.Path, resp.Status, code, tenancy.CodeNotOperator, tenancy.CodeScope)
		}
	}
}
