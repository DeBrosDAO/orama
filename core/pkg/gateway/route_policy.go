package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Who may call what.
//
// This replaces isPublicPath, requiredScope and requiresNamespaceOwnership —
// three hand-maintained lists of path prefixes that nothing tied to the routes
// they described. See package routepolicy for what that cost.
//
// Every route this gateway serves is declared here, and a route that is not
// declared cannot be registered: routepolicy.Mux panics on it, and a test fails
// on it first. The middleware asks the table which policy the request's route
// carries; it never looks at the path itself.

var (
	// open is a route deliberately reachable by anyone.
	policyOpen = routepolicy.Policy{Access: routepolicy.Open}

	// handlerAuth is a route whose handler authenticates the caller itself. It
	// must be exempt from the API-key middleware, or that middleware refuses
	// the caller's credential — an invite token, the cluster secret — as a bad
	// API key before the handler that understands it runs.
	policyHandlerAuth = routepolicy.Policy{Access: routepolicy.HandlerAuth}

	// credential is a route any valid credential reaches.
	policyCredential = routepolicy.Policy{}

	// admin is the control plane.
	policyAdmin = routepolicy.Policy{Scope: auth.ScopeAdmin}

	// adminOwned is the control plane on a namespace's own resources: the
	// caller must also hold a live grant in it.
	policyAdminOwned = routepolicy.Policy{Scope: auth.ScopeAdmin, Ownership: true}
)

// dataPlane is a data-plane grant, with the kind of token it requires.
//
// ownership says whether the caller must additionally hold a live grant in the
// namespace. Storage and cache do not ask for one, which is why a resource
// selector on a storage grant authorises nothing yet: no grant is resolved on
// those requests to carry a selector. See chg-392.
func dataPlane(scope string, ownership bool, token routepolicy.TokenRequirement) routepolicy.Policy {
	return routepolicy.Policy{Scope: scope, Ownership: ownership, Token: token}
}

// gatewayRoutes is the policy of every route. It is built once and read from
// every request.
var gatewayRoutes = buildRoutePolicies()

func buildRoutePolicies() *routepolicy.Table {
	t := routepolicy.NewTable()

	// --- Open to anyone ------------------------------------------------
	t.Add(policyOpen,
		"/health", "/status",
		"/v1/health", "/v1/status", "/v1/version",
		// The key material a client needs to verify a token it was given.
		"/v1/auth/jwks", "/.well-known/jwks.json",
		// The login handshake. Nobody has a credential yet, which is the point.
		"/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/refresh",
		"/v1/auth/logout", "/v1/auth/api-key",
		// The device authorization grant. The machine asking has no credential
		// — that is what it is asking for — and approving one costs a wallet
		// signature, which the handler verifies exactly as /v1/auth/verify does.
		"/v1/auth/device", "/v1/auth/device/approve", "/v1/auth/device/token",
		"/v1/network/status", "/v1/network/peers",
		// Polled while a namespace's cluster is still provisioning, by a client
		// that has not been given anything to poll it with yet.
		"/v1/namespace/status",
		// Called by Caddy on this host; neither authenticates. Both are on the
		// list of things Phase 2 has to give a credential to.
		"/v1/internal/acme/present", "/v1/internal/acme/cleanup", "/v1/internal/tls/check",
		// Peer health probing. Returns the node id and nothing else.
		"/v1/internal/ping",
		// The invoker decides whether the caller may run the function, and a
		// public function is open by design.
		"/v1/invoke/",
	)

	// --- The handler authenticates the caller --------------------------
	t.Add(policyHandlerAuth,
		// Invite token, validated and consumed single-use in the handler.
		"/v1/internal/join", "/v1/node/enroll",
		// Cluster secret in the handler.
		"/v1/internal/wg/peer", "/v1/internal/wg/peers", "/v1/internal/wg/peer/remove",
		// Signed internal header plus a WireGuard-peer source check.
		"/v1/internal/namespace/spawn", "/v1/internal/namespace/repair",
		"/v1/internal/storage/evict",
		"/v1/internal/deployments/replica/setup", "/v1/internal/deployments/replica/update",
		"/v1/internal/deployments/replica/rollback", "/v1/internal/deployments/replica/teardown",
		// Rate-limited per identity hash inside the handler.
		"/v1/vault/push", "/v1/vault/pull", "/v1/vault/status", "/v1/vault/health",
	)

	// --- Any valid credential ------------------------------------------
	t.Add(policyCredential,
		// Exchanging a key for a token, and asking what the credential is.
		"/v1/auth/token", "/v1/auth/whoami",
		// A wallet's own sessions. The handler refuses anything but a token
		// from a signed-in wallet, and reads whose sessions from that token.
		"/v1/auth/sessions", "/v1/auth/sessions/",
		// A workload renewing its own token. It needs the token it is renewing
		// and nothing else, which is what the handler checks.
		"/v1/auth/renew",
		// A read of the gateway's own schema version.
		"/v1/schema-status",
		// Whether WebRTC is on. The switches beside it are admin.
		"/v1/namespace/webrtc/status",
	)

	// --- Control plane, cluster-wide -----------------------------------
	t.Add(policyAdmin,
		// The record of who was given what and when. It names the namespace's
		// wallets and the times they sign in, so reading it is the owner's, not
		// any credential that happens to belong to the namespace.
		"/v1/audit",
		"/v1/db/sqlite/create", "/v1/db/sqlite/query", "/v1/db/sqlite/list",
		"/v1/db/sqlite/delete", "/v1/db/sqlite/backup", "/v1/db/sqlite/backups",
		"/v1/deployments/list", "/v1/deployments/get", "/v1/deployments/delete",
		"/v1/deployments/rollback", "/v1/deployments/versions", "/v1/deployments/logs",
		"/v1/deployments/stats", "/v1/deployments/events",
		"/v1/deployments/env", "/v1/deployments/env/set",
		"/v1/deployments/grants",
		"/v1/deployments/static/upload", "/v1/deployments/static/update",
		"/v1/deployments/nextjs/upload", "/v1/deployments/nextjs/update",
		"/v1/deployments/go/upload", "/v1/deployments/go/update",
		"/v1/deployments/nodejs/upload", "/v1/deployments/nodejs/update",
		"/v1/deployments/domains/add", "/v1/deployments/domains/verify",
		"/v1/deployments/domains/list", "/v1/deployments/domains/remove",
		"/v1/namespace/delete", "/v1/namespace/list", "/v1/namespaces",
		"/v1/namespace/rate-limit",
		"/v1/namespace/webrtc/enable", "/v1/namespace/webrtc/disable",
		"/v1/namespace/webrtc/stealth/enable", "/v1/namespace/webrtc/stealth/disable",
		// Topology mutation and node operation: an operator's. The handlers
		// additionally require the caller's wallet to be on the operator list.
		"/v1/network/connect", "/v1/network/disconnect",
		"/v1/node/status", "/v1/node/command", "/v1/node/logs", "/v1/node/leave",
		// Minting a cluster invite hands out the cluster secret, the swarm key
		// and every other secret the cluster holds. This had no entry at all
		// and fell through to "any valid credential".
		"/v1/operator/invite", "/v1/operator/nodes", "/v1/operator/node/register",
		"/v1/operator/rotate-signing-key",
	)

	// --- Control plane on a namespace's own resources ------------------
	t.Add(policyAdminOwned,
		// The raw database.
		"/v1/rqlite/export", "/v1/rqlite/import",
		// Handing out authority in a namespace is the control plane's own
		// control plane. Transferring goes further and needs the owner, which
		// the handler checks: a grant set cannot express "owner", only what an
		// owner may do.
		"/v1/namespace/members", "/v1/namespace/members/",
		"/v1/namespace/push-credentials", "/v1/namespace/push-credentials/",
		"/v1/push/config", "/v1/push/send",
		"/v1/serverless/ws/connections", "/v1/serverless/ws/connections/",
		// Function management. /v1/functions/ is one handler serving several
		// operations and is declared below.
		"/v1/functions",
	)

	// Scoped API-key management operates on the MAIN cluster registry, where
	// keys are validated. A namespace gateway's own RQLite has no authoritative
	// api_keys table, so a key written there would never authenticate.
	keyManagement := policyAdminOwned
	keyManagement.MainGateway = true
	t.Add(keyManagement, "/v1/namespace/keys", "/v1/namespace/keys/")

	// --- Data plane ----------------------------------------------------
	//
	// storage, webrtc and proxy additionally require a genuine logged-in user.
	// That is what makes an extracted runtime key worthless: the key alone
	// reaches none of them.
	t.Add(dataPlane(auth.ScopeStorage, false, routepolicy.WalletToken),
		"/v1/storage/upload", "/v1/storage/pin", "/v1/storage/get/", "/v1/storage/status/")
	t.Add(dataPlane(auth.ScopeWebRTC, true, routepolicy.WalletToken),
		"/v1/webrtc/turn/credentials", "/v1/webrtc/signal", "/v1/webrtc/rooms")
	t.Add(dataPlane(auth.ScopeProxy, true, routepolicy.WalletToken),
		"/v1/proxy/anon", "/v1/proxy/tunnel")
	t.Add(dataPlane(auth.ScopePubsub, true, routepolicy.AnyCredential),
		"/v1/pubsub/ws", "/v1/pubsub/publish", "/v1/pubsub/publish-batch",
		"/v1/pubsub/topics", "/v1/pubsub/presence")
	t.Add(dataPlane(auth.ScopePush, true, routepolicy.AnyCredential),
		"/v1/push/devices", "/v1/push/devices/")
	t.Add(dataPlane(auth.ScopeCache, false, routepolicy.AnyCredential),
		"/v1/cache/health", "/v1/cache/get", "/v1/cache/mget",
		"/v1/cache/put", "/v1/cache/delete", "/v1/cache/scan")

	// Unpinning is the one storage operation that does not need a logged-in
	// user (bugboard #151). It is namespace-ownership-checked in its handler
	// and can only DROP the namespace's own pins — it never reads or uploads —
	// so a userless server-side reclaim (cron, avatar GC) may prove possession
	// of its storage-scoped key by exchanging it for a token. A bare key still
	// fails. Upload, get and pin keep the strict requirement.
	t.AddDynamic("/v1/storage/unpin/", func(r *http.Request) routepolicy.Policy {
		if r.Method == http.MethodDelete {
			return dataPlane(auth.ScopeStorage, false, routepolicy.AnyToken)
		}
		return dataPlane(auth.ScopeStorage, false, routepolicy.WalletToken)
	})

	// The rqlite ORM composes its own patterns from a base path and reports
	// them, so they are declared from the same list it registers.
	t.Add(policyAdminOwned, ormGatewayRoutes()...)

	// One registered route serves every operation on a named function, and the
	// handler dispatches on the rest of the path. Its policy has to dispatch
	// the same way, so it is declared by the package that does the dispatching.
	t.AddDynamic("/v1/functions/", functionRoutePolicy)

	return t
}

// policyFor is the policy of the route this request matches.
func (g *Gateway) policyFor(r *http.Request) routepolicy.Policy {
	if p, ok := r.Context().Value(ctxKeyRoutePolicy).(routepolicy.Policy); ok {
		return p
	}
	return gatewayRoutes.For(r)
}

// routePolicyMiddleware resolves the policy once and puts it on the request, so
// the four places that read it agree by construction and the mux is walked once
// rather than once per middleware.
func (g *Gateway) routePolicyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, withRoutePolicy(r, gatewayRoutes.For(r)))
	})
}

// ormGatewayRoutes is the pattern list the rqlite ORM gateway registers under
// the gateway's base path.
func ormGatewayRoutes() []string {
	orm := &rqlite.HTTPGateway{BasePath: ormBasePath}
	return orm.Routes()
}

// functionRoutePolicy is the policy of one operation on /v1/functions/.
//
// Invoking is public: the invoker decides whether the caller may run the
// function, and a public function is open by design. The WebSocket is an invoke
// transport and takes the invoke grant. Everything else — deploying, deleting,
// logs, triggers, secrets — is the control plane.
func functionRoutePolicy(r *http.Request) routepolicy.Policy {
	path := strings.TrimPrefix(r.URL.Path, "/v1/functions/")
	switch {
	case strings.HasSuffix(path, "/invoke"):
		return policyOpen
	case strings.HasSuffix(path, "/ws"):
		return routepolicy.Policy{Scope: auth.ScopeInvoke, Ownership: true}
	default:
		return policyAdminOwned
	}
}

// ormBasePath is where the rqlite ORM gateway mounts. It is the same constant
// the routes use to mount it, so the declaration and the registration cannot
// name different paths.
const ormBasePath = "/v1/rqlite"

// ctxKeyRoutePolicy carries the matched route's policy on the request.
type routePolicyKey struct{}

var ctxKeyRoutePolicy = routePolicyKey{}

func withRoutePolicy(r *http.Request, p routepolicy.Policy) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKeyRoutePolicy, p))
}
