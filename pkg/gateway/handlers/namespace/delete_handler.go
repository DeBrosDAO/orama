package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
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
	ipfsClient    ipfs.IPFSClient // can be nil
	logger        *zap.Logger
}

// NewDeleteHandler creates a new delete handler
func NewDeleteHandler(dp NamespaceDeprovisioner, orm rqlite.Client, ipfsClient ipfs.IPFSClient, logger *zap.Logger) *DeleteHandler {
	return &DeleteHandler{
		deprovisioner: dp,
		ormClient:     orm,
		ipfsClient:    ipfsClient,
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

	// 1. Deprovision the cluster (stops infra + deployment processes, deallocates ports, deletes DNS)
	if err := h.deprovisioner.DeprovisionCluster(r.Context(), namespaceID); err != nil {
		h.logger.Error("Failed to deprovision cluster", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	// 2. Unpin IPFS content (must run before global table cleanup to read CID list)
	h.unpinNamespaceContent(r.Context(), ns)

	// 3. Clean up global tables that use namespace TEXT (not FK cascade)
	h.cleanupGlobalTables(r.Context(), ns)

	// 4. Delete API keys, ownership records, and namespace record (FK cascade handles children)
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

// unpinNamespaceContent unpins all IPFS content owned by the namespace.
// Best-effort: individual failures are logged but do not abort deletion.
func (h *DeleteHandler) unpinNamespaceContent(ctx context.Context, ns string) {
	if h.ipfsClient == nil {
		h.logger.Debug("IPFS client not available, skipping IPFS cleanup")
		return
	}

	type cidRow struct {
		CID string `db:"cid"`
	}
	var rows []cidRow
	if err := h.ormClient.Query(ctx, &rows,
		"SELECT cid FROM ipfs_content_ownership WHERE namespace = ?", ns); err != nil {
		h.logger.Warn("Failed to query IPFS content for namespace",
			zap.String("namespace", ns), zap.Error(err))
		return
	}

	if len(rows) == 0 {
		return
	}

	h.logger.Info("Unpinning IPFS content for namespace",
		zap.String("namespace", ns),
		zap.Int("cid_count", len(rows)))

	for _, row := range rows {
		if err := h.ipfsClient.Unpin(ctx, row.CID); err != nil {
			h.logger.Warn("Failed to unpin CID (best-effort)",
				zap.String("cid", row.CID),
				zap.String("namespace", ns),
				zap.Error(err))
		}
	}
}

// cleanupGlobalTables deletes orphaned records from global tables that reference
// the namespace by TEXT name (not by integer FK, so CASCADE doesn't help).
// Best-effort: individual failures are logged but do not abort deletion.
func (h *DeleteHandler) cleanupGlobalTables(ctx context.Context, ns string) {
	tables := []struct {
		table  string
		column string
	}{
		{"global_deployment_subdomains", "namespace"},
		{"ipfs_content_ownership", "namespace"},
		{"functions", "namespace"},
		{"function_secrets", "namespace"},
		{"namespace_sqlite_databases", "namespace"},
		{"namespace_quotas", "namespace"},
		{"home_node_assignments", "namespace"},
	}

	for _, t := range tables {
		query := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t.table, t.column)
		if _, err := h.ormClient.Exec(ctx, query, ns); err != nil {
			h.logger.Warn("Failed to clean up global table (best-effort)",
				zap.String("table", t.table),
				zap.String("namespace", ns),
				zap.Error(err))
		}
	}
}

func writeDeleteResponse(w http.ResponseWriter, status int, resp map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
