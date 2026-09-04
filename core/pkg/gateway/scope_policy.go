package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

// isKeyMgmtPath reports whether a path is a scoped-key management endpoint
// (bugboard #148). These are served by the main gateway (authoritative api_keys
// live in the main cluster RQLite), never proxied to a namespace gateway.
func isKeyMgmtPath(p string) bool {
	return p == "/v1/namespace/keys" || strings.HasPrefix(p, "/v1/namespace/keys/")
}

// requiredScope returns the API-key grant required to reach (method, path), or
// "" when a valid credential of any scope suffices. It is the single source of
// truth for the data-plane vs control-plane split (bugboard #148).
//
// Order matters: several prefixes are MIXED (part data-plane, part admin), so
// the more specific data-plane paths (e.g. /v1/push/devices) must be matched
// before the coarse admin bucket for the same prefix (/v1/push/config).
//
// Public paths (health, auth handshake, function invoke) are exempted by the
// gate before this is called; the isPublicPath short-circuit here is a belt-
// and-braces guard so a stray classification can never over-restrict them.
func requiredScope(method, path string) string {
	if isPublicPath(path) {
		return ""
	}

	// --- Functions: /invoke is public (handled above); /ws is an invoke
	// transport (data-plane); everything else is control-plane. ---
	if path == "/v1/functions" || strings.HasPrefix(path, "/v1/functions/") {
		if strings.HasSuffix(path, "/ws") {
			return auth.ScopeInvoke
		}
		return auth.ScopeAdmin
	}

	// --- Storage (data-plane) ---
	if strings.HasPrefix(path, "/v1/storage/") {
		return auth.ScopeStorage
	}

	// --- Push: devices are data-plane; config/send are admin ---
	if path == "/v1/push/devices" || strings.HasPrefix(path, "/v1/push/devices/") {
		return auth.ScopePush
	}
	if strings.HasPrefix(path, "/v1/push/") {
		return auth.ScopeAdmin
	}
	if path == "/v1/namespace/push-credentials" || strings.HasPrefix(path, "/v1/namespace/push-credentials/") {
		return auth.ScopeAdmin
	}

	// --- WebRTC data-plane (signal/rooms/turn credentials) ---
	if strings.HasPrefix(path, "/v1/webrtc/") {
		return auth.ScopeWebRTC
	}
	// Namespace WebRTC management: enable/disable/stealth are admin; status is a
	// read that any valid credential may poll.
	if strings.HasPrefix(path, "/v1/namespace/webrtc/") {
		if strings.HasSuffix(path, "/status") {
			return ""
		}
		return auth.ScopeAdmin
	}

	// --- Anon proxy (data-plane) ---
	if strings.HasPrefix(path, "/v1/proxy/") {
		return auth.ScopeProxy
	}

	// --- Pub/sub REST (data-plane grant; not in anchat profiles) ---
	if strings.HasPrefix(path, "/v1/pubsub/") {
		return auth.ScopePubsub
	}

	// --- Olric cache REST (data-plane grant; dead in client) ---
	if strings.HasPrefix(path, "/v1/cache/") {
		return auth.ScopeCache
	}

	// Cluster node command/logs/leave/status: operator only (bugboard #54/#55).
	// Enroll authenticates via invite token inside the handler, not an API-key
	// admin grant (the CLI sends Bearer <invite>).
	if strings.HasPrefix(path, "/v1/node/") {
		if path == "/v1/node/enroll" {
			return ""
		}
		return auth.ScopeAdmin
	}
	// Topology mutation: operator only (bugboard #56). Status/peers stay public.
	if path == "/v1/network/connect" || path == "/v1/network/disconnect" {
		return auth.ScopeAdmin
	}

	// --- Control-plane (admin only) ---
	if path == "/rqlite" || path == "/v1/rqlite" || strings.HasPrefix(path, "/v1/rqlite/") {
		return auth.ScopeAdmin
	}
	if strings.HasPrefix(path, "/v1/deployments/") {
		return auth.ScopeAdmin
	}
	if strings.HasPrefix(path, "/v1/db/sqlite/") {
		return auth.ScopeAdmin
	}
	if strings.HasPrefix(path, "/v1/serverless/") {
		return auth.ScopeAdmin
	}
	if path == "/v1/namespace/rate-limit" {
		return auth.ScopeAdmin
	}
	if path == "/v1/namespace/keys" || strings.HasPrefix(path, "/v1/namespace/keys/") {
		return auth.ScopeAdmin
	}
	if path == "/v1/namespace/delete" || path == "/v1/namespace/list" {
		return auth.ScopeAdmin
	}

	// Default: a valid credential is enough; no elevated grant required.
	return ""
}

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
			if confirmed, _ := ctx.Value(ctxKeyOwnerConfirmed).(bool); confirmed {
				return auth.ScopeSet{auth.ScopeAdmin: {}}
			}
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

// requiresUserJWT reports whether a data-plane grant additionally requires a
// genuine per-user (wallet) JWT — the layer-1 hardening that makes an extracted
// runtime key worthless without a logged-in user. Admin callers are exempt (see
// scopeMiddleware); push already enforces this in its own handler.
func requiresUserJWT(grant string) bool {
	switch grant {
	case auth.ScopeStorage, auth.ScopeWebRTC, auth.ScopeProxy:
		return true
	}
	return false
}

// isAPIKeySubject reports whether a JWT subject is an API key (ak_<rand>:<ns>),
// as minted by the API-key→JWT exchange, rather than a SIWE wallet address.
// This is the single signal used to (a) decide a JWT is not a genuine user
// (hasWalletJWT) and (b) decide whether to trust an embedded scopes claim
// (callerScopes). Wallet subjects are plain addresses; only exchanged keys
// carry the ak_ prefix.
func isAPIKeySubject(sub string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sub)), "ak_")
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
// wallet JWT OR an API-key-exchanged one (ak_ subject). Used ONLY by the
// storage-unpin exception below (bugboard #151): a serverless cron/job has no
// logged-in user, so it proves key-possession by exchanging its storage-scoped
// runtime key for a JWT. A bare API key (no JWT) yields false here, so the
// "an extracted key is inert without a JWT" property of layer-1 still holds.
func hasAnyJWT(r *http.Request) bool {
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			return strings.TrimSpace(claims.Sub) != ""
		}
	}
	return false
}

// isStorageUnpinPath reports whether (method, path) is the storage RECLAIM
// endpoint — DELETE /v1/storage/unpin/:cid. This is the ONLY storage op whose
// layer-1 user-JWT requirement is relaxed (bugboard #151): unpin is
// namespace-ownership-checked in its handler and can only DROP the namespace's
// own pins — it never reads or uploads. So a storage-scoped exchanged JWT is
// sufficient for the userless server-side reclaim (cron / avatar-GC /
// free-up-space). upload / get / pin keep the strict wallet-JWT requirement.
func isStorageUnpinPath(method, path string) bool {
	return method == http.MethodDelete && strings.HasPrefix(path, "/v1/storage/unpin/")
}

// scopeMiddleware enforces the API-key scope model. It runs after the
// authorization (ownership) middleware, so ownership has already been verified;
// this layer additionally (a) rejects a credential whose grant set does not
// cover the operation (403 INSUFFICIENT_SCOPE, bugboard #148), and (b) requires
// a genuine user JWT for the storage/webrtc/proxy data-plane grants unless the
// caller is admin (the layer-1 hardening — an extracted runtime key is useless
// without a logged-in user).
func (g *Gateway) scopeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		required := requiredScope(r.Method, r.URL.Path)
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
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":          "insufficient scope: this credential lacks the '" + required + "' grant required for " + r.URL.Path,
				"code":           "INSUFFICIENT_SCOPE",
				"required_scope": required,
			})
			return
		}
		if requiresUserJWT(required) && !scopes.IsAdmin() && !hasWalletJWT(r) {
			// Exception (bugboard #151): server-side storage RECLAIM. A
			// serverless cron/job (prune-attachments, avatar-GC, free-up-space)
			// has no logged-in user and authenticates by exchanging its
			// storage-scoped runtime key for a JWT. Unpin is namespace-isolated
			// (handler verifies CID ownership) and reclaim-only, so a scoped
			// exchanged JWT suffices — but a bare API key (no JWT) still fails,
			// and the scope check above already proved the caller holds storage.
			if isStorageUnpinPath(r.Method, r.URL.Path) && hasAnyJWT(r) {
				next.ServeHTTP(w, r)
				return
			}
			g.logger.ComponentWarn("gateway", "request rejected: user JWT required",
				zap.String("path", r.URL.Path),
				zap.String("required_scope", required),
			)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":          "user authentication required (JWT): the '" + required + "' operation requires a logged-in user; an API key alone is not sufficient",
				"code":           "USER_JWT_REQUIRED",
				"required_scope": required,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// markOwnerConfirmed returns a shallow copy of the request whose context records
// that a SIWE wallet owner was verified for the namespace (used by callerScopes
// to grant the owner admin via a wallet JWT).
func markOwnerConfirmed(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.OwnerConfirmed, true))
}
