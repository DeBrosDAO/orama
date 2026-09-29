//go:build e2e_fleet

package gatewaymiddleware

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestSecurityHeaders_everyResponseClass: the headers are set by the fourth
// middleware, before rate limiting, CORS, routing and auth, so every answer
// carries them — success, a refusal, a missing route, a wrong method, a bad
// body (docs/ARCHITECTURE.md "Middleware Stack", step 4). The 5xx and 429
// classes are in gateway-middleware-chaos, which can cause them.
func TestSecurityHeaders_everyResponseClass(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	cases := []struct {
		what   string
		req    gw.Req
		status int
	}{
		{"health", gw.Req{Path: "/health"}, http.StatusOK},
		{"version", gw.Req{Path: "/v1/version"}, http.StatusOK},
		{"no credential", gw.Req{Path: "/v1/auth/whoami"}, http.StatusUnauthorized},
		{"garbage bearer", gw.Req{Path: "/v1/auth/whoami", Bearer: "x.y.z"}, http.StatusUnauthorized},
		{"wrong method", gw.Req{Method: http.MethodGet, Path: "/v1/internal/acme/present"}, http.StatusMethodNotAllowed},
		{"tls check refused", gw.Req{Path: "/v1/internal/tls/check", Query: url.Values{"domain": {"example.com"}}}, http.StatusForbidden},
		{"tls check no domain", gw.Req{Path: "/v1/internal/tls/check"}, http.StatusBadRequest},
		{"unknown namespace host", gw.Req{Path: "/health", Host: "ns-e2e-nobody-here." + harness.Fleet(t).State.BaseDomain}, http.StatusNotFound},
	}
	for _, tc := range cases {
		resp := c.MustSend(t, tc.req)
		if resp.Status != tc.status {
			t.Errorf("%s: HTTP %d, want %d: %.200s", tc.what, resp.Status, tc.status, resp.Body)
		}
		requireSecurityHeaders(t, tc.what, resp)
	}
}

// TestSecurityHeaders_proxiedNamespaceResponses: a namespace host's answers
// come from the namespace gateway through the cluster gateway; they carry
// the same headers, with no copy disagreeing, on success and on refusal.
func TestSecurityHeaders_proxiedNamespaceResponses(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	ok := tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"})
	ok.Expect(t, http.StatusOK)
	requireSecurityHeaders(t, "namespace query", ok)
	refused := tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Cred{}, map[string]any{"sql": "SELECT 1"})
	requireSecurityHeaders(t, "namespace query without a credential", refused)
	if refused.Status != http.StatusUnauthorized {
		t.Errorf("an anonymous namespace query answered %d", refused.Status)
	}
}

// TestSecurityHeaders_statusPageOverridesReferrer: the status page and its
// assets set their own CSP and a stricter Referrer-Policy (no-referrer); the
// other security headers still apply (core/pkg/gateway/statuspage).
func TestSecurityHeaders_statusPageOverridesReferrer(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, path := range []string{"/status", "/status/assets/app.js"} {
		resp := c.MustSend(t, gw.Req{Path: path, Header: http.Header{"Accept": {"text/html"}}}).Expect(t, http.StatusOK)
		requireSecurityHeaders(t, path, resp, "Referrer-Policy")
		if got := resp.Header.Values("Referrer-Policy"); len(got) != 1 || got[0] != "no-referrer" {
			t.Errorf("%s: Referrer-Policy %v, want exactly no-referrer", path, got)
		}
	}
}
