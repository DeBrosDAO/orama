package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// keyHop is a request as the main gateway forwards it for an API key: the
// scopes on the key's row travel with it.
func keyHop(t *testing.T, g *Gateway, method, path, scopes string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set(HeaderInternalAuthValidated, "true")
	r.Header.Set(HeaderInternalAuthNamespace, hopNamespace)
	r.Header.Set(HeaderInternalAuthJWTSub, "ak_exchanged_key")
	r.Header.Set(HeaderInternalAuthScopes, scopes)
	if err := signInternalAuthHeaders(g.internalAuthKey, r.Header, method, path, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

// Every key that is not admin is given a runtime grant when it is minted, and
// a runtime role is the whole data plane. On a route that resolves the grant —
// publishing does — a key minted for `invoke`, or for the cache alone, was
// therefore let publish. A key reaches what its own scopes say.
func TestKeyScopes_boundTheGrantOnRoutesThatResolveIt(t *testing.T) {
	const (
		appRuntime = "invoke,proxy,push,storage,webrtc" // the app-runtime profile
		cacheOnly  = "cache"
		admin      = "admin"
	)
	cases := []struct {
		name, scopes, path string
		want               int
	}{
		{"an app-runtime key cannot publish", appRuntime, "/v1/pubsub/publish", http.StatusForbidden},
		{"a cache key cannot publish", cacheOnly, "/v1/pubsub/publish", http.StatusForbidden},
		{"a cache key reaches the cache", cacheOnly, "/v1/cache/put", http.StatusOK},
		{"an app-runtime key reaches push", appRuntime, "/v1/push/devices", http.StatusOK},
		{"an admin key publishes", admin, "/v1/pubsub/publish", http.StatusOK},
		{"an admin key reaches the control plane", admin, "/v1/functions", http.StatusOK},
		{"a runtime key does not reach the control plane", appRuntime, "/v1/functions", http.StatusForbidden},
		{"a key with no scopes reaches nothing", "unknown-word", "/v1/pubsub/publish", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role := "runtime"
			if tc.scopes == admin {
				role = "admin"
			}
			g, _ := namespaceGatewayForHops(t, role)

			rec, _ := serveHop(g, keyHop(t, g, http.MethodPost, tc.path, tc.scopes))

			if rec.Code != tc.want {
				t.Errorf("%s %s: status %d, want %d: %s", tc.scopes, tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
