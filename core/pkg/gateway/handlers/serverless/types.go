package serverless

import (
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	"go.uber.org/zap"
)

// JWTVerifier is the subset of *auth.Service the serverless handlers
// need for mid-session token refresh on persistent WS (bugboard #321).
// Kept as an interface so tests can pass a fake without standing up
// the full auth service.
type JWTVerifier interface {
	ParseAndVerifyJWT(token string) (*auth.JWTClaims, error)
}

// ServerlessHandlers contains handlers for serverless function endpoints.
// It's a separate struct to keep the Gateway struct clean.
type ServerlessHandlers struct {
	invoker        *serverless.Invoker
	engine         *serverless.Engine // for persistent WS instantiation
	registry       serverless.FunctionRegistry
	wsManager      *serverless.WSManager
	triggerStore   *triggers.PubSubTriggerStore
	cronStore      *triggers.CronTriggerStore // optional; nil = cron triggers unavailable
	dispatcher     *triggers.PubSubDispatcher
	persistentMgr  *persistent.Manager // optional; when nil persistent WS rejects 503
	wsBridge       *wsbridge.Bridge    // optional; nil = no client→ns registration
	secretsManager serverless.SecretsManager
	jwtVerifier    JWTVerifier // optional; when nil, mid-session auth.refresh is disabled
	logger         *zap.Logger
}

// NewServerlessHandlers creates a new ServerlessHandlers instance.
//
// engine, persistentMgr, and wsBridge may be nil — persistent-WS
// functions then return 503 on upgrade, and bridged WS clients can't
// be tracked (the host call returns "unknown client_id"). All other
// endpoints continue to work via the invoker.
func NewServerlessHandlers(
	invoker *serverless.Invoker,
	engine *serverless.Engine,
	registry serverless.FunctionRegistry,
	wsManager *serverless.WSManager,
	triggerStore *triggers.PubSubTriggerStore,
	cronStore *triggers.CronTriggerStore,
	dispatcher *triggers.PubSubDispatcher,
	persistentMgr *persistent.Manager,
	wsBridge *wsbridge.Bridge,
	secretsManager serverless.SecretsManager,
	logger *zap.Logger,
) *ServerlessHandlers {
	return &ServerlessHandlers{
		invoker:        invoker,
		engine:         engine,
		registry:       registry,
		wsManager:      wsManager,
		triggerStore:   triggerStore,
		cronStore:      cronStore,
		dispatcher:     dispatcher,
		persistentMgr:  persistentMgr,
		wsBridge:       wsBridge,
		secretsManager: secretsManager,
		logger:         logger,
	}
}

// SetJWTVerifier wires the JWT verifier used for mid-session auth
// refresh on persistent WS (bugboard #321 control frame). Optional —
// when not set, the persistent WS handler rejects auth.refresh frames
// with a "not supported on this gateway" ack and the client falls back
// to the legacy close+reconnect path.
//
// Done as a setter rather than a constructor arg to avoid breaking
// existing call sites that don't yet have an auth service handy. Set
// once at gateway init, after construction.
func (h *ServerlessHandlers) SetJWTVerifier(v JWTVerifier) {
	h.jwtVerifier = v
}

// HealthStatus returns the health status of the serverless engine.
func (h *ServerlessHandlers) HealthStatus() map[string]interface{} {
	stats := h.wsManager.GetStats()
	return map[string]interface{}{
		"status":      "ok",
		"connections": stats.ConnectionCount,
		"topics":      stats.TopicCount,
	}
}

// getNamespaceFromRequest extracts namespace from JWT or query param.
func (h *ServerlessHandlers) getNamespaceFromRequest(r *http.Request) string {
	// Try context first (set by auth middleware) - most secure
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if ns, ok := v.(string); ok && ns != "" {
			return ns
		}
	}

	// Try query param as fallback (e.g. for public access or admin)
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		return ns
	}

	// Try header as fallback
	if ns := r.Header.Get("X-Namespace"); ns != "" {
		return ns
	}

	return "default"
}

// getCallerClaimsFromRequest returns the JWT custom claims for the caller,
// or nil if the request was not JWT-authenticated. The map is safe to share
// (read-only on the engine side); we copy to avoid retaining the JWT struct.
func (h *ServerlessHandlers) getCallerClaimsFromRequest(r *http.Request) map[string]string {
	v := r.Context().Value(ctxkeys.JWT)
	if v == nil {
		return nil
	}
	claims, ok := v.(*auth.JWTClaims)
	if !ok || claims == nil || len(claims.Custom) == 0 {
		return nil
	}
	out := make(map[string]string, len(claims.Custom))
	for k, val := range claims.Custom {
		out[k] = val
	}
	return out
}

// getJWTSubjectFromRequest returns the Bearer JWT's `sub` claim if present,
// independent of the API-key-vs-JWT precedence used for general wallet
// resolution. Returns "" when the request was not JWT-authenticated.
//
// This is the source of truth for `oh.GetCallerJWTSubject()` inside WASM
// — bug #215. Functions that must bind on the JWT-signed identity (e.g.
// signup paths verifying the registering wallet matches the auth-challenge
// signer) read this instead of GetCallerWallet, which returns the namespace
// pseudo-identifier when the API key is the resolved auth.
func (h *ServerlessHandlers) getJWTSubjectFromRequest(r *http.Request) string {
	v := r.Context().Value(ctxkeys.JWT)
	if v == nil {
		return ""
	}
	claims, ok := v.(*auth.JWTClaims)
	if !ok || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.Sub)
}

// getJWTExpiryFromRequest returns the Bearer JWT's `exp` claim (unix seconds)
// if the request was JWT-authenticated, or 0 otherwise (e.g. API-key auth, or
// a token without an exp). Persistent WS connections capture this at upgrade
// to enforce mid-session expiry — a long-lived socket must stop serving RPCs
// once its authorizing token expires, unless refreshed via the #321
// auth.refresh control frame. Bugboard #868.
func (h *ServerlessHandlers) getJWTExpiryFromRequest(r *http.Request) int64 {
	v := r.Context().Value(ctxkeys.JWT)
	if v == nil {
		return 0
	}
	claims, ok := v.(*auth.JWTClaims)
	if !ok || claims == nil {
		return 0
	}
	return claims.Exp
}

// getCallerHasInvokeFromRequest reports whether the caller may invoke a
// private function (bugboard #259). HTTP /invoke is a public path, so
// scopeMiddleware never runs; this is the grant check.
//
// True for: admin, an API key (or exchanged JWT) that holds ScopeInvoke,
// or a SIWE wallet JWT (non-ak_ subject). Storage-only API keys are false.
func (h *ServerlessHandlers) getCallerHasInvokeFromRequest(r *http.Request) bool {
	if h.getCallerIsAdminFromRequest(r) {
		return true
	}
	ctx := r.Context()
	if v := ctx.Value(ctxkeys.Scopes); v != nil {
		if s, ok := v.(auth.ScopeSet); ok && s.Has(auth.ScopeInvoke) {
			return true
		}
	}
	if v := ctx.Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			sub := strings.ToLower(strings.TrimSpace(claims.Sub))
			if auth.IsAPIKeySubject(sub) {
				if claims.Custom != nil {
					if raw := strings.TrimSpace(claims.Custom["scopes"]); raw != "" && auth.ParseScopes(raw).Has(auth.ScopeInvoke) {
						return true
					}
				}
				return false
			}
			if sub != "" {
				return true
			}
		}
	}
	return false
}

// getCallerIsAdminFromRequest reports whether the request holds the admin
// grant used to invoke `internal: true` functions (bugboard #152).
func (h *ServerlessHandlers) getCallerIsAdminFromRequest(r *http.Request) bool {
	ctx := r.Context()
	if v := ctx.Value(ctxkeys.Scopes); v != nil {
		if s, ok := v.(auth.ScopeSet); ok && s.IsAdmin() {
			return true
		}
	}
	// An API-key-exchanged JWT (from /v1/auth/token) carries the key's scopes in
	// a custom claim — the JWT-bearer auth path sets ctxkeys.JWT but NOT
	// ctxkeys.Scopes — so an admin key used via exchange would otherwise be
	// wrongly denied. Mirror Gateway.callerScopes: trust custom["scopes"] ONLY
	// for an ak_ subject; a SIWE wallet JWT must never self-assert admin here.
	if v := ctx.Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			sub := strings.ToLower(strings.TrimSpace(claims.Sub))
			if auth.IsAPIKeySubject(sub) && claims.Custom != nil {
				if raw := strings.TrimSpace(claims.Custom["scopes"]); raw != "" && auth.ParseScopes(raw).IsAdmin() {
					return true
				}
			}
		}
	}
	// The grant the authorization middleware resolved for this namespace. It
	// used to be a boolean meaning "owner confirmed", so every member of a
	// namespace was an admin here; a runtime or reader member is not.
	if grant, _ := ctx.Value(ctxkeys.Grant).(*auth.Grant); grant != nil {
		return grant.Scopes().IsAdmin()
	}
	return false
}

// getWalletFromRequest resolves the caller identity for an invoke from
// VERIFIED sources only: the JWT subject (a wallet), else the API-key-derived
// namespace. It deliberately does NOT trust any client-supplied identity header
// (bugboard #152): the invoke paths are public, so an unauthenticated caller
// could otherwise set a header and impersonate any wallet — including the
// namespace owner — defeating in-function admin gates.
func (h *ServerlessHandlers) getWalletFromRequest(r *http.Request) string {
	// Identity comes only from a VERIFIED JWT subject, else the API-key-derived
	// namespace. A client-supplied X-Wallet header is NOT trusted (bugboard
	// #152) — it let an unauthenticated caller impersonate any wallet on the
	// public invoke paths.
	if v := r.Context().Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			subj := strings.TrimSpace(claims.Sub)
			// Ensure it's not an API key (standard Orama logic)
			if auth.IsWalletSubject(subj) {
				return subj
			}
		}
	}

	// 3. Fallback to API key identity (namespace)
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if ns, ok := v.(string); ok && ns != "" {
			return ns
		}
	}

	return ""
}
