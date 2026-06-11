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

// getWalletFromRequest extracts wallet address from JWT.
func (h *ServerlessHandlers) getWalletFromRequest(r *http.Request) string {
	// Import strings package functions inline to avoid circular dependencies
	trimSpace := func(s string) string {
		start := 0
		end := len(s)
		for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
			start++
		}
		for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
			end--
		}
		return s[start:end]
	}

	hasPrefix := func(s, prefix string) bool {
		return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
	}

	contains := func(s, substr string) bool {
		return len(s) >= len(substr) && func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()
	}

	toLower := func(s string) string {
		result := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c >= 'A' && c <= 'Z' {
				result[i] = c + 32
			} else {
				result[i] = c
			}
		}
		return string(result)
	}

	// 1. Try X-Wallet header (legacy/direct bypass)
	if wallet := r.Header.Get("X-Wallet"); wallet != "" {
		return wallet
	}

	// 2. Try JWT claims from context
	if v := r.Context().Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			subj := trimSpace(claims.Sub)
			// Ensure it's not an API key (standard Orama logic)
			if !hasPrefix(toLower(subj), "ak_") && !contains(subj, ":") {
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
