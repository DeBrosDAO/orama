//go:build e2e_fleet

package gatewaymiddleware

import (
	"net/http"
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// CORS answer (core/pkg/gateway/middleware.go corsMiddleware).
const (
	corsMethods = "GET, PUT, POST, DELETE, OPTIONS"
	corsHeaders = "Content-Type, Authorization, X-API-Key"
	corsMaxAge  = "600"
)

// TestCORS_preflightEchoesOnlyTheClusterOrigins: a preflight is answered 204
// before auth; the base domain and any name under it (and the development
// origins localhost/127.0.0.1) are echoed back with Vary: Origin, and any
// other origin — a lookalike included — gets the base domain, which a browser
// then refuses (docs/ARCHITECTURE.md "Middleware Stack": CORS runs before
// authentication).
func TestCORS_preflightEchoesOnlyTheClusterOrigins(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	base := "https://" + f.State.BaseDomain
	echoed := []string{base, "https://app." + f.State.BaseDomain, "https://a.b." + f.State.BaseDomain + ":8443",
		"http://localhost:3000", "http://127.0.0.1"}
	refused := []string{"https://evil.example", "https://" + f.State.BaseDomain + ".evil.example",
		"https://evil" + f.State.BaseDomain, "null", "https://dbrsteting.bid"}
	for _, origin := range append(echoed, refused...) {
		resp := c.MustSend(t, gw.Req{Method: http.MethodOptions, Path: "/v1/rqlite/query", Header: http.Header{
			"Origin": {origin}, "Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"authorization"}}})
		if resp.Status != http.StatusNoContent || len(resp.Body) != 0 {
			t.Errorf("preflight from %s: %d %q, want an empty 204", origin, resp.Status, resp.Body)
		}
		want := base
		if slices.Contains(echoed, origin) {
			want = origin
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("preflight from %s: Allow-Origin %q, want %q", origin, got, want)
		}
		if resp.Header.Get("Access-Control-Allow-Methods") != corsMethods || resp.Header.Get("Access-Control-Allow-Headers") != corsHeaders ||
			resp.Header.Get("Access-Control-Max-Age") != corsMaxAge || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("preflight from %s: CORS headers %v", origin, resp.Header)
		}
		requireSecurityHeaders(t, "preflight", resp)
	}
}

// TestCORS_refusalsAreReadable: a 401 to an allowed origin carries the CORS
// headers, so a browser client can read the refusal's code instead of an
// opaque "failed to fetch" (core/pkg/gateway/middleware.go: the readiness
// gate sits inside CORS for the same reason).
func TestCORS_refusalsAreReadable(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	origin := "https://app." + f.State.BaseDomain
	resp := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/auth/whoami", Header: http.Header{"Origin": {origin}}})
	if resp.Status != http.StatusUnauthorized || resp.Header.Get("Access-Control-Allow-Origin") != origin || resp.ErrorCode() == "" {
		t.Fatalf("anonymous whoami from %s: %d Allow-Origin %q code %q, want a readable 401 with a code",
			origin, resp.Status, resp.Header.Get("Access-Control-Allow-Origin"), resp.ErrorCode())
	}
	if resp.Header.Get("Vary") == "" {
		t.Error("an echoed origin without Vary: a shared cache could serve it to another origin")
	}
}

// TestCORS_noOriginGetsTheBaseDomain: a request without Origin (a server, the
// CLI) is answered for the base domain, never "*".
func TestCORS_noOriginGetsTheBaseDomain(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	resp := harness.GW(t).MustSend(t, gw.Req{Path: "/health"})
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://"+f.State.BaseDomain {
		t.Fatalf("Allow-Origin %q without an Origin, want the base domain", got)
	}
}
