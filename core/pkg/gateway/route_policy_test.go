package gateway

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Who may call what is decided by three hand-maintained lists of path prefixes:
// isPublicPath, requiredScope and requiresNamespaceOwnership. Nothing connects
// them to the routes they describe, so a route can be added and match none of
// them, or match two that contradict each other, and the only symptom is an
// endpoint that answers the wrong thing to the wrong caller.
//
// That has already happened. /v1/node/enroll was exempted from the scope check
// on the grounds that its handler validates a Bearer invite token — but it was
// never added to isPublicPath, so the API-key middleware refused the invite
// token as a bad API key before the handler ran, and enrolling a node could not
// work at all.
//
// This is not the fix. The fix is a policy declared where the route is
// registered, which is Phase 2. Until then, every registered route has to say
// which kind of route it is, and the three lists have to agree with that.

type routeClass string

const (
	// classOpen is deliberately reachable by anyone: health, version, the
	// key material clients need to verify a token, the login handshake.
	classOpen routeClass = "open"

	// classHandlerAuth is exempt from the API-key middleware because the
	// handler authenticates the caller itself — an invite token, the cluster
	// secret, a signed internal header. Such a route MUST be in isPublicPath:
	// otherwise the middleware refuses the caller's credential as a bad API
	// key and the handler's own check never runs.
	classHandlerAuth routeClass = "handler-auth"

	// classCredential needs an API key or a JWT, checked by the middleware.
	classCredential routeClass = "credential"
)

// routeClasses says what every registered route is. A route missing from here
// fails the test below, which is the point: adding one is a decision about who
// may call it, and it should not be possible to make that decision by accident.
var routeClasses = map[string]routeClass{
	// --- Open ---------------------------------------------------------
	"/health":                   classOpen,
	"/status":                   classOpen,
	"/v1/health":                classOpen,
	"/v1/status":                classOpen,
	"/v1/version":               classOpen,
	"/v1/auth/jwks":             classOpen,
	"/.well-known/jwks.json":    classOpen,
	"/v1/network/status":        classOpen,
	"/v1/network/peers":         classOpen,
	"/v1/namespace/status":      classOpen,
	"/v1/auth/challenge":        classOpen,
	"/v1/auth/verify":           classOpen,
	"/v1/auth/refresh":          classOpen,
	"/v1/auth/logout":           classOpen,
	"/v1/auth/api-key":          classOpen,
	"/v1/auth/phantom/session":  classOpen,
	"/v1/auth/phantom/session/": classOpen,
	"/v1/auth/phantom/complete": classOpen,
	// Called by Caddy on this host. Neither authenticates; both are on the
	// list of things Phase 2 has to give a credential to.
	"/v1/internal/acme/present": classOpen,
	"/v1/internal/acme/cleanup": classOpen,
	"/v1/internal/tls/check":    classOpen,
	// Peer health probing. Returns the node id and nothing else.
	"/v1/internal/ping": classOpen,
	// The invoker decides whether the caller may run the function; a public
	// function is open by design.
	"/v1/invoke/": classOpen,

	// --- The handler authenticates the caller -------------------------
	// Invite token in the handler.
	"/v1/internal/join": classHandlerAuth,
	"/v1/node/enroll":   classHandlerAuth,
	// Cluster secret in the handler.
	"/v1/internal/wg/peer":        classHandlerAuth,
	"/v1/internal/wg/peers":       classHandlerAuth,
	"/v1/internal/wg/peer/remove": classHandlerAuth,
	// Internal header + WireGuard-peer source check in the handler. That
	// header is a constant, which is its own ticket (bug-390); the
	// classification here is what the route is meant to be.
	"/v1/internal/namespace/spawn":              classHandlerAuth,
	"/v1/internal/namespace/repair":             classHandlerAuth,
	"/v1/internal/namespace/webrtc/enable":      classHandlerAuth,
	"/v1/internal/namespace/webrtc/disable":     classHandlerAuth,
	"/v1/internal/namespace/webrtc/status":      classHandlerAuth,
	"/v1/internal/storage/evict":                classHandlerAuth,
	"/v1/internal/deployments/replica/setup":    classHandlerAuth,
	"/v1/internal/deployments/replica/update":   classHandlerAuth,
	"/v1/internal/deployments/replica/rollback": classHandlerAuth,
	"/v1/internal/deployments/replica/teardown": classHandlerAuth,
	// Rate-limited per identity hash inside the handler.
	"/v1/vault/push":   classHandlerAuth,
	"/v1/vault/pull":   classHandlerAuth,
	"/v1/vault/status": classHandlerAuth,
	"/v1/vault/health": classHandlerAuth,

	// --- Needs a credential at the middleware -------------------------
	"/v1/auth/token":    classCredential,
	"/v1/auth/whoami":   classCredential,
	"/v1/audit":         classCredential,
	"/v1/schema-status": classCredential,

	"/v1/cache/get":    classCredential,
	"/v1/cache/put":    classCredential,
	"/v1/cache/delete": classCredential,
	"/v1/cache/mget":   classCredential,
	"/v1/cache/scan":   classCredential,
	"/v1/cache/health": classCredential,

	"/v1/db/sqlite/create":  classCredential,
	"/v1/db/sqlite/query":   classCredential,
	"/v1/db/sqlite/list":    classCredential,
	"/v1/db/sqlite/delete":  classCredential,
	"/v1/db/sqlite/backup":  classCredential,
	"/v1/db/sqlite/backups": classCredential,

	"/v1/deployments/get":            classCredential,
	"/v1/deployments/list":           classCredential,
	"/v1/deployments/delete":         classCredential,
	"/v1/deployments/logs":           classCredential,
	"/v1/deployments/stats":          classCredential,
	"/v1/deployments/events":         classCredential,
	"/v1/deployments/versions":       classCredential,
	"/v1/deployments/rollback":       classCredential,
	"/v1/deployments/env":            classCredential,
	"/v1/deployments/env/set":        classCredential,
	"/v1/deployments/go/upload":      classCredential,
	"/v1/deployments/go/update":      classCredential,
	"/v1/deployments/nextjs/upload":  classCredential,
	"/v1/deployments/nextjs/update":  classCredential,
	"/v1/deployments/nodejs/upload":  classCredential,
	"/v1/deployments/nodejs/update":  classCredential,
	"/v1/deployments/static/upload":  classCredential,
	"/v1/deployments/static/update":  classCredential,
	"/v1/deployments/domains/add":    classCredential,
	"/v1/deployments/domains/list":   classCredential,
	"/v1/deployments/domains/remove": classCredential,
	"/v1/deployments/domains/verify": classCredential,

	"/v1/functions":  classCredential,
	"/v1/functions/": classCredential,

	"/v1/namespaces":                       classCredential,
	"/v1/namespace/list":                   classCredential,
	"/v1/namespace/delete":                 classCredential,
	"/v1/namespace/keys":                   classCredential,
	"/v1/namespace/keys/":                  classCredential,
	"/v1/namespace/rate-limit":             classCredential,
	"/v1/namespace/push-credentials":       classCredential,
	"/v1/namespace/push-credentials/":      classCredential,
	"/v1/namespace/webrtc/enable":          classCredential,
	"/v1/namespace/webrtc/disable":         classCredential,
	"/v1/namespace/webrtc/status":          classCredential,
	"/v1/namespace/webrtc/stealth/enable":  classCredential,
	"/v1/namespace/webrtc/stealth/disable": classCredential,

	"/v1/network/connect":    classCredential,
	"/v1/network/disconnect": classCredential,

	"/v1/node/status":  classCredential,
	"/v1/node/command": classCredential,
	"/v1/node/logs":    classCredential,
	"/v1/node/leave":   classCredential,

	"/v1/operator/invite":        classCredential,
	"/v1/operator/nodes":         classCredential,
	"/v1/operator/node/register": classCredential,

	"/v1/proxy/anon":   classCredential,
	"/v1/proxy/tunnel": classCredential,

	"/v1/pubsub/publish":       classCredential,
	"/v1/pubsub/publish-batch": classCredential,
	"/v1/pubsub/topics":        classCredential,
	"/v1/pubsub/presence":      classCredential,
	"/v1/pubsub/ws":            classCredential,

	"/v1/push/config":   classCredential,
	"/v1/push/send":     classCredential,
	"/v1/push/devices":  classCredential,
	"/v1/push/devices/": classCredential,

	"/v1/rqlite/query":        classCredential,
	"/v1/rqlite/exec":         classCredential,
	"/v1/rqlite/select":       classCredential,
	"/v1/rqlite/find":         classCredential,
	"/v1/rqlite/find-one":     classCredential,
	"/v1/rqlite/transaction":  classCredential,
	"/v1/rqlite/schema":       classCredential,
	"/v1/rqlite/create-table": classCredential,
	"/v1/rqlite/drop-table":   classCredential,
	"/v1/rqlite/export":       classCredential,
	"/v1/rqlite/import":       classCredential,

	"/v1/serverless/ws/connections":  classCredential,
	"/v1/serverless/ws/connections/": classCredential,

	"/v1/storage/upload":  classCredential,
	"/v1/storage/pin":     classCredential,
	"/v1/storage/get/":    classCredential,
	"/v1/storage/status/": classCredential,
	"/v1/storage/unpin/":  classCredential,

	"/v1/webrtc/rooms":            classCredential,
	"/v1/webrtc/signal":           classCredential,
	"/v1/webrtc/turn/credentials": classCredential,
}

// Every route registered on the mux has to be classified, and every
// classification has to belong to a route that exists.
func TestRoutePolicy_everyRegisteredRouteIsClassified(t *testing.T) {
	registered := registeredRoutes(t)
	if len(registered) < 100 {
		t.Fatalf("found %d routes; this test is not reading the route registrations", len(registered))
	}

	seen := map[string]bool{}
	for _, route := range registered {
		seen[route] = true
		if _, ok := routeClasses[route]; !ok {
			t.Errorf("%s is registered but not classified. Say in routeClasses whether it is open "+
				"to anyone, authenticated by its own handler, or needs a credential — the three "+
				"policy lists do not agree on a route nobody has decided about.", route)
		}
	}

	unregistered := []string{}
	for route := range routeClasses {
		if !seen[route] {
			unregistered = append(unregistered, route)
		}
	}
	sort.Strings(unregistered)
	for _, route := range unregistered {
		t.Errorf("%s is classified but no longer registered; a stale entry reads as policy that "+
			"is not applied to anything", route)
	}
}

// The contradiction that made /v1/node/enroll unreachable: a route whose
// handler authenticates the caller has to be exempt from the middleware that
// would otherwise refuse that caller's credential first.
func TestRoutePolicy_aRouteThatAuthenticatesItselfIsExemptFromTheMiddleware(t *testing.T) {
	for _, route := range registeredRoutes(t) {
		class := routeClasses[route]
		if class != classOpen && class != classHandlerAuth {
			continue
		}
		if !isPublicPath(route) {
			t.Errorf("%s is %s but isPublicPath says no, so the API-key middleware refuses the "+
				"caller before the route is reached. This is exactly how enrolling a node was "+
				"impossible: the handler checked a Bearer invite token that the middleware had "+
				"already rejected as a bad API key.", route, class)
		}
	}
}

func TestRoutePolicy_aRouteThatNeedsACredentialIsNotPublic(t *testing.T) {
	for _, route := range registeredRoutes(t) {
		if routeClasses[route] != classCredential {
			continue
		}
		if isPublicPath(route) {
			t.Errorf("%s needs a credential but isPublicPath says it is open, so no credential is "+
				"ever asked for", route)
		}
	}
}

// A scope or an ownership requirement on a public path is a requirement that
// never runs: both middlewares return early for a public path. It reads as
// protection and is not.
func TestRoutePolicy_apublicPathCarriesNoRequirementThatCannotRun(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, route := range registeredRoutes(t) {
		if !isPublicPath(route) {
			continue
		}
		for _, method := range methods {
			if scope := requiredScope(method, route); scope != "" {
				t.Errorf("%s %s is public but requiredScope demands %q. scopeMiddleware returns "+
					"early for a public path, so that grant is never checked.", method, route, scope)
			}
		}
		if requiresNamespaceOwnership(route) {
			t.Errorf("%s is public but requiresNamespaceOwnership says yes. authorizationMiddleware "+
				"returns early for a public path, so the ownership check never runs.", route)
		}
	}
}

// Ownership is checked against the namespace a credential belongs to, so a
// route that requires it must also require a credential.
func TestRoutePolicy_ownershipImpliesACredential(t *testing.T) {
	for _, route := range registeredRoutes(t) {
		if !requiresNamespaceOwnership(route) {
			continue
		}
		if routeClasses[route] != classCredential {
			t.Errorf("%s requires namespace ownership but is classified %q; there is no credential "+
				"to own anything with", route, routeClasses[route])
		}
	}
}

// The names in the classification are the ones the lists use. A typo would
// silently classify nothing.
func TestRouteClasses_holdNoMalformedPaths(t *testing.T) {
	for route := range routeClasses {
		if !strings.HasPrefix(route, "/") {
			t.Errorf("%q is not a path", route)
		}
		if strings.Contains(route, " ") {
			t.Errorf("%q contains a space", route)
		}
	}
}
