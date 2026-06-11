package serverless

import (
	"net/http/httptest"
	"testing"
)

// TestCheckWSOrigin_ProxyHopRewritesHost is the regression guard for bugs
// #240 / #249. The namespace-gateway proxy hop in
// pkg/gateway/middleware.go::handleNamespaceGatewayRequest REWRITES r.Host
// to the backend target's IP:port (e.g. "10.0.0.6:10004") before
// forwarding. The original public host (e.g.
// "ns-anchat-test.orama-devnet.network") is preserved in
// X-Forwarded-Host. If checkWSOrigin only consults r.Host, every
// browser / RN-iOS WebSocket upgrade is rejected 403 because the
// client's Origin (`https://ns-anchat-test.orama-devnet.network`) will
// never match the proxied `10.0.0.6` r.Host.
//
// AnChat hit this for ~24h with their iPhone WS retests producing
// `code=1006 reason="Received bad response code from server: 403"`,
// while curl probes succeeded because curl doesn't send Origin and so
// the check returns true unconditionally — masking the bug.
//
// Fix: prefer X-Forwarded-Host when present.
func TestCheckWSOrigin_ProxyHopRewritesHost(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	// Simulate what the namespace gateway sees AFTER the proxy hop in
	// handleNamespaceGatewayRequest: r.Host has been overwritten to the
	// backend IP, but X-Forwarded-Host carries the original public host.
	r.Host = "10.0.0.6:10004"
	r.Header.Set("X-Forwarded-Host", "ns-anchat-test.orama-devnet.network")
	r.Header.Set("Origin", "https://ns-anchat-test.orama-devnet.network")

	if !checkWSOrigin(r) {
		t.Fatal("checkWSOrigin must accept Origin matching X-Forwarded-Host (proxy-hop scenario); rejecting will reproduce bugs #240/#249 — every iOS / browser WS client gets 403")
	}
}

// TestCheckWSOrigin_NoOriginAllowed confirms the historical curl-friendly
// path still works. Non-browser clients (curl, native libs without Origin)
// pass through unconditionally.
func TestCheckWSOrigin_NoOriginAllowed(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	r.Host = "10.0.0.6:10004"
	if !checkWSOrigin(r) {
		t.Fatal("requests without Origin must always be allowed (curl, native CLIs)")
	}
}

// TestCheckWSOrigin_DirectMatch covers the non-proxied case (direct
// connection to the gateway, no X-Forwarded-Host). r.Host IS the public
// host in that scenario.
func TestCheckWSOrigin_DirectMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	r.Host = "ns-anchat-test.orama-devnet.network"
	r.Header.Set("Origin", "https://ns-anchat-test.orama-devnet.network")
	if !checkWSOrigin(r) {
		t.Fatal("direct-connection Origin == r.Host must be allowed")
	}
}

// TestCheckWSOrigin_SubdomainMatch covers the documented "subdomain of
// host" allowance (HasSuffix("." + host)).
func TestCheckWSOrigin_SubdomainMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	r.Header.Set("X-Forwarded-Host", "orama-devnet.network")
	r.Header.Set("Origin", "https://app.orama-devnet.network")
	if !checkWSOrigin(r) {
		t.Fatal("subdomain of X-Forwarded-Host must be allowed")
	}
}

// TestCheckWSOrigin_CrossDomainRejected is the negative case — a request
// from a totally unrelated origin should still be rejected even after
// the X-Forwarded-Host fix. Defense-in-depth against CSRF.
func TestCheckWSOrigin_CrossDomainRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	r.Host = "10.0.0.6:10004"
	r.Header.Set("X-Forwarded-Host", "ns-anchat-test.orama-devnet.network")
	r.Header.Set("Origin", "https://evil.example.com")
	if checkWSOrigin(r) {
		t.Fatal("cross-origin request must be rejected; this is the CSRF guard")
	}
}

// TestCheckWSOrigin_NoHostAndNoForwardedHostRejected — defensive: if both
// r.Host and X-Forwarded-Host are empty, the check has no comparison
// target and should reject (the historical behavior).
func TestCheckWSOrigin_NoHostAndNoForwardedHostRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/functions/rpc-router/ws", nil)
	r.Host = ""
	r.Header.Set("Origin", "https://anywhere.example.com")
	if checkWSOrigin(r) {
		t.Fatal("missing both r.Host and X-Forwarded-Host must reject — no comparison target")
	}
}
