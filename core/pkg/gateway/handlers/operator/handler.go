// Package operator provides HTTP handlers for node operator management.
//
// Operators authenticate via wallet JWT (same auth flow as namespaces).
// Each operator's nodes are tracked by their wallet address in the
// dns_nodes and wireguard_peers tables.
package operator

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Handler provides HTTP handlers for operator node management.
type Handler struct {
	logger       *zap.Logger
	rqliteClient rqlite.Client
}

// NewHandler creates an operator handler.
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client) *Handler {
	return &Handler{
		logger:       logger,
		rqliteClient: rqliteClient,
	}
}

// walletFromRequest extracts the operator's wallet address from the JWT
// stored in the request context by the auth middleware.
func walletFromRequest(r *http.Request) string {
	claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims)
	if !ok || claims == nil {
		return ""
	}
	return claims.Sub
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
