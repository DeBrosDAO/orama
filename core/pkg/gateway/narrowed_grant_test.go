package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// serveHopAuthorizing runs the namespace gateway's real chain and, where a
// handler would, asks whether the credential covers one object.
func serveHopAuthorizing(g *Gateway, r *http.Request, object auth.Resource) (status int, reached bool) {
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if err := auth.AuthorizeResource(r.Context(), object); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	g.internalAuthMiddleware(g.routePolicyMiddleware(g.authMiddleware(
		g.authorizationMiddleware(g.scopeMiddleware(next))))).ServeHTTP(rec, r)
	return rec.Code, reached
}

// A wallet narrowed to cache:key=sessions/* was handed the whole data plane on
// the namespace host, because cache does not require ownership and so no grant
// was resolved for the selector to live on.
func TestForwardedDataPlane_cacheSelectorNarrowsTheWallet(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "cache:key=sessions/*"

	for name, tc := range map[string]struct {
		key  string
		want int
	}{
		"inside the selector":               {"sessions/abc", http.StatusOK},
		"a key that is not a path":          {"sessions/../tokens/x", http.StatusOK},
		"another map":                       {"tokens/x", http.StatusForbidden},
		"a prefix that is not the selector": {"sessionsx/abc", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
			status, reached := serveHopAuthorizing(g, r, auth.Resource{
				Domain: auth.DomainCache, Action: auth.ActionWrite, Name: tc.key,
			})
			if !reached || status != tc.want {
				t.Errorf("put %s: reached %v, status %d, want %d", tc.key, reached, status, tc.want)
			}
		})
	}
}

func TestForwardedDataPlane_storageSelectorNarrowsTheWallet(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "storage:avatars/*"

	for name, tc := range map[string]struct {
		object string
		want   int
	}{
		"inside the selector": {"avatars/me.png", http.StatusOK},
		"outside it":          {"keys/x.png", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			r := hop(t, g, http.MethodPost, "/v1/storage/upload", hopNamespace, hopWallet)
			status, _ := serveHopAuthorizing(g, r, auth.Resource{
				Domain: auth.DomainStorage, Action: auth.ActionWrite, Name: tc.object,
			})
			if status != tc.want {
				t.Errorf("upload %s: status %d, want %d", tc.object, status, tc.want)
			}
		})
	}
}

// A selector in another domain says nothing about this one, and a grant
// narrowed to it holds only what it names: cache is not part of it.
func TestForwardedDataPlane_aSelectorInAnotherDomainDoesNotReachTheCache(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "pubsub:topic=chat.*"

	r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
	status, reached := serveHopAuthorizing(g, r, auth.Resource{
		Domain: auth.DomainCache, Action: auth.ActionWrite, Name: "sessions/abc",
	})
	if reached || status != http.StatusForbidden {
		t.Errorf("reached %v, status %d, want the gate to refuse with 403", reached, status)
	}
}

// A wallet that holds the whole role, or no grant at all, reaches the whole
// data plane as it always did — and its answer is remembered, so the hot path
// is not a registry read per request.
func TestForwardedDataPlane_theGrantIsReadOncePerLifetime(t *testing.T) {
	for name, role := range map[string]string{"whole role": "runtime", "no grant": ""} {
		t.Run(name, func(t *testing.T) {
			g, registry := namespaceGatewayForHops(t, role)
			object := auth.Resource{Domain: auth.DomainCache, Action: auth.ActionWrite, Name: "tokens/x"}

			for i := 0; i < 3; i++ {
				r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
				if status, _ := serveHopAuthorizing(g, r, object); status != http.StatusOK {
					t.Fatalf("request %d: status %d, want 200", i, status)
				}
				if i == 0 {
					registry.queries = 0
				}
			}
			if registry.queries != 0 {
				t.Errorf("the grant was read %d more times after the first request", registry.queries)
			}
		})
	}
}

// An API key's scopes are its authority on these routes and are read from its
// row on every request; the grant lookup is a wallet's.
func TestForwardedDataPlane_anAPIKeyIsNotLookedUp(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "cache:key=sessions/*"

	if g.callerHoldsNarrowedGrant(hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, "ak_exchanged_key"),
		g.policyFor(hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, "ak_exchanged_key"))) {
		t.Error("a key was treated as holding a wallet's narrowed grant")
	}
	if registry.queries != 0 {
		t.Errorf("a key made %d registry queries", registry.queries)
	}
}

func TestGrantCache_entriesExpireAndAreBounded(t *testing.T) {
	var c grantCache
	now := time.Now()
	grant := &auth.Grant{Role: auth.RoleRuntime}

	if _, ok := c.get("a", now); ok {
		t.Fatal("an empty cache answered")
	}
	c.put("a", grant, now)
	if got, ok := c.get("a", now.Add(narrowedGrantTTL-time.Nanosecond)); !ok || got != grant {
		t.Errorf("a live entry: %v %v", got, ok)
	}
	if _, ok := c.get("a", now.Add(narrowedGrantTTL)); ok {
		t.Error("an entry outlived its lifetime")
	}

	c.put("none", nil, now)
	if got, ok := c.get("none", now); !ok || got != nil {
		t.Errorf("having no grant is an answer too: %v %v", got, ok)
	}

	for i := 0; i < narrowedGrantCacheMax+10; i++ {
		c.put(string(rune('a'+i%26))+time.Duration(i).String(), grant, now)
	}
	if len(c.entries) > narrowedGrantCacheMax {
		t.Errorf("the cache holds %d entries, bound is %d", len(c.entries), narrowedGrantCacheMax)
	}
}
