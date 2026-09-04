// Package operator provides HTTP handlers for node operator management.
//
// Operators authenticate via wallet JWT (same auth flow as namespaces).
// Each operator's nodes are tracked by their wallet address in the
// dns_nodes and wireguard_peers tables.
package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Handler provides HTTP handlers for operator node management.
type Handler struct {
	logger       *zap.Logger
	rqliteClient rqlite.Client

	// audit records what an operator did. An operator action is the highest
	// authority anything on this cluster has, and none of it was recorded.
	audit *auth.AuditLog
}

// SetAuditLog wires the record. Set by the gateway after construction; nil
// leaves the actions unrecorded, which is the test case.
func (h *Handler) SetAuditLog(a *auth.AuditLog) { h.audit = a }

// NewHandler creates an operator handler.
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client) *Handler {
	return &Handler{
		logger:       logger,
		rqliteClient: rqliteClient,
	}
}

// walletFromRequest extracts the operator's wallet address from the request.
// Supports both JWT auth (wallet in Sub claim) and API key auth (wallet looked
// up from wallet_api_keys table).
func (h *Handler) walletFromRequest(r *http.Request) string {
	return WalletFromRequest(r, h.rqliteClient)
}

// WalletFromRequest resolves the caller's wallet: the JWT subject when it is a
// wallet, and otherwise the wallet that owns the API key's namespace.
//
// Exported for the gateway, which asks the same question before letting a
// caller read the cluster registry.
func WalletFromRequest(r *http.Request, db rqlite.Client) string {
	h := &Handler{rqliteClient: db}
	return h.resolveWallet(r)
}

func (h *Handler) resolveWallet(r *http.Request) string {
	// 1. Try JWT claims first (wallet JWT auth sets Sub = "0x...")
	if claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims); ok && claims != nil {
		sub := strings.TrimSpace(claims.Sub)
		if strings.HasPrefix(strings.ToLower(sub), "0x") {
			return sub
		}
		// JWT with API key subject
		if strings.HasPrefix(strings.ToLower(sub), "ak_") {
			return h.resolveWalletFromAPIKey(r.Context(), sub)
		}
	}

	// 2. Try API key from context (X-API-Key header, no JWT)
	if apiKey, ok := r.Context().Value(ctxkeys.APIKey).(string); ok && apiKey != "" {
		return h.resolveWalletFromAPIKey(r.Context(), apiKey)
	}

	return ""
}

// resolveWalletFromAPIKey looks up the wallet address linked to an API key.
// It queries namespace_ownership for a wallet-type owner of the namespace.
func (h *Handler) resolveWalletFromAPIKey(ctx context.Context, apiKeySub string) string {
	if h.rqliteClient == nil {
		return ""
	}
	ns := extractNamespace(apiKeySub)
	if ns == "" {
		return ""
	}
	var rows []struct {
		OwnerID string `db:"owner_id"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		`SELECT no.owner_id FROM namespace_ownership no
		 JOIN namespaces n ON no.namespace_id = n.id
		 WHERE n.name = ? AND no.owner_type = 'wallet'
		 LIMIT 1`,
		ns); err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].OwnerID
}

// extractNamespace extracts the namespace from an API key subject like "ak_xxx:namespace".
func extractNamespace(apiKeySub string) string {
	parts := strings.SplitN(apiKeySub, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return apiKeySub
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
