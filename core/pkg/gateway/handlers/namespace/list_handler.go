package namespace

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
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

	// Get current namespace from auth context
	ns := ""
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			ns = s
		}
	}
	if ns == "" {
		writeListResponse(w, http.StatusUnauthorized, map[string]interface{}{"error": "not authenticated"})
		return
	}

	// Look up the owner wallet from the current namespace
	type ownerRow struct {
		OwnerID string `db:"owner_id"`
	}
	var owners []ownerRow
	if err := h.ormClient.Query(r.Context(), &owners,
		`SELECT p.identifier AS owner_id
		   FROM grants g JOIN principals p ON p.id = g.principal_id
		  WHERE g.namespace_id = (SELECT id FROM namespaces WHERE name = ? LIMIT 1)
		    AND g.role = 'owner' AND g.revoked_at IS NULL
		  LIMIT 1`, ns); err != nil || len(owners) == 0 {
		h.logger.Warn("Failed to resolve namespace owner",
			zap.String("namespace", ns), zap.Error(err))
		writeListResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": "failed to resolve namespace owner"})
		return
	}

	ownerID := owners[0].OwnerID

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
		 WHERE p.identifier = ?
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
