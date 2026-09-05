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

// callerScopes resolves the effective grant set for the authenticated request.
//
//   - API-key-exchanged JWT (from /v1/auth/token): carries the key's exact
//     scopes in a custom claim — this is what closes the exchange-then-escalate
//     hole (a runtime key exchanged for a JWT keeps its narrow scope).
//   - SIWE wallet JWT: a confirmed namespace owner gets admin; any other
//     authenticated user gets the data-plane set (never admin).
//   - Raw API key: the scopes stashed at lookup time.
func (g *Gateway) callerScopes(r *http.Request) auth.ScopeSet {
	ctx := r.Context()

	if v := ctx.Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			// Only an API-key-exchanged JWT (ak_ subject) may carry an
			// authoritative scopes claim. A SIWE wallet JWT must NOT be trusted
			// here even if a custom["scopes"] is present, because a tenant
			// claims-provider could otherwise inject "admin" for every end-user
			// (defense-in-depth alongside reserving "scopes" in the provider).
			if isAPIKeySubject(claims.Sub) && claims.Custom != nil {
				if raw := strings.TrimSpace(claims.Custom["scopes"]); raw != "" {
					return auth.ParseScopes(raw)
				}
			}
			// The grant the authorization middleware resolved for this
			// namespace decides what a wallet JWT may reach. An owner or an
			// admin gets the control plane; a runtime member gets the data
			// plane; a reader gets nothing beyond the routes that ask for no
			// grant at all.
			if grant, _ := ctx.Value(ctxKeyGrant).(*auth.Grant); grant != nil {
				return grant.Scopes()
			}
			// No grant was resolved, which is every route the ownership gate
			// does not cover. A logged-in user gets the data plane there, as
			// they always have.
			return auth.DataPlaneScopes()
		}
	}

	if v := ctx.Value(ctxKeyScopes); v != nil {
		if s, ok := v.(auth.ScopeSet); ok {
			return s
		}
	}

	// No identity resolved (should not happen for a non-public path, which the
	// auth middleware already gated) — deny by returning an empty set.
	return auth.ScopeSet{}
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
		required := policy.Scope
		if required == "" {
			next.ServeHTTP(w, r)
			return
		}
		scopes := g.callerScopes(r)
		if !scopes.Has(required) {
			g.logger.ComponentWarn("gateway", "request rejected: insufficient scope",
				zap.String("path", r.URL.Path),
				zap.String("required_scope", required),
			)
			// The grant goes in a field, not only in the prose. A client that
			// has to regex the message to find out what it lacks cannot act on
			// it; @debros/orama turns this into a ScopeError naming the grant.
			forbidden(w, CodeScopeMissing,
				"insufficient scope: this credential lacks the '"+required+"' grant required for "+r.URL.Path,
				map[string]any{"required_scope": required})
			return
		}
		if !g.hasRequiredToken(r, policy, scopes) {
			g.logger.ComponentWarn("gateway", "request rejected: user JWT required",
				zap.String("path", r.URL.Path),
				zap.String("required_scope", required),
			)
			unauthorized(w, CodeAuthUserJWTRequired,
				"user authentication required (JWT): the '"+required+"' operation requires a logged-in user; an API key alone is not sufficient",
				map[string]any{"required_scope": required})
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
func (g *Gateway) hasRequiredToken(r *http.Request, policy routepolicy.Policy, scopes auth.ScopeSet) bool {
	if policy.Token == routepolicy.AnyCredential || scopes.IsAdmin() {
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
