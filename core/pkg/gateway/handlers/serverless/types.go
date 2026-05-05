package serverless

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	"go.uber.org/zap"
)

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
