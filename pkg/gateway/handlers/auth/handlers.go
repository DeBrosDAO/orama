// Package auth provides HTTP handlers for wallet-based authentication,
// JWT token management, and API key operations. It supports challenge/response
// flows using cryptographic signatures for Ethereum and other blockchain wallets.
package auth

import (
	"context"
	"database/sql"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// contextKey is the type for context keys
type contextKey string

// Context keys for request-scoped auth metadata
// These are exported so they can be used by the gateway middleware
const (
	CtxKeyAPIKey            contextKey = "api_key"
	CtxKeyJWT               contextKey = "jwt_claims"
	CtxKeyNamespaceOverride contextKey = "namespace_override"
)

// NetworkClient defines the minimal network client interface needed by auth handlers
type NetworkClient interface {
	Database() DatabaseClient
}

// DatabaseClient defines the database query interface
type DatabaseClient interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*QueryResult, error)
}

// QueryResult represents a database query result
type QueryResult struct {
	Count int           `json:"count"`
	Rows  []interface{} `json:"rows"`
}

// Handlers holds dependencies for authentication HTTP handlers
type Handlers struct {
	logger         *logging.ColoredLogger
	authService    *authsvc.Service
	netClient      NetworkClient
	defaultNS      string
	internalAuthFn func(context.Context) context.Context
}

// NewHandlers creates a new authentication handlers instance
func NewHandlers(
	logger *logging.ColoredLogger,
	authService *authsvc.Service,
	netClient NetworkClient,
	defaultNamespace string,
	internalAuthFn func(context.Context) context.Context,
) *Handlers {
	return &Handlers{
		logger:         logger,
		authService:    authService,
		netClient:      netClient,
		defaultNS:      defaultNamespace,
		internalAuthFn: internalAuthFn,
	}
}

// markNonceUsed marks a nonce as used in the database
func (h *Handlers) markNonceUsed(ctx context.Context, namespaceID interface{}, wallet, nonce string) {
	if h.netClient == nil {
		return
	}
	db := h.netClient.Database()
	internalCtx := h.internalAuthFn(ctx)
	_, _ = db.Query(internalCtx, "UPDATE nonces SET used_at = datetime('now') WHERE namespace_id = ? AND wallet = ? AND nonce = ?", namespaceID, wallet, nonce)
}

// resolveNamespace resolves namespace ID for nonce marking
func (h *Handlers) resolveNamespace(ctx context.Context, namespace string) (interface{}, error) {
	if h.authService == nil {
		return nil, sql.ErrNoRows
	}
	return h.authService.ResolveNamespaceID(ctx, namespace)
}
