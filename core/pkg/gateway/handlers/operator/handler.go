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
	// 1. Try JWT claims first (a wallet JWT's subject is the address itself)
	if claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims); ok && claims != nil {
		sub := strings.TrimSpace(claims.Sub)
		if auth.IsWalletSubject(sub) {
			return sub
		}
		// A key-exchanged JWT. Its namespace is a claim on the token, which is
		// where the namespace lives now that it is not in the key string.
		if ns := strings.TrimSpace(claims.Namespace); ns != "" {
			return h.resolveWalletFromAPIKey(r.Context(), ns)
		}
	}

	// 2. An API key with no JWT. The namespace the middleware resolved for it
	// is in the context; the key itself does not carry one.
	if ns, ok := r.Context().Value(ctxkeys.NamespaceOverride).(string); ok && strings.TrimSpace(ns) != "" {
		return h.resolveWalletFromAPIKey(r.Context(), ns)
	}

	return ""
}

// resolveWalletFromAPIKey looks up the wallet that owns the namespace a key
// belongs to.
//
// The namespace comes from the token's own claim. It used to be parsed out of
// the key string — `ak_<random>:<namespace>` — which is a format that no longer
// exists, deliberately: a key pasted into an issue or a log line published
// which tenant it belonged to.
func (h *Handler) resolveWalletFromAPIKey(ctx context.Context, ns string) string {
	if h.rqliteClient == nil {
		return ""
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return ""
	}
	var rows []struct {
		OwnerID string `db:"owner_id"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		`SELECT p.identifier AS owner_id
		   FROM grants g
		   JOIN principals p ON p.id = g.principal_id
		   JOIN namespaces n ON g.namespace_id = n.id
		  WHERE n.name = ? AND g.role = 'owner' AND g.revoked_at IS NULL AND p.type = 'wallet'
		  LIMIT 1`,
		ns); err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].OwnerID
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
