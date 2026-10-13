package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// docs/whitepaper/technical-reference/vol1/14-authorization.md: a reader holds nothing beyond the routes that ask for no
// permission. On a route that does not require ownership no grant was resolved
// for a wallet unless it was narrowed, so any wallet session was handed the
// data plane, and a reader put and read the cache (stagenet e2e, 2026-09-30).
// The wallet's role decides now.
var dataPlaneRoutes = []struct {
	name, method, path string
}{
	{"cache put", http.MethodPost, "/v1/cache/put"},
	{"cache get", http.MethodPost, "/v1/cache/get"},
	{"publish", http.MethodPost, "/v1/pubsub/publish"},
	{"topics", http.MethodGet, "/v1/pubsub/topics"},
	{"storage upload", http.MethodPost, "/v1/storage/upload"},
}

func TestForwardedDataPlane_aReaderReachesNoDataPlaneRoute(t *testing.T) {
	for _, route := range dataPlaneRoutes {
		t.Run(route.name, func(t *testing.T) {
			g, _ := namespaceGatewayForHops(t, "reader")

			rec, reached := serveHop(g, hop(t, g, route.method, route.path, hopNamespace, hopWallet))

			if reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeScopeMissing) {
				t.Errorf("reached %v, status %d, body %s; want 403 %s", reached, rec.Code, rec.Body.String(), CodeScopeMissing)
			}
		})
	}
}

func TestForwardedDataPlane_runtimeAndAboveReachEveryDataPlaneRoute(t *testing.T) {
	for _, role := range []string{"runtime", "developer", "admin", "owner"} {
		for _, route := range dataPlaneRoutes {
			t.Run(role+" "+route.name, func(t *testing.T) {
				g, _ := namespaceGatewayForHops(t, role)

				rec, reached := serveHop(g, hop(t, g, route.method, route.path, hopNamespace, hopWallet))

				if !reached {
					t.Errorf("a %s was refused %s: %d %s", role, route.name, rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// A wallet with no grant in the namespace keeps the data plane, as every
// signed-in user always had; the role decides only for a wallet that holds one.
func TestForwardedDataPlane_aWalletWithNoGrantKeepsTheDataPlane(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "")

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet))

	if !reached {
		t.Errorf("a wallet with no grant was refused %d: %s", rec.Code, rec.Body.String())
	}
}

// The role is read through the 10s grant cache, so the hot path pays the
// registry round trips once per wallet per namespace per lifetime.
func TestForwardedDataPlane_theRoleIsReadOncePerLifetime(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")

	serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet))
	if registry.queries == 0 {
		t.Fatal("the first publish did not read the wallet's role")
	}
	registry.queries = 0
	for i := 0; i < 5; i++ {
		serveHop(g, hop(t, g, http.MethodPost, "/v1/pubsub/publish", hopNamespace, hopWallet))
	}
	if registry.queries != 0 {
		t.Errorf("five more publishes made %d registry queries", registry.queries)
	}
}

// A role that cannot be read is not a role: the request is refused, where it
// used to be served the data plane.
func TestForwardedDataPlane_anUnreadableRoleRefusesTheRequest(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "reader")
	registry.failGrants = true

	rec, reached := serveHop(g, hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet))

	if reached || rec.Code != http.StatusServiceUnavailable {
		t.Errorf("reached %v, status %d; want 503 before the handler", reached, rec.Code)
	}
}

// An API key stays on its scopes: no grant is read for it on these routes.
func TestForwardedDataPlane_anAPIKeyStaysOnItsScopes(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "reader")

	rec, reached := serveHop(g, keyHop(t, g, http.MethodPost, "/v1/cache/put", "cache"))

	if !reached {
		t.Errorf("a cache key was refused the cache: %d %s", rec.Code, rec.Body.String())
	}
	if registry.queries != 0 {
		t.Errorf("a key made %d registry queries", registry.queries)
	}
}
