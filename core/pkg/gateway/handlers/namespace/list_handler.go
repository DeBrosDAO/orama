package namespace

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ListHandler handles namespace list requests
type ListHandler struct {
	ormClient rqlite.Client
	logger    *zap.Logger
}

// NewListHandler creates a new namespace list handler
func NewListHandler(orm rqlite.Client, logger *zap.Logger) *ListHandler {
	return &ListHandler{
		ormClient: orm,
		logger:    logger.With(zap.String("component", "namespace-list-handler")),
	}
}

// ServeHTTP handles GET /v1/namespace/list
func (h *ListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeListResponse(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
		return
	}

	// The calling wallet's own namespaces. This used to list those of the
	// current namespace's owner, so any admin member saw the owner's whole
	// portfolio, and a session in the lobby (which nobody owns) listed nothing.
	wallet := walletFromContext(r)
	if wallet == "" {
		writeListResponse(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "listing your namespaces requires a signed-in wallet",
		})
		return
	}
	ownerID := auth.NormalizeWallet(wallet)

	// Query all namespaces owned by this wallet
	type nsRow struct {
		Name          string `db:"name"           json:"name"`
		CreatedAt     string `db:"created_at"     json:"created_at"`
		ClusterStatus string `db:"cluster_status" json:"cluster_status"`
	}
	var namespaces []nsRow
	if err := h.ormClient.Query(r.Context(), &namespaces,
		`SELECT n.name, n.created_at, COALESCE(nc.status, 'none') as cluster_status
		 FROM namespaces n
		 JOIN grants g ON g.namespace_id = n.id AND g.role = 'owner' AND g.revoked_at IS NULL
		 JOIN principals p ON p.id = g.principal_id
		 LEFT JOIN namespace_clusters nc ON nc.namespace_id = n.id
		 WHERE p.type = 'wallet' AND p.identifier = ?
		 ORDER BY n.created_at DESC`, ownerID); err != nil {
		h.logger.Error("Failed to list namespaces", zap.Error(err))
		writeListResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to list namespaces"})
		return
	}

	writeListResponse(w, http.StatusOK, map[string]interface{}{
		"namespaces": namespaces,
		"count":      len(namespaces),
	})
}

func writeListResponse(w http.ResponseWriter, status int, resp map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
