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

	// The grant the authorization middleware resolved for this namespace, for
	// whichever principal the credential named. It is the answer whenever the
	// route resolves one.
	if grant, _ := ctx.Value(ctxKeyGrant).(*auth.Grant); grant != nil {
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
		// A logged-in user with no grant in this namespace gets the data
		// plane, as they always have.
		return auth.DataPlanePermissions()
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
				forbidden(w, CodeScopeMissing,
					"insufficient permission: this credential does not hold "+required.String()+
						", required for "+r.URL.Path,
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
	if policy.Token == routepolicy.AnyToken {
		return hasAnyJWT(r)
	}
	return hasWalletJWT(r)
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
