package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// removeTimeout bounds a whole namespace removal: the cluster teardown
// (namespacepkg.DeprovisionTimeout, which the reconciler's takeover of an
// abandoned one is timed against) and the cleanup of deployments, content
// references and rows after it.
const removeTimeout = namespacepkg.DeprovisionTimeout + 5*time.Minute

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
	// clusterSecretPath is where the cluster secret the replica teardown calls
	// are stamped with is read from, per call, as the spawn requests do.
	clusterSecretPath string

	// removing holds the namespaces this process is removing, so a client that
	// retries a delete the first request is still carrying out is refused
	// without touching the registry (which refuses it across gateways).
	removingMu sync.Mutex
	removing   map[string]bool
}

const (
	// ErrCodeDeleteInProgress is the code of the 409 a second delete of a
	// namespace gets while the first still runs.
	ErrCodeDeleteInProgress = "NAMESPACE_DELETE_IN_PROGRESS"

	// deleteInProgressRetryAfterSeconds is that refusal's Retry-After.
	deleteInProgressRetryAfterSeconds = 15

	// releaseClaimTimeout bounds the write that gives back a failed attempt's
	// teardown claim; it runs on its own context, since the removal's may be
	// the one that expired.
	releaseClaimTimeout = 10 * time.Second
)

// beginRemoving records ns as being removed by this process, false if it
// already is.
func (h *DeleteHandler) beginRemoving(ns string) bool {
	h.removingMu.Lock()
	defer h.removingMu.Unlock()
	if h.removing == nil {
		h.removing = make(map[string]bool)
	}
	if h.removing[ns] {
		return false
	}
	h.removing[ns] = true
	return true
}

func (h *DeleteHandler) endRemoving(ns string) {
	h.removingMu.Lock()
	defer h.removingMu.Unlock()
	delete(h.removing, ns)
}

// refuseDeleteInProgress answers 409 to a delete of a namespace whose removal is
// already running. The first request keeps running when its client leaves, so a
// client that retries is expected.
func refuseDeleteInProgress(w http.ResponseWriter, ns string) {
	w.Header().Set("Retry-After", fmt.Sprint(deleteInProgressRetryAfterSeconds))
	writeDeleteResponse(w, http.StatusConflict, map[string]interface{}{
		"error":     fmt.Sprintf("namespace %s is already being deleted; retry shortly to see it finished", ns),
		"code":      ErrCodeDeleteInProgress,
		"retryable": true,
	})
}

// SetClusterSecretPath sets where the replica teardown calls get their key.
func (h *DeleteHandler) SetClusterSecretPath(path string) { h.clusterSecretPath = path }

// signReplicaTeardown stamps a replica teardown for the node whose peer id is
// audience.
func (h *DeleteHandler) signReplicaTeardown(req *http.Request, audience string) error {
	secret, err := os.ReadFile(h.clusterSecretPath)
	if err != nil {
		return fmt.Errorf("cannot read the cluster secret at %q, so the replica teardown for node %s "+
			"cannot be stamped: %w", h.clusterSecretPath, audience, err)
	}
	key, err := nodeauth.CoordinationKey(string(secret))
	if err != nil {
		return err
	}
	return nodeauth.SignCoordination(key, req, time.Now(), audience)
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

	// The removal is owned by this handler, not by the request: a client that
	// disconnects mid-delete (a killed CLI, a proxy timeout) cancels the request
	// context, and every step below that used it stopped where it was: the stop
	// sent to a node failed "context canceled", and so did the write recording
	// that failure for replay, which left a cluster in 'deprovisioning' with no
	// teardown owed to anyone. The values of the request (the namespace override,
	// the auth the registry writes carry) are kept; its cancellation is not.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), removeTimeout)
	defer cancel()

	if !h.beginRemoving(ns) {
		refuseDeleteInProgress(w, ns)
		return
	}
	defer h.endRemoving(ns)

	// Resolve namespace ID
	var rows []map[string]interface{}
	if err := h.ormClient.Query(ctx, &rows, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns); err != nil || len(rows) == 0 {
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
	if err := h.refs.CheckReady(ctx, ns); err != nil {
		h.logger.Error("Namespace delete refused: the cluster reference index cannot be trusted yet",
			zap.String("namespace", ns), zap.Error(err))
		writeDeleteResponse(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":     "the cluster reference index is not ready to release this namespace's content; retry shortly",
			"retryable": true,
		})
		return
	}

	// From here the cluster is touched, so a failure is recorded in the audit
	// trail as well as logged: a removal that stopped half way is exactly what
	// someone will later ask about. The response carries a fixed message; the
	// detail (driver and registry text) is only logged.
	// claimed is set once BeginDeprovision has given this attempt the claim.
	claimed := false
	failed := func(stage, message string, err error) {
		h.logger.Error("Namespace removal failed", zap.String("namespace", ns), zap.String("stage", stage), zap.Error(err))
		// A cluster that is still there must not stay refused for the rest of
		// the window by the claim this attempt took, or the retry it asks for
		// would be answered 409. Only a claim this attempt holds is given back:
		// a claim that failed may be another teardown's, which must keep it.
		if claimed {
			h.releaseClaim(ctx, ns, namespaceID)
		}
		h.audit.RecordFromRequest(ctx, r, auth.AuditEvent{
			Actor:    auth.ActorFromRequest(r),
			Action:   action,
			Resource: ns,
			Result:   auth.AuditFailure,
			Metadata: metadata,
		})
		writeDeleteResponse(w, http.StatusInternalServerError, map[string]interface{}{"error": message, "retryable": true})
	}

	// 1. Deprovision the cluster (stops infra on ALL nodes, deletes cluster-state, deallocates ports, deletes DNS)
	//
	// A node that did not confirm its teardown is not a reason to stop here: by
	// then the cluster row is gone and the unconfirmed teardown is recorded for
	// replay, so stopping would leave the namespace row and its owner grant
	// behind with no cluster, counted against the owner's cap, and a retry of
	// the delete would find nothing to deprovision and do what is done below.
	// Creating the name again is refused (CreateHandler) until it has finished.
	cleanupPending := false
	if err := namespacepkg.BeginDeprovision(ctx, h.ormClient, namespaceID); err != nil {
		if errors.Is(err, namespacepkg.ErrDeprovisionInProgress) {
			h.logger.Warn("Namespace delete refused: a teardown of its cluster is already running", zap.String("namespace", ns))
			refuseDeleteInProgress(w, ns)
			return
		}
		failed("deprovision", "the namespace's cluster could not be claimed for deprovisioning; retry the delete", err)
		return
	}
	claimed = true
	deprovisionCtx, cancelDeprovision := context.WithTimeout(ctx, namespacepkg.DeprovisionTimeout)
	err := h.deprovisioner.DeprovisionCluster(deprovisionCtx, namespaceID)
	cancelDeprovision()
	if err != nil {
		if !errors.Is(err, namespacepkg.ErrTeardownIncomplete) {
			failed("deprovision", "the namespace's cluster could not be deprovisioned; retry the delete", err)
			return
		}
		cleanupPending = true
		h.logger.Warn("Namespace cluster removed, but a node did not confirm its teardown; the teardown stays recorded for replay",
			zap.String("namespace", ns), zap.Error(err))
	}

	// 2. Clean up deployments (teardown replicas on all nodes, unpin IPFS, delete DB records)
	h.cleanupDeployments(ctx, ns)

	// 3. Unpin IPFS content from ipfs_content_ownership (separate from deployment CIDs)
	if err := h.unpinNamespaceContent(ctx, ns); err != nil {
		failed("release-ipfs-references", "the namespace's IPFS references could not be released; retry the delete", err)
		return
	}

	// 4. Clean up global tables that use namespace TEXT (not FK cascade)
	h.cleanupGlobalTables(ctx, ns)

	// 5. Delete FK children explicitly (ON DELETE CASCADE is decorative:
	// rqlited is not started with -fk, bugboard #164). Check every error.
	if err := h.deleteNamespaceRows(ctx, namespaceID, ns); err != nil {
		failed("delete-rows", "the namespace's records could not be deleted; retry the delete", err)
		return
	}

	// Last, after the namespace row is gone: the marker is what keeps every
	// other namespace's readiness check true for a namespace that still exists,
	// so it must not be removed before the steps that can still fail. A marker
	// that outlives its namespace is inert: nothing counts a namespace that is
	// not in the registry, and a namespace recreated under the name only skips a
	// backfill it has no content for.
	if err := h.refs.RemoveMarker(ctx, ns); err != nil {
		h.logger.Error("Namespace deleted, but its reference index marker could not be removed; it is inert", zap.String("namespace", ns), zap.Error(err))
	}

	h.logger.Info("Namespace deleted successfully", zap.String("namespace", ns), zap.Bool("cleanup_pending", cleanupPending))

	// Deleting a namespace takes every deployment, key and grant in it with
	// it. It is the most destructive thing an owner can do, so it belongs in
	// the record.
	//
	// Recorded at cluster level rather than against the namespace: names are
	// reusable, and whoever creates this one next would otherwise open their
	// audit trail on the previous tenant's wallet.
	resp := map[string]interface{}{"status": "deleted", "namespace": ns}
	if cleanupPending {
		resp["cleanup_pending"] = true
		metadata = withTeardownPending(metadata)
	}
	h.audit.RecordFromRequest(ctx, r, auth.AuditEvent{
		Actor:    auth.ActorFromRequest(r),
		Action:   action,
		Resource: ns,
		Result:   auth.AuditSuccess,
		Metadata: metadata,
	})

	writeDeleteResponse(w, http.StatusOK, resp)
}

// withTeardownPending is metadata plus the note that a node's teardown is still
// owed. The caller's map is not modified.
func withTeardownPending(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+1)
	for k, v := range metadata {
		out[k] = v
	}
	out["teardown"] = "pending"
	return out
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
		if err := h.signReplicaTeardown(req, node.NodeID); err != nil {
			h.logger.Warn("Failed to sign the teardown request",
				zap.String("node_id", node.NodeID), zap.Error(err))
			continue
		}

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
// build CIDs) and removes the cluster pin of each CID
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
			if _, err := h.refs.UnpinUnreferenced(ctx, h.ipfsClient, cid); err != nil {
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

// releaseClaim gives back the teardown claim of a removal that failed, on a
// context of its own: the removal's may have expired, which is often why it
// failed.
func (h *DeleteHandler) releaseClaim(ctx context.Context, ns string, namespaceID int64) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()
	if err := namespacepkg.ReleaseDeprovision(rctx, h.ormClient, namespaceID); err != nil {
		h.logger.Error("Could not release the teardown claim after a failed removal; a retry is refused until the window ends",
			zap.String("namespace", ns), zap.Error(err))
	}
}
