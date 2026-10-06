//go:build e2e_fleet

package gatewaymiddleware

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestCacheControl_apiResponsesAreNoStore: every /v1/* answer, success or
// refusal, tells HTTP caches not to store it, so a user's data does not sit in
// a browser or WebView disk cache; a route that chooses its own caching, the
// public status endpoint, keeps it; non-API paths are untouched
// (docs/SECURITY.md#response-caching, bugboard #735).
func TestCacheControl_apiResponsesAreNoStore(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	for _, tc := range []struct {
		what   string
		path   string
		status int
	}{
		{"health", "/v1/health", http.StatusOK},
		{"version", "/v1/version", http.StatusOK},
		{"refusal", "/v1/auth/whoami", http.StatusUnauthorized},
		// Anonymous, an unknown route is refused before routing says it does
		// not exist (auth-keys-roles pins the 401).
		{"unknown route", "/v1/e2e-no-such-route", http.StatusUnauthorized},
	} {
		resp := c.MustSend(t, gw.Req{Path: tc.path})
		if resp.Status != tc.status {
			t.Errorf("%s: HTTP %d, want %d: %.200s", tc.what, resp.Status, tc.status, resp.Body)
		}
		if got := resp.Header.Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
			t.Errorf("%s: Cache-Control %v, want exactly no-store", tc.what, got)
		}
		if got := resp.Header.Get("Pragma"); got != "no-cache" {
			t.Errorf("%s: Pragma %q, want no-cache", tc.what, got)
		}
	}
	status := c.MustSend(t, gw.Req{Path: "/v1/status"}).Expect(t, http.StatusOK)
	if got := status.Header.Values("Cache-Control"); len(got) != 1 || got[0] != "public, max-age=5" {
		t.Errorf("/v1/status: Cache-Control %v, want its own public, max-age=5", got)
	}
}

// TestCacheControl_proxiedNamespaceResponsesAreNoStore: a namespace host's
// answers come through the cluster gateway from the namespace gateway and carry
// one no-store, not two.
func TestCacheControl_proxiedNamespaceResponsesAreNoStore(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	ok := tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
	refused := tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Cred{}, map[string]any{"sql": "SELECT 1"})
	for what, resp := range map[string]*gw.Response{"namespace query": ok, "namespace query without a credential": refused} {
		if got := resp.Header.Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
			t.Errorf("%s: Cache-Control %v, want exactly no-store", what, got)
		}
	}
}
