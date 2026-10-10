package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"go.uber.org/zap"
)

// callerPermissions is what this request's credential may do.
//
// One path, deliberately. There were three, and they could disagree: an
// API-key-exchanged JWT carried an authoritative `scopes` claim, a wallet JWT
// ignored any claim and used the grant the authorization middleware resolved,
// and a bare API key used the scope set the auth middleware put on the context.
// Three answers to one question is three places for them to drift, and it
// produced an asymmetry nobody chose — narrowing a grant took effect at once
// for a wallet and only at the next token for a key.
//
// A token says who you are. What you may do is read from the grant, here.
func (g *Gateway) callerPermissions(r *http.Request) auth.PermissionSet {
	ctx := r.Context()

	// The lobby belongs to nobody and holds nothing (docs/whitepaper/technical-reference/vol1/12-gateway-architecture.md, "The
	// lobby"): its session reaches only the routes that ask for no
	// permission. That holds for a grant too — a cluster from before
	// ownership was fixed may still record one for whichever wallet signed in
	// to it first. A wallet session naming no namespace is read the same way.
	ns, _ := ctx.Value(CtxKeyNamespaceOverride).(string)
	inLobby := auth.IsLobbyNamespace(ns) || strings.TrimSpace(ns) == ""

	// The grant the authorization middleware resolved for this namespace, for
	// whichever principal the credential named. It is the answer whenever the
	// route resolves one.
	if grant, _ := ctx.Value(ctxKeyGrant).(*auth.Grant); grant != nil {
		if auth.IsLobbyNamespace(ns) {
			return auth.PermissionSet{}
		}
		if grant.PrincipalType == auth.PrincipalServiceAccount {
			// A key's role is runtime or admin and nothing between, so its
			// own scopes say what it holds.
			scopes, _ := ctx.Value(ctxKeyScopes).(auth.ScopeSet)
			return auth.KeyPermissions(scopes.Canonical(), grant.Role, grant.Resource)
		}
		return auth.PermissionsFor(grant.Role, grant.Resource)
	}

	// No grant was resolved: every route the ownership gate does not cover.
	// What the credential is decides what it gets there.
	if claims, ok := ctx.Value(ctxKeyJWT).(*auth.JWTClaims); ok && claims != nil {
		if isAPIKeySubject(claims.Sub) {
			// A key's own permissions, from the row rather than from the claim
			// the token carries. The claim is what made a narrowed grant take
			// a token lifetime to bite.
			if scopes, ok := ctx.Value(ctxKeyScopes).(auth.ScopeSet); ok {
				return auth.PermissionsFromScopes(scopes.Canonical())
			}
			return auth.PermissionSet{}
		}
		// A lobby session used to get the data plane like any other, so every
		// signed-in wallet shared the index namespace's cache, pub/sub and
		// storage.
		if inLobby {
			return auth.PermissionSet{}
		}
		// A logged-in user with no grant in this namespace gets the data
		// plane, as they always have, except publishing to pub/sub: see
		// auth.NoGrantPermissions.
		return auth.NoGrantPermissions()
	}

	if scopes, ok := ctx.Value(ctxKeyScopes).(auth.ScopeSet); ok {
		return auth.PermissionsFromScopes(scopes.Canonical())
	}

	// No identity resolved. The auth middleware has already gated every
	// non-public route, so this is a route that needs none — and it holds
	// nothing either way.
	return auth.PermissionSet{}
}

// isAPIKeySubject reports whether a JWT subject is an API key, as minted by the
// API-key→JWT exchange, rather than a SIWE wallet address. This is the single
// signal used to (a) decide a JWT is not a genuine user (hasWalletJWT) and (b)
// decide whether to trust an embedded scopes claim (callerScopes).
//
// It asks whether the subject IS a wallet rather than whether it looks like a
// key: a subject nothing recognises is then a key, which holds only what its
// row says, rather than a logged-in user. See auth.IsWalletSubject.
func isAPIKeySubject(sub string) bool {
	return auth.IsAPIKeySubject(sub)
}

// hasWalletJWT reports whether the request carries a genuine end-user (SIWE
// wallet) JWT — as opposed to an API-key-exchanged JWT (sub is the key). This
// is what layer-1 accepts: an exchanged runtime-key JWT must NOT satisfy it,
// or the escalation hole reopens.
func hasWalletJWT(r *http.Request) bool {
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			sub := strings.TrimSpace(claims.Sub)
			if sub == "" {
				return false
			}
			return !isAPIKeySubject(sub) // an exchanged-key JWT is not a user
		}
	}
	return false
}

// hasAnyJWT reports whether the request carries ANY verified JWT — a genuine
// wallet JWT OR an API-key-exchanged one. It is what a route asking for
// routepolicy.AnyToken accepts: a serverless cron or job has no logged-in user,
// so it proves possession of its key by exchanging it. A bare API key (no JWT)
// yields false, so "an extracted key is inert on its own" still holds.
func hasAnyJWT(r *http.Request) bool {
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			return strings.TrimSpace(claims.Sub) != ""
		}
	}
	return false
}

// scopeMiddleware enforces the API-key scope model. It runs after the
// authorization (ownership) middleware, so ownership has already been verified;
// this layer additionally (a) rejects a credential whose grant set does not
// cover the operation (403 INSUFFICIENT_SCOPE, bugboard #148), and (b) requires
// the kind of token the route asks for unless the caller is admin — the layer-1
// hardening that makes an extracted runtime key useless without a logged-in
// user.
//
// What a route requires comes from its declared policy, never from its path.
func (g *Gateway) scopeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := g.policyFor(r)
		if r.Method == http.MethodOptions || policy.Access.Anonymous() {
			next.ServeHTTP(w, r)
			return
		}
		perms := g.callerPermissions(r)
		// Carried on the request so the handler narrows the same answer the
		// gate widened. Two computations of one thing is two things to keep
		// in step.
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.Permissions, perms))

		if policy.Domain != "" {
			required := auth.Resource{
				Domain: auth.Domain(policy.Domain),
				Action: auth.Action(policy.Action),
			}
			// The gate's question, not the handler's: does this credential
			// reach the domain at all, before anything knows which object.
			if !perms.PermitsDomain(required.Domain, required.Action) {
				g.logger.ComponentWarn("gateway", "request rejected: insufficient permission",
					zap.String("path", r.URL.Path),
					zap.String("required", required.String()),
				)
				// What is missing goes in a field, not only in the prose. A
				// client that has to regex the message to find out what it
				// lacks cannot act on it.
				forbidden(w, CodeScopeMissing, refusedPermissionMessage(r, required),
					map[string]any{"required_scope": policy.Domain, "required_permission": required.String()})
				return
			}
		}

		// The token requirement is checked whether or not a permission was,
		// because the two are independent: creating a namespace needs a
		// logged-in wallet and no permission at all, and a wallet with no
		// namespace holds none anywhere. Returning early when a route required
		// nothing made a declared token requirement do nothing, silently.
		if !g.hasRequiredToken(r, policy, perms) {
			g.logger.ComponentWarn("gateway", "request rejected: user JWT required",
				zap.String("path", r.URL.Path),
				zap.String("required", policy.Domain),
			)
			unauthorized(w, CodeAuthUserJWTRequired,
				"user authentication required (JWT): "+r.URL.Path+" requires a logged-in user; an API key alone is not sufficient",
				map[string]any{"required_scope": policy.Domain})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasRequiredToken reports whether the caller presented the kind of token the
// route asks for.
//
// An admin caller is exempt: the requirement exists to make a leaked data-plane
// key inert, and an admin credential is not one.
func (g *Gateway) hasRequiredToken(r *http.Request, policy routepolicy.Policy, perms auth.PermissionSet) bool {
	if policy.Token == routepolicy.AnyCredential || perms.IsAdmin() {
		return true
	}
	switch policy.Token {
	case routepolicy.AnyToken:
		return hasAnyJWT(r)
	case routepolicy.PrincipalToken:
		return hasWalletJWT(r) || hasWorkloadJWT(r)
	default:
		return hasWalletJWT(r)
	}
}

// hasWorkloadJWT reports whether the request carries a deployed app's own
// workload token (subject app:<namespace>/<name>). It is minted by the gateway
// for the app at start and renewed only by its holder; a key's exchange never
// carries that subject.
func hasWorkloadJWT(r *http.Request) bool {
	if claims, ok := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims); ok && claims != nil {
		return auth.IsWorkloadSubject(claims.Sub)
	}
	return false
}

// markGrant returns a shallow copy of the request whose context carries the
// grant the caller holds in the namespace, as resolved by the authorization
// middleware. callerScopes turns it into the scope set a wallet JWT gets.
//
// This used to be a bare `true`: "a SIWE wallet owner was verified". A boolean
// has one thing to say, so everybody it was set for became an admin. That is
// what the roles exist to end.
func markGrant(r *http.Request, grant *auth.Grant) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.Grant, grant))
}

// noGrantPublishRemedy names what a signed-in user with no grant in the
// namespace can do about being refused pub/sub write (bugboard #733).
const noGrantPublishRemedy = "this wallet holds no grant in this namespace, and a signed-in user without " +
	"one may subscribe but not publish: ask the namespace owner for a grant with pubsub write, " +
	"or publish from a function"

// refusedPermissionMessage is the sentence a refused caller reads. A wallet
// with no grant is told what to do about publishing, where every other caller
// is told which permission it lacks.
func refusedPermissionMessage(r *http.Request, required auth.Resource) string {
	if required.Domain == auth.SelectorPubsub && required.Action == auth.ActionWrite && isGrantlessWallet(r) {
		return "insufficient permission: " + noGrantPublishRemedy + " (required " + required.String() + ", " + r.URL.Path + ")"
	}
	return "insufficient permission: this credential does not hold " + required.String() +
		", required for " + r.URL.Path
}

// isGrantlessWallet reports whether the caller is a signed-in wallet that holds
// no grant in a tenant namespace: the principal callerPermissions gives
// auth.NoGrantPermissions.
func isGrantlessWallet(r *http.Request) bool {
	ctx := r.Context()
	if grant, _ := ctx.Value(ctxKeyGrant).(*auth.Grant); grant != nil {
		return false
	}
	ns, _ := ctx.Value(CtxKeyNamespaceOverride).(string)
	if auth.IsLobbyNamespace(ns) || strings.TrimSpace(ns) == "" {
		return false
	}
	return hasWalletJWT(r)
}
