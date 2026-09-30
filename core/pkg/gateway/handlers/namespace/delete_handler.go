package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
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
	// refs is the cluster-wide reference index. orm is the cluster registry, so
	// it is the index itself.
	refs   *storage.CIDRefs
	audit  *auth.AuditLog
	logger *zap.Logger
}

// NewDeleteHandler creates a new delete handler
func NewDeleteHandler(dp NamespaceDeprovisioner, orm rqlite.Client, ipfsClient ipfs.IPFSClient, audit *auth.AuditLog, logger *zap.Logger) *DeleteHandler {
	return &DeleteHandler{
		deprovisioner: dp,
		ormClient:     orm,
		ipfsClient:    ipfsClient,
		refs:          storage.NewCIDRefs(orm),
		audit:         audit,
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
	h.remove(w, r, ns, auth.AuditNamespaceDeleted, nil)
}

// remove tears namespace ns down and deletes it: its cluster, deployments,
// content references, rows and grants. The owner's delete and an operator's
// removal of a namespace whose owner is gone both come here; action and
// metadata are what the audit trail records for it.
func (h *DeleteHandler) remove(w http.ResponseWriter, r *http.Request, ns, action string, metadata map[string]string) {
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

	// 0. Refuse while the cluster reference index cannot say who else holds this
	// namespace's content: deleting first and finding out later would leave the
	// cluster torn down and another tenant's pin possibly removed. The namespace
	// being deleted does not count against its own readiness.
	if err := h.refs.CheckReady(r.Context(), ns); err != nil {
		h.logger.Error("Namespace delete refused: the cluster reference index cannot be trusted yet",
			zap.String("namespace", ns), zap.Error(err))
		writeDeleteResponse(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":     "the cluster reference index is not ready to release this namespace's content; retry shortly",
			"retryable": true,
		})
		return
	}

	// 1. Deprovision the cluster (stops infra on ALL nodes, deletes cluster-state, deallocates ports, deletes DNS)
	if err := h.deprovisioner.DeprovisionCluster(r.Context(), namespaceID); err != nil {
		h.logger.Error("Failed to deprovision cluster", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	// 2. Clean up deployments (teardown replicas on all nodes, unpin IPFS, delete DB records)
	h.cleanupDeployments(r.Context(), ns)

	// 3. Unpin IPFS content from ipfs_content_ownership (separate from deployment CIDs)
	if err := h.unpinNamespaceContent(r.Context(), ns); err != nil {
		h.logger.Error("Failed to release the namespace's IPFS references", zap.String("namespace", ns), zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"error":     "the namespace's IPFS references could not be released; retry the delete",
			"retryable": true,
		})
		return
	}

	// 4. Clean up global tables that use namespace TEXT (not FK cascade)
	h.cleanupGlobalTables(r.Context(), ns)

	// 5. Delete FK children explicitly (ON DELETE CASCADE is decorative:
	// rqlited is not started with -fk, bugboard #164). Check every error.
	if err := h.deleteNamespaceRows(r.Context(), namespaceID, ns); err != nil {
		h.logger.Error("Failed to delete namespace rows", zap.Error(err))
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Info("Namespace deleted successfully", zap.String("namespace", ns))

	// Deleting a namespace takes every deployment, key and grant in it with
	// it. It is the most destructive thing an owner can do, so it belongs in
	// the record.
	//
	// Recorded at cluster level rather than against the namespace: names are
	// reusable, and whoever creates this one next would otherwise open their
	// audit trail on the previous tenant's wallet.
	h.audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Actor:    auth.ActorFromRequest(r),
		Action:   action,
		Resource: ns,
		Result:   auth.AuditSuccess,
		Metadata: metadata,
	})

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

	// 3. Clean up deployment DB records (children first, since FK cascades disabled in rqlite)
	for _, dep := range deps {
		// Child tables with FK to deployments(id)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_replicas WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM port_allocations WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_domains WHERE deployment_id = ?", dep.ID)
		h.ormClient.Exec(ctx, "DELETE FROM deployment_history WHERE deployment_id = ?", dep.ID)
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
		url := constants.GatewayURLFor(node.InternalIP) + "/v1/internal/deployments/replica/teardown"
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

// unpinNamespaceContent releases every reference the namespace holds in the
// cluster reference index (its storage pins and its deployments' content and
// build CIDs, and its backfill marker) and removes the cluster pin of each CID
// that leaves no reference anywhere. The index, not the namespace's database,
// is what says who holds a CID. A failure to release is returned, so the delete
// fails and is retried: references left behind would hold other tenants' pins
// and skip the backfill of a namespace recreated under the same name. Failing
// to unpin an orphaned CID is only logged, as it always was.
func (h *DeleteHandler) unpinNamespaceContent(ctx context.Context, ns string) error {
	orphaned, releaseErr := h.refs.ReleaseNamespace(ctx, ns)
	if h.ipfsClient != nil && len(orphaned) > 0 {
		h.logger.Info("Unpinning IPFS content for namespace",
			zap.String("namespace", ns),
			zap.Int("cid_count", len(orphaned)))
		for _, cid := range orphaned {
			if err := h.ipfsClient.Unpin(ctx, cid); err != nil {
				h.logger.Warn("Failed to unpin CID (best-effort)",
					zap.String("cid", cid),
					zap.String("namespace", ns),
					zap.Error(err))
			}
		}
	}
	if releaseErr != nil {
		return fmt.Errorf("failed to release the IPFS references of namespace %s: %w", ns, releaseErr)
	}
	return nil
}

// namespaceFKChildren are tables that declare REFERENCES namespaces(id)
// ON DELETE CASCADE. CASCADE does not fire (foreign_keys=0 / no -fk), so
// deleteNamespaceRows must empty them first. Order: children, then namespaces.
var namespaceFKChildren = []string{
	"wallet_api_keys",
	"api_keys",
	"grants",
	"apps",
	"nonces",
	"subscriptions",
	"refresh_tokens",
	"namespace_clusters",
}

// deleteNamespaceRows empties every table that belongs to a namespace, then the
// namespace row itself.
//
// audit_events is not in namespaceFKChildren because it is not keyed the same
// way: migration 048 rebuilt it with the namespace's NAME, so that an event
// against a namespace that does not exist can still be recorded. Deleting it by
// namespace_id fails on the missing column and leaves the namespace half
// removed — its cluster already gone.
func (h *DeleteHandler) deleteNamespaceRows(ctx context.Context, namespaceID int64, name string) error {
	for _, table := range namespaceFKChildren {
		if _, err := h.ormClient.Exec(ctx, "DELETE FROM "+table+" WHERE namespace_id = ?", namespaceID); err != nil {
			return fmt.Errorf("delete %s for namespace %d: %w", table, namespaceID, err)
		}
	}
	// The trail goes with the namespace: a namespace created later under the
	// same name would otherwise inherit a stranger's history.
	if _, err := h.ormClient.Exec(ctx, "DELETE FROM audit_events WHERE namespace = ?", name); err != nil {
		return fmt.Errorf("delete audit_events for namespace %s: %w", name, err)
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
