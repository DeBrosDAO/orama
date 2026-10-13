//go:build e2e_fleet

package gatewaymiddleware

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// securityHeaders is what the security-headers middleware sets on every
// response the gateway writes (core/pkg/gateway/middleware.go
// securityHeadersMiddleware). HSTS is set when the request arrived over TLS,
// which Caddy reports with X-Forwarded-Proto: https.
var securityHeaders = map[string]string{
	"X-Content-Type-Options":    "nosniff",
	"X-Frame-Options":           "DENY",
	"X-Xss-Protection":          "0",
	"Referrer-Policy":           "strict-origin-when-cross-origin",
	"Permissions-Policy":        "camera=(self), microphone=(self), geolocation=()",
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
}

// requireSecurityHeaders fails unless resp carries every security header,
// each value the expected one (a proxied response may repeat a header; every
// copy must agree). except names headers a handler deliberately overrides.
func requireSecurityHeaders(t *testing.T, what string, resp *gw.Response, except ...string) {
	t.Helper()
	skip := map[string]bool{}
	for _, h := range except {
		skip[http.CanonicalHeaderKey(h)] = true
	}
	for name, want := range securityHeaders {
		if skip[http.CanonicalHeaderKey(name)] {
			continue
		}
		got := resp.Header.Values(name)
		if len(got) == 0 {
			t.Errorf("%s (HTTP %d): no %s", what, resp.Status, name)
			continue
		}
		for _, v := range got {
			if v != want {
				t.Errorf("%s (HTTP %d): %s is %q, want %q", what, resp.Status, name, v, want)
			}
		}
	}
}

// jsonHeader is the Content-Type of a JSON body.
func jsonHeader() http.Header { return http.Header{"Content-Type": {"application/json"}} }
