package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func wsUpgrade(target, remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.RemoteAddr = remoteAddr
	return r
}

// Only a WebSocket upgrade of a function's /ws that carries a capability is
// one; the same path with a credential, a plain GET, or another route is not.
func TestIsCapabilityUpgrade(t *testing.T) {
	if !isCapabilityUpgrade(wsUpgrade("/v1/functions/rpc/ws?namespace=anchat&cap=abc", "203.0.113.7:1")) {
		t.Error("a capability upgrade was not recognised")
	}
	plainGet := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc/ws?cap=abc", nil)
	for name, r := range map[string]*http.Request{
		"no capability":     wsUpgrade("/v1/functions/rpc/ws?namespace=anchat", "203.0.113.7:1"),
		"an empty cap":      wsUpgrade("/v1/functions/rpc/ws?cap=", "203.0.113.7:1"),
		"not an upgrade":    plainGet,
		"another route":     wsUpgrade("/v1/pubsub/ws?cap=abc", "203.0.113.7:1"),
		"a function invoke": wsUpgrade("/v1/functions/rpc/invoke?cap=abc", "203.0.113.7:1"),
	} {
		if isCapabilityUpgrade(r) {
			t.Errorf("%s was taken for a capability upgrade", name)
		}
	}
}

// The handler authorizes a capability upgrade itself, so no middleware may
// demand a credential of it; every other /ws still takes the invoke grant.
func TestFunctionRoutePolicy_aCapabilityUpgradeIsHandlerAuth(t *testing.T) {
	if got := functionRoutePolicy(wsUpgrade("/v1/functions/rpc/ws?cap=abc", "203.0.113.7:1")); got.Access != routepolicy.HandlerAuth {
		t.Errorf("a capability upgrade has policy %+v, want handler-auth", got)
	}
	if got := functionRoutePolicy(wsUpgrade("/v1/functions/rpc/ws", "203.0.113.7:1")); got.Access.Anonymous() {
		t.Errorf("a credential upgrade became anonymous: %+v", got)
	}
}

// The bug: the capability upgrade was recognised by its path's suffix, so a
// trigger or secret route ending in /ws, sent with upgrade headers and any
// cap, became anonymous and reached the trigger and secret handlers — which
// take the namespace from the query. Only a GET of exactly {fn}/ws counts.
func TestFunctionRoutePolicy_nothingButTheSocketBecomesAnonymous(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/v1/functions/victim/triggers/ws?namespace=v&cap=x"},
		{http.MethodGet, "/v1/functions/victim/triggers/ws?namespace=v&cap=x"},
		{http.MethodDelete, "/v1/functions/victim/triggers/ws?namespace=v&cap=x"},
		{http.MethodDelete, "/v1/functions/secrets/ws?namespace=v&cap=x"},
		{http.MethodGet, "/v1/functions/secrets/ws?namespace=v&cap=x"},
		{http.MethodPost, "/v1/functions/victim/ws?namespace=v&cap=x"},
	} {
		r := wsUpgrade(tc.target, "203.0.113.7:1")
		r.Method = tc.method
		if isCapabilityUpgrade(r) || functionRoutePolicy(r).Access.Anonymous() {
			t.Errorf("%s %s is anonymous", tc.method, tc.target)
		}
	}
}

// The same suffix match made the trigger and secret routes that end in
// /invoke public, with no upgrade needed.
func TestFunctionRoutePolicy_onlyAFunctionsInvokeIsPublic(t *testing.T) {
	for _, target := range []string{
		"/v1/functions/victim/triggers/invoke?namespace=v",
		"/v1/functions/secrets/invoke?namespace=v",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			r := httptest.NewRequest(method, target, nil)
			if functionRoutePolicy(r).Access.Anonymous() {
				t.Errorf("%s %s is anonymous", method, target)
			}
		}
	}
	if got := functionRoutePolicy(httptest.NewRequest(http.MethodPost, "/v1/functions/fn/invoke", nil)); got.Access != routepolicy.Open {
		t.Errorf("a function's invoke is %+v, want open", got)
	}
}

// Capability upgrades from one address get a bucket of their own; the rest of
// that address's traffic is not charged to it.
func TestRateLimitMiddleware_capabilityUpgradesHaveTheirOwnBucket(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:                logger,
		rateLimiter:           NewRateLimiter(100000, 100000),
		capabilityRateLimiter: NewRateLimiter(60, 2),
	}
	served := 0
	handler := g.rateLimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served++ }))

	var last int
	for range 5 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, wsUpgrade("/v1/functions/rpc/ws?cap=abc", "203.0.113.7:1"))
		last = w.Code
	}
	if served != 2 || last != http.StatusTooManyRequests {
		t.Errorf("served %d of 5 capability upgrades (last %d); the burst is 2", served, last)
	}
	handler.ServeHTTP(httptest.NewRecorder(), wsUpgrade("/v1/functions/rpc/ws", "203.0.113.7:1"))
	if served != 3 {
		t.Error("a credential upgrade from the same address was charged to the capability bucket")
	}
}

func TestCapabilityRateLimiterIsConfigured(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	configureRateLimiters(g)
	if g.capabilityRateLimiter == nil || g.capabilityRateLimiter.burst != capabilityUpgradeBurst {
		t.Errorf("capability rate limiter = %+v", g.capabilityRateLimiter)
	}
}
