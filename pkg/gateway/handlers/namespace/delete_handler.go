package namespace

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// NamespaceDeprovisioner is the interface for deprovisioning namespace clusters
type NamespaceDeprovisioner interface {
	DeprovisionCluster(ctx context.Context, namespaceID int64) error
}

// DeleteHandler handles namespace deletion requests
type DeleteHandler struct {
	deprovisioner NamespaceDeprovisioner
	ormClient     rqlite.Client
	logger        *zap.Logger
}

// NewDeleteHandler creates a new delete handler
func NewDeleteHandler(dp NamespaceDeprovisioner, orm rqlite.Client, logger *zap.Logger) *DeleteHandler {
	return &DeleteHandler{
		deprovisioner: dp,
		ormClient:     orm,
		logger:        logger.With(zap.String("component", "namespace-delete-handler")),
	}
}

// ServeHTTP handles DELETE /v1/namespace/delete
func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		writeDeleteResponse(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
		return
	}

	// Get namespace from context (set by auth middleware — already ownership-verified)
	ns := ""
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			ns = s
		}
	}
	if ns == "" || ns == "default" {
		writeDeleteResponse(w, http.StatusBadRequest, map[string]interface{}{"error": "cannot delete default namespace"})
		return
	}

	if h.deprovisioner == nil {
		writeDeleteResponse(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "cluster provisioning not enabled"})
		return
	}

	// Resolve namespace ID
	var rows []map[string]interface{}
	if err := h.ormClient.Query(r.Context(), &rows, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns); err != nil || len(rows) == 0 {
		writeDeleteResponse(w, http.StatusNotFound, map[string]interface{}{"error": "namespace not found"})
		return
	}

	var namespaceID int64
	switch v := rows[0]["id"].(type) {
	case float64:
		namespaceID = int64(v)
	case int64:
		namespaceID = v
	case int:
		namespaceID = int64(v)
	default:
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": "invalid namespace ID type"})
		return
	}

	h.logger.Info("Deprovisioning namespace cluster",
		zap.String("namespace", ns),
		zap.Int64("namespace_id", namespaceID),
	)

	// Deprovision the cluster (stops processes, deallocates ports, deletes DB records)
	if err := h.deprovisioner.DeprovisionCluster(r.Context(), namespaceID); err != nil {
		h.logger.Error("Failed to deprovision cluster", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	// Delete API keys, ownership records, and namespace record
	h.ormClient.Exec(r.Context(), "DELETE FROM wallet_api_keys WHERE namespace_id = ?", namespaceID)
	h.ormClient.Exec(r.Context(), "DELETE FROM api_keys WHERE namespace_id = ?", namespaceID)
	h.ormClient.Exec(r.Context(), "DELETE FROM namespace_ownership WHERE namespace_id = ?", namespaceID)
	h.ormClient.Exec(r.Context(), "DELETE FROM namespaces WHERE id = ?", namespaceID)

	h.logger.Info("Namespace deleted successfully", zap.String("namespace", ns))

	writeDeleteResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "deleted",
		"namespace": ns,
	})
}

func writeDeleteResponse(w http.ResponseWriter, status int, resp map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
