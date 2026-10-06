package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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

// Publish and subscribe are owned routes: a hop used to skip the grant for them,
// so a wallet narrowed to pubsub:topic=chat.* published to billing (stagenet
// e2e TestPubsubAuth_topicSelectorGrant saw a 200).
func TestForwardedDataPlane_pubsubSelectorNarrowsTheWallet(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "pubsub:topic=chat.*"

	for name, tc := range map[string]struct {
		method string
		path   string
		action auth.Action
		topic  string
		want   int
	}{
		"publish inside the selector":   {http.MethodPost, "/v1/pubsub/publish", auth.ActionWrite, "chat.room1", http.StatusOK},
		"publish to another topic":      {http.MethodPost, "/v1/pubsub/publish", auth.ActionWrite, "billing", http.StatusForbidden},
		"batch to another topic":        {http.MethodPost, "/v1/pubsub/publish-batch", auth.ActionWrite, "billing", http.StatusForbidden},
		"subscribe inside the selector": {http.MethodGet, "/v1/pubsub/ws", auth.ActionRead, "chat.room1", http.StatusOK},
		"subscribe to another topic":    {http.MethodGet, "/v1/pubsub/ws", auth.ActionRead, "billing", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			r := hop(t, g, tc.method, tc.path, hopNamespace, hopWallet)
			status, reached := serveHopAuthorizing(g, r, auth.Resource{
				Domain: auth.DomainPubsub, Action: tc.action, Name: tc.topic,
			})
			if !reached || status != tc.want {
				t.Errorf("%s %s: reached %v, status %d, want %d", tc.path, tc.topic, reached, status, tc.want)
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

	rec, reached := serveHop(g, keyHop(t, g, http.MethodPost, "/v1/cache/put", "cache"))
	if !reached {
		t.Errorf("a cache key was refused the cache: %d", rec.Code)
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

// A failed grant read was cached as "no grant" for the cache's lifetime, and
// no grant is the whole data plane: a wallet narrowed to sessions/* reached
// every key while the registry was unreachable and for 10s after (review,
// 2026-09-30). It is refused instead, and nothing is cached.
func TestForwardedDataPlane_unreadableGrantIsRefusedNotWidened(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "cache:key=sessions/*"
	registry.failGrants = true

	put := func(key string) (int, bool) {
		r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, hopWallet)
		return serveHopAuthorizing(g, r, auth.Resource{Domain: auth.DomainCache, Action: auth.ActionWrite, Name: key})
	}
	if status, reached := put("tokens/x"); reached || status != http.StatusServiceUnavailable {
		t.Fatalf("with the registry down: reached %v, status %d; want 503 before the handler", reached, status)
	}

	registry.failGrants = false
	if status, reached := put("tokens/x"); !reached || status != http.StatusForbidden {
		t.Fatalf("after the registry recovered: reached %v, status %d; want the selector to refuse (403), not a cached widening", reached, status)
	}
}

// A full cache no longer empties itself: one caller cycling through wallets
// could flush every other caller's entry (security review, 2026-09-30).
func TestGrantCache_fullCacheKeepsLiveEntries(t *testing.T) {
	var c grantCache
	now := time.Now()
	keep := &auth.Grant{Role: auth.RoleRuntime}
	for i := 0; i < narrowedGrantCacheMax*2; i++ {
		c.put("cycle-"+strconv.Itoa(i), nil, now)
	}
	if len(c.entries) > narrowedGrantCacheMax {
		t.Fatalf("cache holds %d entries, over the bound %d", len(c.entries), narrowedGrantCacheMax)
	}
	// Once every entry has expired, making room drops all of them rather
	// than a live one.
	later := now.Add(narrowedGrantTTL + time.Second)
	c.put("after-expiry", keep, later)
	if len(c.entries) != 1 {
		t.Fatalf("expired entries were not dropped first: %d entries remain", len(c.entries))
	}
	if g, ok := c.get("after-expiry", later); !ok || g != keep {
		t.Fatal("the new entry is not served")
	}
}

// Invoking a function is an open route: whether a caller may run it is the
// invoker's decision. A grant narrowed to fn:name= still has to apply, and it
// never did, because an open route resolves no grant (stagenet e2e
// TestCapabilitySocket_lifecycle/fn_selector saw a 200 for another function).
func TestForwardedInvoke_fnSelectorNarrowsTheWallet(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "fn:name=checkout"

	for name, tc := range map[string]struct {
		function string
		want     int
	}{
		"the named function":                  {"checkout", http.StatusOK},
		"another function":                    {"refund", http.StatusForbidden},
		"a name that only starts the same":    {"checkout-2", http.StatusForbidden},
		"the same name in a longer path tail": {"checkoutx", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			r := hop(t, g, http.MethodPost, "/v1/functions/"+tc.function+"/invoke", hopNamespace, hopWallet)
			status, reached := serveHopAuthorizing(g, r, auth.Resource{Domain: auth.SelectorFn, Name: tc.function})
			if !reached || status != tc.want {
				t.Errorf("invoke %s: reached %v, status %d, want %d", tc.function, reached, status, tc.want)
			}
		})
	}
}

// Only a narrowed grant changes anything on the open route: a wallet holding
// the whole role or no grant reaches every function as before, an anonymous
// caller reads no grant at all, and a grant that cannot be read refuses rather
// than widens.
func TestForwardedInvoke_onlyANarrowedGrantChangesTheOpenRoute(t *testing.T) {
	object := auth.Resource{Domain: auth.SelectorFn, Name: "refund"}
	for name, role := range map[string]string{"whole role": "runtime", "no grant": ""} {
		t.Run(name, func(t *testing.T) {
			g, _ := namespaceGatewayForHops(t, role)
			r := hop(t, g, http.MethodPost, "/v1/functions/refund/invoke", hopNamespace, hopWallet)
			if status, reached := serveHopAuthorizing(g, r, object); !reached || status != http.StatusOK {
				t.Errorf("reached %v, status %d, want 200", reached, status)
			}
		})
	}

	t.Run("anonymous", func(t *testing.T) {
		g, registry := namespaceGatewayForHops(t, "runtime")
		registry.resource = "fn:name=checkout"
		registry.queries = 0
		r := httptest.NewRequest(http.MethodPost, "/v1/functions/refund/invoke", nil)
		if status, reached := serveHopAuthorizing(g, r, object); !reached || status != http.StatusOK {
			t.Errorf("reached %v, status %d, want 200", reached, status)
		}
		if registry.queries != 0 {
			t.Errorf("an anonymous invoke read the registry %d times", registry.queries)
		}
	})
}

// POST /v1/invoke/<namespace>/<function> is the canonical invoke route and was
// registered on its own, as a plain open route: a wallet narrowed to one
// function still ran every other one through it.
func TestForwardedInvoke_theInvokeRouteIsNarrowedToo(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.resource = "fn:name=checkout"
	for fn, want := range map[string]int{"checkout": http.StatusOK, "refund": http.StatusForbidden} {
		r := hop(t, g, http.MethodPost, "/v1/invoke/"+hopNamespace+"/"+fn, hopNamespace, hopWallet)
		status, reached := serveHopAuthorizing(g, r, auth.Resource{Domain: auth.SelectorFn, Name: fn})
		if !reached || status != want {
			t.Errorf("/v1/invoke/%s/%s: reached %v, status %d, want %d", hopNamespace, fn, reached, status, want)
		}
	}
}

// A deployment's grant is recorded under the app principal. Its token's
// subject is not a wallet, so the gateway read it as a key: the grant was
// looked for under a service account and never found, and a selector the
// app-grants API accepted narrowed nothing.
func TestForwardedWorkload_theAppGrantSelectorNarrowsTheWorkload(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.principalType = auth.PrincipalApp
	workload := auth.WorkloadSubject(hopNamespace, "web")

	registry.resource = "fn:name=checkout"
	for _, path := range []string{"/v1/invoke/" + hopNamespace + "/", "/v1/functions/"} {
		for fn, want := range map[string]int{"checkout": http.StatusOK, "refund": http.StatusForbidden} {
			target := path + fn
			if path == "/v1/functions/" {
				target += "/invoke"
			}
			r := hop(t, g, http.MethodPost, target, hopNamespace, workload)
			status, reached := serveHopAuthorizing(g, r, auth.Resource{Domain: auth.SelectorFn, Name: fn})
			if !reached || status != want {
				t.Errorf("%s: reached %v, status %d, want %d", target, reached, status, want)
			}
		}
	}

	registry.resource = "cache:key=sessions/*"
	g.narrowedGrants = grantCache{}
	for key, want := range map[string]int{"sessions/abc": http.StatusOK, "tokens/x": http.StatusForbidden} {
		r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, workload)
		status, reached := serveHopAuthorizing(g, r, auth.Resource{Domain: auth.DomainCache, Action: auth.ActionWrite, Name: key})
		if !reached || status != want {
			t.Errorf("cache put %s: reached %v, status %d, want %d", key, reached, status, want)
		}
	}
}

func TestSubjectOwnerType_eachPrincipalKindIsLookedUpUnderItsOwnType(t *testing.T) {
	for sub, want := range map[string]auth.PrincipalType{
		hopWallet: auth.PrincipalWallet,
		auth.WorkloadSubject(hopNamespace, "web"): auth.PrincipalApp,
		"ak_exchanged_key":                        auth.PrincipalServiceAccount,
	} {
		if got := grantPrincipalType(subjectOwnerType(sub)); got != want {
			t.Errorf("%s: looked up as %s, want %s", sub, got, want)
		}
	}
}

// A workload nobody has granted anything reaches nothing, the safe default
// docs/AUTH.md promises; resolving its grant must not turn "none" into the
// data plane a wallet with no grant is given.
func TestForwardedWorkload_withNoGrantReachesNothing(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "")
	registry.principalType = auth.PrincipalApp
	r := hop(t, g, http.MethodPost, "/v1/cache/put", hopNamespace, auth.WorkloadSubject(hopNamespace, "web"))
	status, _ := serveHopAuthorizing(g, r, auth.Resource{Domain: auth.DomainCache, Action: auth.ActionWrite, Name: "sessions/k"})
	if status != http.StatusForbidden {
		t.Errorf("an ungranted workload wrote the cache: status %d, want 403", status)
	}
}

// An app is started before its owner can grant it anything, so its token
// carries no invoke scope for as long as it has not renewed. The invoker asks
// the grant, which has to reach it even when it names no function: an app
// granted runtime was refused every invoke until its first renewal.
func TestForwardedWorkload_theInvokeCarriesItsGrantEvenWithoutASelector(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.principalType = auth.PrincipalApp
	r := hop(t, g, http.MethodPost, "/v1/functions/store/invoke", hopNamespace, auth.WorkloadSubject(hopNamespace, "web"))

	var carried *auth.Grant
	rec := httptest.NewRecorder()
	g.internalAuthMiddleware(g.routePolicyMiddleware(g.authMiddleware(g.authorizationMiddleware(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			carried, _ = r.Context().Value(ctxKeyGrant).(*auth.Grant)
		}))))).ServeHTTP(rec, r)

	if carried == nil || !carried.Scopes().Has(auth.ScopeInvoke) {
		t.Fatalf("the invoke did not carry the workload's runtime grant: %+v (status %d)", carried, rec.Code)
	}
}

// The same wallet with no selector is left as it was: its invoke is the
// invoker's decision and nothing was resolved for it.
func TestForwardedInvoke_aWalletWithoutASelectorCarriesNoGrant(t *testing.T) {
	g, _ := namespaceGatewayForHops(t, "runtime")
	r := hop(t, g, http.MethodPost, "/v1/functions/store/invoke", hopNamespace, hopWallet)

	var carried *auth.Grant
	g.internalAuthMiddleware(g.routePolicyMiddleware(g.authMiddleware(g.authorizationMiddleware(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			carried, _ = r.Context().Value(ctxKeyGrant).(*auth.Grant)
		}))))).ServeHTTP(httptest.NewRecorder(), r)

	if carried != nil {
		t.Errorf("a wallet with no selector carried a grant: %+v", carried)
	}
}
