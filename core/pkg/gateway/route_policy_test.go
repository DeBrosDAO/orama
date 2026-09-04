package gateway

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

// Who may call what used to be three hand-maintained lists of path prefixes —
// isPublicPath, requiredScope and requiresNamespaceOwnership — with nothing
// connecting any of them to the routes they described. A route could match none
// of them, or two that disagreed, and the only symptom was an endpoint
// answering the wrong thing to the wrong caller: /v1/node/enroll was exempted
// from the scope check and never made public, so the API-key middleware refused
// the invite token as a bad API key and enrolling a node could not work.
//
// The policy is declared with the route now, and these are what keep the
// declaration and the routes in step.

// The registration itself fails on an undeclared pattern — routepolicy.Mux
// panics rather than serving a route nobody decided about — but that is a
// runtime check on a gateway that is already starting. This is the same check,
// against the source, before anything runs.
func TestRoutePolicy_everyRegisteredRouteIsDeclared(t *testing.T) {
	registered := registeredRoutes(t)
	if len(registered) < 100 {
		t.Fatalf("found %d routes; this test is not reading the route registrations", len(registered))
	}

	declared := map[string]bool{}
	for _, pattern := range gatewayRoutes.Patterns() {
		declared[pattern] = true
	}

	seen := map[string]bool{}
	for _, route := range registered {
		seen[route] = true
		if !declared[route] {
			t.Errorf("%s is registered with no policy. Say in route_policy.go who may call it: "+
				"a route nobody has decided about would be reachable by any credential at all.", route)
		}
	}

	stale := []string{}
	for pattern := range declared {
		if !seen[pattern] {
			stale = append(stale, pattern)
		}
	}
	sort.Strings(stale)
	for _, pattern := range stale {
		t.Errorf("%s has a policy and no route; a stale declaration reads as protection that is "+
			"not applied to anything", pattern)
	}
}

// publicRoutes is every route reachable without a credential the middleware
// checks. It is checked in so that making a route public is a diff somebody
// reads, not a line in a policy table that happens to say Open.
//
// A route is here for one of two reasons: it is deliberately open to anyone, or
// its handler authenticates the caller itself and the middleware must not
// refuse that caller's credential first.
var publicRoutes = []string{
	"/.well-known/jwks.json",
	"/health",
	"/status",
	"/v1/auth/api-key",
	"/v1/auth/challenge",
	"/v1/auth/jwks",
	"/v1/auth/logout",
	"/v1/auth/refresh",
	"/v1/auth/verify",
	"/v1/health",
	"/v1/internal/acme/cleanup",
	"/v1/internal/acme/present",
	"/v1/internal/deployments/replica/rollback",
	"/v1/internal/deployments/replica/setup",
	"/v1/internal/deployments/replica/teardown",
	"/v1/internal/deployments/replica/update",
	"/v1/internal/join",
	"/v1/internal/namespace/repair",
	"/v1/internal/namespace/spawn",
	"/v1/internal/ping",
	"/v1/internal/storage/evict",
	"/v1/internal/tls/check",
	"/v1/internal/wg/peer",
	"/v1/internal/wg/peer/remove",
	"/v1/internal/wg/peers",
	"/v1/invoke/",
	"/v1/namespace/status",
	"/v1/network/peers",
	"/v1/network/status",
	"/v1/node/enroll",
	"/v1/status",
	"/v1/vault/health",
	"/v1/vault/pull",
	"/v1/vault/push",
	"/v1/vault/status",
	"/v1/version",
}

func TestRoutePolicy_thePublicSetIsTheOneThatWasReviewed(t *testing.T) {
	want := map[string]bool{}
	for _, route := range publicRoutes {
		want[route] = true
	}

	for _, pattern := range gatewayRoutes.Patterns() {
		anonymous := policyOf(http.MethodGet, pattern).Access.Anonymous()
		switch {
		case anonymous && !want[pattern]:
			t.Errorf("%s is reachable with no credential and is not in publicRoutes. If that is "+
				"intended, add it there — making a route public should be a diff somebody reads.", pattern)
		case !anonymous && want[pattern]:
			t.Errorf("%s is in publicRoutes but now needs a credential; remove it, or the list "+
				"stops meaning anything", pattern)
		}
	}
}

// A grant or an ownership requirement on a public route is a requirement that
// never runs: both middlewares return early for one. It reads as protection and
// is not.
func TestRoutePolicy_aPublicRouteCarriesNoRequirementThatCannotRun(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, pattern := range gatewayRoutes.Patterns() {
		for _, method := range methods {
			policy := policyOf(method, pattern)
			if !policy.Access.Anonymous() {
				continue
			}
			if policy.Scope != "" {
				t.Errorf("%s %s is public but requires the %q grant; the scope gate returns early "+
					"for a public route, so that grant is never checked", method, pattern, policy.Scope)
			}
			if policy.Ownership {
				t.Errorf("%s %s is public but requires a namespace grant; the authorization gate "+
					"returns early for a public route, so the check never runs", method, pattern)
			}
			if policy.Token != routepolicy.AnyCredential {
				t.Errorf("%s %s is public but asks for a token; nothing checks it", method, pattern)
			}
		}
	}
}

// A token requirement is enforced only once a grant has been checked, so a
// route asking for one without a grant asks for nothing.
func TestRoutePolicy_aTokenRequirementNeedsAGrantToHangOn(t *testing.T) {
	for _, pattern := range gatewayRoutes.Patterns() {
		policy := policyOf(http.MethodPost, pattern)
		if policy.Token != routepolicy.AnyCredential && policy.Scope == "" {
			t.Errorf("%s asks for a token but no grant; the scope gate returns before the token "+
				"check when no grant is required", pattern)
		}
	}
}

// Ownership is checked against the namespace a credential belongs to, so a
// route requiring it must require a credential.
func TestRoutePolicy_ownershipImpliesACredential(t *testing.T) {
	for _, pattern := range gatewayRoutes.Patterns() {
		policy := policyOf(http.MethodPost, pattern)
		if policy.Ownership && policy.Access.Anonymous() {
			t.Errorf("%s requires a namespace grant but needs no credential; there is nothing to "+
				"hold a grant", pattern)
		}
	}
}

// Every grant a route names has to be one the scope model knows, or the route
// is unreachable by anything: no credential can hold a grant that does not
// exist.
func TestRoutePolicy_everyRequiredGrantIsAKnownOne(t *testing.T) {
	known := map[string]bool{auth.ScopeAdmin: true}
	for _, scope := range auth.AllGrants() {
		known[scope] = true
	}

	for _, pattern := range gatewayRoutes.Patterns() {
		scope := policyOf(http.MethodPost, pattern).Scope
		if scope != "" && !known[scope] {
			t.Errorf("%s requires the %q grant, which no credential can hold", pattern, scope)
		}
	}
}

// The declared patterns are what the matcher is built from. A typo would
// silently classify nothing.
func TestRoutePolicy_declaresNoMalformedPattern(t *testing.T) {
	for _, pattern := range gatewayRoutes.Patterns() {
		if !strings.HasPrefix(pattern, "/") {
			t.Errorf("%q is not a path", pattern)
		}
		if strings.Contains(pattern, " ") {
			t.Errorf("%q contains a space", pattern)
		}
	}
}

// A path that matches no route resolves to the zero policy: a credential is
// required and no grant reaches anything. An unmatched path is not an open one,
// and the prefix lists used to make several of them exactly that.
func TestRoutePolicy_anUnmatchedPathIsNotPublic(t *testing.T) {
	for _, path := range []string{
		"/v1/unknown", "/v1/pubsub", "/v1/namespace/statusfoo", "/v1/auth/simple-key",
		"/v1/auth/phantom/session", "/rqlite", "/v1/deployments",
	} {
		policy := policyOf(http.MethodPost, path)
		if policy.Access.Anonymous() {
			t.Errorf("%q matches no route and is reachable without a credential", path)
		}
		if policy.Scope != "" || policy.Ownership {
			t.Errorf("%q matches no route but carries a requirement: %+v", path, policy)
		}
	}
}

// Registering the real routes is what proves the declaration and the wiring
// agree: routepolicy.Mux panics on a pattern with no policy, so a gateway that
// builds its routes at all has one for every route it serves.
func TestRoutePolicy_theRealRoutesRegister(t *testing.T) {
	g := chainGateway(t, "alice", &stubKeyDatabase{})
	handler := g.Routes()
	if handler == nil {
		t.Fatal("Routes returned nothing")
	}
}

// The serverless package registers its own handlers, so it reports its patterns
// and the gateway checks them against the table before wiring them. A pattern
// it registers without reporting would be served with no policy.
func TestRoutePolicy_serverlessReportsEveryRouteItRegisters(t *testing.T) {
	reported := map[string]bool{}
	for _, pattern := range serverlesshandlers.Routes() {
		reported[pattern] = true
	}

	registered := literalMuxPatterns(t,
		filepath.Join(repoRootFor(t), "core/pkg/gateway/handlers/serverless/routes.go"))
	if len(registered) == 0 {
		t.Fatal("no routes found; this test is not reading the registrations")
	}
	for _, pattern := range registered {
		if !reported[pattern] {
			t.Errorf("serverless registers %s and does not report it, so its policy is never "+
				"checked and it is served with none", pattern)
		}
	}
	for pattern := range reported {
		found := false
		for _, p := range registered {
			if p == pattern {
				found = true
			}
		}
		if !found {
			t.Errorf("serverless reports %s and does not register it", pattern)
		}
	}
}
