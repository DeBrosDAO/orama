package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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

	h.logger.Info("Deleting namespace",
		zap.String("namespace", ns),
		zap.Int64("namespace_id", namespaceID),
	)

	// 1. Deprovision the cluster (stops infra on ALL nodes, deletes cluster-state, deallocates ports, deletes DNS)
	if err := h.deprovisioner.DeprovisionCluster(r.Context(), namespaceID); err != nil {
		h.logger.Error("Failed to deprovision cluster", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	// 2. Clean up deployments (teardown replicas on all nodes, unpin IPFS, delete DB records)
	h.cleanupDeployments(r.Context(), ns)

	// 3. Unpin IPFS content from ipfs_content_ownership (separate from deployment CIDs)
	h.unpinNamespaceContent(r.Context(), ns)

	// 4. Clean up global tables that use namespace TEXT (not FK cascade)
	h.cleanupGlobalTables(r.Context(), ns)

	// 5. Delete FK children explicitly (ON DELETE CASCADE is decorative:
	// rqlited is not started with -fk, bugboard #164). Check every error.
	if err := h.deleteNamespaceRows(r.Context(), namespaceID); err != nil {
		h.logger.Error("Failed to delete namespace rows", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Info("Namespace deleted successfully", zap.String("namespace", ns))

	writeDeleteResponse(w, http.StatusOK, map[string]interface{}{
		"status":    "deleted",
		"namespace": ns,
	})
}

// cleanupDeployments tears down all deployment replicas on all nodes, unpins IPFS content,
// and deletes all deployment-related DB records for the namespace.
// Best-effort: individual failures are logged but do not abort deletion.
func (h *DeleteHandler) cleanupDeployments(ctx context.Context, ns string) {
	type deploymentInfo struct {
		ID         string `db:"id"`
		Name       string `db:"name"`
		Type       string `db:"type"`
		ContentCID string `db:"content_cid"`
		BuildCID   string `db:"build_cid"`
	}
	var deps []deploymentInfo
	if err := h.ormClient.Query(ctx, &deps,
		"SELECT id, name, type, content_cid, build_cid FROM deployments WHERE namespace = ?", ns); err != nil {
		h.logger.Warn("Failed to query deployments for cleanup",
			zap.String("namespace", ns), zap.Error(err))
		return
	}

	if len(deps) == 0 {
		return
	}

	h.logger.Info("Cleaning up deployments for namespace",
		zap.String("namespace", ns),
		zap.Int("count", len(deps)))

	// 1. Send teardown to all replica nodes for each deployment
	for _, dep := range deps {
		h.teardownDeploymentReplicas(ctx, ns, dep.ID, dep.Name, dep.Type)
	}

	// 2. Unpin deployment IPFS content
	if h.ipfsClient != nil {
		for _, dep := range deps {
			if dep.ContentCID != "" {
				if err := h.ipfsClient.Unpin(ctx, dep.ContentCID); err != nil {
					h.logger.Warn("Failed to unpin deployment content CID",
						zap.String("deployment_id", dep.ID),
						zap.String("cid", dep.ContentCID), zap.Error(err))
				}
			}
			if dep.BuildCID != "" {
				if err := h.ipfsClient.Unpin(ctx, dep.BuildCID); err != nil {
					h.logger.Warn("Failed to unpin deployment build CID",
						zap.String("deployment_id", dep.ID),
						zap.String("cid", dep.BuildCID), zap.Error(err))
				}
			}
		}
	}

	// 3. Clean up deployment DB records (children first, since FK cascades disabled in rqlite)
	for _, dep := range deps {
		// Child tables with FK to deployments(id)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_replicas WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM port_allocations WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_domains WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_history WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_env_vars WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_events WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_health_checks WHERE deployment_id = ?", dep.ID)
		// Tables with no FK constraint
		h.ormClient.Exec(ctx, "DELETE FROM dns_records WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM global_deployment_subdomains WHERE deployment_id = ?", dep.ID)
	}
	h.ormClient.Exec(ctx, "DELETE FROM deployments WHERE namespace = ?", ns)

	h.logger.Info("Deployment cleanup completed",
		zap.String("namespace", ns),
		zap.Int("deployments_cleaned", len(deps)))
}

// teardownDeploymentReplicas sends a teardown request to every node that has a replica
// of the given deployment. Each node stops its process, removes files, and deallocates its port.
func (h *DeleteHandler) teardownDeploymentReplicas(ctx context.Context, ns, deploymentID, name, depType string) {
	type replicaNode struct {
		NodeID     string `db:"node_id"`
		InternalIP string `db:"internal_ip"`
	}
	var nodes []replicaNode
	query := `
		SELECT dr.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip
		FROM deployment_replicas dr
		JOIN dns_nodes dn ON dr.node_id = dn.id
		WHERE dr.deployment_id = ?
	`
	if err := h.ormClient.Query(ctx, &nodes, query, deploymentID); err != nil {
		h.logger.Warn("Failed to query replica nodes for teardown",
			zap.String("deployment_id", deploymentID), zap.Error(err))
		return
	}

	if len(nodes) == 0 {
		return
	}

	payload := map[string]interface{}{
		"deployment_id": deploymentID,
		"namespace":     ns,
		"name":          name,
		"type":          depType,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("Failed to marshal teardown payload", zap.Error(err))
		return
	}

	for _, node := range nodes {
		url := fmt.Sprintf("http://%s:6001/v1/internal/deployments/replica/teardown", node.InternalIP)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			h.logger.Warn("Failed to create teardown request",
				zap.String("node_id", node.NodeID), zap.Error(err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Orama-Internal-Auth", "replica-coordination")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			h.logger.Warn("Failed to send teardown to replica node",
				zap.String("deployment_id", deploymentID),
				zap.String("node_id", node.NodeID),
				zap.String("node_ip", node.InternalIP),
				zap.Error(err))
			continue
		}
		resp.Body.Close()
	}
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

// namespaceFKChildren are tables that declare REFERENCES namespaces(id)
// ON DELETE CASCADE. CASCADE does not fire (foreign_keys=0 / no -fk), so
// deleteNamespaceRows must empty them first. Order: children, then namespaces.
var namespaceFKChildren = []string{
	"wallet_api_keys",
	"api_keys",
	"namespace_ownership",
	"apps",
	"nonces",
	"subscriptions",
	"refresh_tokens",
	"audit_events",
	"namespace_clusters",
}

func (h *DeleteHandler) deleteNamespaceRows(ctx context.Context, namespaceID int64) error {
	for _, table := range namespaceFKChildren {
		if _, err := h.ormClient.Exec(ctx, "DELETE FROM "+table+" WHERE namespace_id = ?", namespaceID); err != nil {
			return fmt.Errorf("delete %s for namespace %d: %w", table, namespaceID, err)
		}
	}
	if _, err := h.ormClient.Exec(ctx, "DELETE FROM namespaces WHERE id = ?", namespaceID); err != nil {
		return fmt.Errorf("delete namespaces id %d: %w", namespaceID, err)
	}
	return nil
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
		{"webrtc_rooms", "namespace_name"},
		{"namespace_webrtc_config", "namespace_name"},
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
