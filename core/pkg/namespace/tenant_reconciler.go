package namespace

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// The tenant plane converges rather than being nudged.
//
// rqlite, Olric and the gateway for each namespace used to be edge-triggered:
// provisioned once, repaired on a dead-node event, and restored once at boot by
// a loop that gave up after twelve attempts. Anything that happened outside
// those moments stayed broken until someone ran a runbook — and seven manual
// steps existed precisely because of that. The WebRTC plane already converges
// on a 60s loop; this is the same shape for the rest of the tenant services.
//
// Two legs, because they answer different questions and have different
// authority:
//
//   - The PER-NODE leg is what this node owes the namespaces it hosts: start
//     what is missing, and rewrite a config that has drifted. Every node runs
//     it, for its own services only.
//   - The COORDINATOR leg is cluster-wide state — pruning departed members,
//     releasing their ports, taking them out of the namespace's raft. Exactly
//     one node may do that per sweep, elected deterministically, because
//     concurrent writers are how these stores diverged in the first place.

// tenantReconcileInterval is how often each node re-checks its tenant services.
// Matches the WebRTC reconciler, and is the same order as the systemd restart
// backoff — fast enough that a failure is corrected within a minute or two,
// slow enough not to fight a service that is legitimately starting.
const tenantReconcileInterval = 60 * time.Second

// StartTenantReconciler runs the tenant convergence loop until ctx is done.
//
// The first sweep runs immediately: after a restart this node's tenant services
// are down, and waiting a full interval to notice is the outage it exists to
// end.
func (cm *ClusterManager) StartTenantReconciler(ctx context.Context) {
	go func() {
		cm.reconcileTenantsOnce(ctx)

		ticker := time.NewTicker(tenantReconcileInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				cm.logger.Info("Tenant reconciler stopping")
				return
			case <-ticker.C:
				cm.reconcileTenantsOnce(ctx)
			}
		}
	}()
}

// reconcileTenantsOnce is one sweep. It never returns an error: a sweep that
// fails is retried by the next one, and the loop must outlive any single
// failure.
func (cm *ClusterManager) reconcileTenantsOnce(ctx context.Context) {
	if cm.localNodeID == "" {
		cm.logger.Warn("Tenant reconcile skipped: this node has no id, so it cannot tell which services are its own")
		return
	}

	// Per-node leg. RestoreLocalClusters spawns whatever is missing and skips
	// what is already running; reconcileLocalDrift is what corrects the ones it
	// skipped.
	if err := cm.RestoreLocalClusters(ctx); err != nil {
		cm.logger.Warn("Tenant reconcile: could not start missing services this sweep", zap.Error(err))
	}
	if err := cm.reconcileLocalDrift(ctx); err != nil {
		cm.logger.Warn("Tenant reconcile: could not reconcile local service configs this sweep", zap.Error(err))
	}

	// Coordinator leg.
	if err := cm.replayPendingCleanups(ctx); err != nil {
		cm.logger.Warn("Tenant reconcile: could not replay pending cleanups this sweep", zap.Error(err))
	}
	if err := cm.reconcileClusterMembership(ctx); err != nil {
		cm.logger.Warn("Tenant reconcile: could not reconcile cluster membership this sweep", zap.Error(err))
	}
}

// tenantAssignment is one namespace this node hosts services for.
type tenantAssignment struct {
	ClusterID     string `db:"namespace_cluster_id"`
	NamespaceName string `db:"namespace_name"`
}

// localAssignments returns the ready namespaces this node hosts.
func (cm *ClusterManager) localAssignments(ctx context.Context) ([]tenantAssignment, error) {
	var out []tenantAssignment
	err := cm.db.Query(client.WithInternalAuth(ctx), &out, `
		SELECT DISTINCT cn.namespace_cluster_id, c.namespace_name
		  FROM namespace_cluster_nodes cn
		  JOIN namespace_clusters c ON cn.namespace_cluster_id = c.id
		 WHERE cn.node_id = ? AND c.status IN ('ready', 'degraded')`, cm.localNodeID)
	if err != nil {
		return nil, fmt.Errorf("query local tenant assignments: %w", err)
	}
	return out, nil
}

// reconcileLocalDrift rewrites this node's Olric and gateway configs when the
// live membership no longer matches what is on disk.
//
// This is the half that was missing entirely. ReplaceClusterNode wrote config
// only for the REPLACEMENT node, so every survivor kept the departed node's
// overlay address in its Olric peers and its gateway's olric_servers — for
// ever, since nothing else rewrote them. ReconcileGateway existed but was only
// ever called from the boot restore, and there was no ReconcileOlric at all.
func (cm *ClusterManager) reconcileLocalDrift(ctx context.Context) error {
	assignments, err := cm.localAssignments(ctx)
	if err != nil {
		return err
	}

	var errs []error
	for _, a := range assignments {
		if err := cm.reconcileNamespaceOnThisNode(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.NamespaceName, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("reconcile local tenant configs: %v", errs)
	}
	return nil
}

// reconcileNamespaceOnThisNode brings one namespace's local services in line
// with the live membership.
func (cm *ClusterManager) reconcileNamespaceOnThisNode(ctx context.Context, a tenantAssignment) error {
	desired, err := cm.desiredLocalConfig(ctx, a.ClusterID)
	if err != nil {
		return err
	}
	if desired == nil {
		// No port allocation for this node on this cluster. Either the
		// assignment is being torn down or the allocation has not been written
		// yet; both resolve on a later sweep, and neither is a reason to touch
		// a running service.
		return nil
	}

	// Only reconcile a service that is actually running. A stopped one is
	// RestoreLocalClusters' job, and its cold-spawn path writes a fresh config
	// anyway — reconciling it here would just race that.
	if active, err := cm.systemdSpawner.systemdMgr.IsServiceActive(a.NamespaceName, systemd.ServiceTypeOlric); err == nil && active {
		if err := cm.systemdSpawner.ReconcileOlric(ctx, a.NamespaceName, cm.localNodeID, desired.Olric); err != nil {
			return fmt.Errorf("reconcile olric: %w", err)
		}
	}

	if active, err := cm.systemdSpawner.systemdMgr.IsServiceActive(a.NamespaceName, systemd.ServiceTypeGateway); err == nil && active {
		if err := cm.systemdSpawner.ReconcileGateway(ctx, a.NamespaceName, cm.localNodeID, desired.Gateway); err != nil {
			return fmt.Errorf("reconcile gateway: %w", err)
		}
	}
	return nil
}

// localServiceConfig is the desired config for this node's services in one
// namespace, derived from live membership.
type localServiceConfig struct {
	Olric   olric.InstanceConfig
	Gateway gateway.InstanceConfig
}

// reconcileClusterMembership is the coordinator leg: forget members that are
// permanently gone, and take them out of the namespace's raft.
//
// Pruning already released their ports (bugboard #280). What it never did was
// remove them from raft, so a namespace kept counting a departed node toward
// quorum — the tenant-plane version of the dead-voter problem, and the reason a
// three-member namespace could not survive its second replacement.
func (cm *ClusterManager) reconcileClusterMembership(ctx context.Context) error {
	assignments, err := cm.localAssignments(ctx)
	if err != nil {
		return err
	}

	var errs []error
	for _, a := range assignments {
		// Same membership read the WebRTC reconciler uses, so the two legs
		// cannot disagree about who is live.
		_, live, err := cm.getWebRTCMemberStatus(ctx, a.ClusterID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.NamespaceName, err))
			continue
		}
		if coordinator := tenantReconcileCoordinator(live); coordinator != cm.localNodeID {
			continue
		}

		if err := cm.pruneAndDeraft(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.NamespaceName, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("reconcile tenant membership: %v", errs)
	}
	return nil
}

// tenantReconcileCoordinator picks the one node that may make cluster-wide
// changes this sweep: the lowest-sorted live member.
//
// A deterministic election — every node computes the same answer from the same
// membership list, with no lock and no leader lookup — so exactly one applies
// the plan and the rest no-op. Same rule the WebRTC reconciler uses.
func tenantReconcileCoordinator(liveNodeIDs []string) string {
	if len(liveNodeIDs) == 0 {
		return ""
	}
	sorted := append([]string(nil), liveNodeIDs...)
	sort.Strings(sorted)
	return sorted[0]
}

// desiredLocalConfig derives what this node's Olric and gateway configs SHOULD
// contain for a cluster, from the live membership.
//
// Returns nil when this node has no port allocation for the cluster, which is
// not an error: the assignment is either being torn down or not yet allocated,
// and neither is a reason to touch a running service.
func (cm *ClusterManager) desiredLocalConfig(ctx context.Context, clusterID string) (*localServiceConfig, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var mine []PortBlock
	if err := cm.db.Query(internalCtx, &mine,
		`SELECT * FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`,
		clusterID, cm.localNodeID); err != nil {
		return nil, fmt.Errorf("read this node's port allocation: %w", err)
	}
	if len(mine) == 0 {
		return nil, nil
	}
	block := mine[0]

	// Peers come from the CURRENT membership joined to dns_nodes, so a member
	// that has been pruned drops out of the list on the next sweep. Restricted
	// to nodes that are still active: a peer list that includes a departed
	// node is what made every gateway restart stall for minutes timing out
	// against it.
	type peerRow struct {
		NodeID              string `db:"node_id"`
		InternalIP          string `db:"internal_ip"`
		OlricMemberlistPort int    `db:"olric_memberlist_port"`
		OlricHTTPPort       int    `db:"olric_http_port"`
	}
	var peers []peerRow
	if err := cm.db.Query(internalCtx, &peers, `
		SELECT pa.node_id,
		       COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip,
		       pa.olric_memberlist_port, pa.olric_http_port
		  FROM namespace_port_allocations pa
		  JOIN dns_nodes dn ON pa.node_id = dn.id
		  JOIN namespace_cluster_nodes cn
		    ON cn.namespace_cluster_id = pa.namespace_cluster_id AND cn.node_id = pa.node_id
		 WHERE pa.namespace_cluster_id = ? AND dn.status = 'active'`, clusterID); err != nil {
		return nil, fmt.Errorf("read cluster peers: %w", err)
	}

	localIP, err := cm.nodeInternalIP(cm.localNodeID)
	if err != nil {
		return nil, err
	}

	olricPeers := make([]string, 0, len(peers))
	olricServers := make([]string, 0, len(peers))
	for _, p := range peers {
		if p.InternalIP == "" {
			continue
		}
		if p.NodeID != cm.localNodeID {
			olricPeers = append(olricPeers, fmt.Sprintf("%s:%d", p.InternalIP, p.OlricMemberlistPort))
		}
		olricServers = append(olricServers, fmt.Sprintf("%s:%d", p.InternalIP, p.OlricHTTPPort))
	}
	sort.Strings(olricPeers)
	sort.Strings(olricServers)

	return &localServiceConfig{
		Olric: olric.InstanceConfig{
			Namespace:      "",
			NodeID:         cm.localNodeID,
			HTTPPort:       block.OlricHTTPPort,
			MemberlistPort: block.OlricMemberlistPort,
			BindAddr:       localIP,
			AdvertiseAddr:  localIP,
			PeerAddresses:  olricPeers,
		},
		Gateway: gateway.InstanceConfig{
			NodeID:       cm.localNodeID,
			HTTPPort:     block.GatewayHTTPPort,
			OlricServers: olricServers,
		},
	}, nil
}

// pruneAndDeraft forgets members that are permanently gone, and takes them out
// of the namespace's raft.
//
// Order matters. The raft address has to be read BEFORE the prune, because
// pruning deletes the port allocation the address is built from — after that
// there is nothing left to name in the removal, and the departed node stays a
// configured voter for ever. That was the gap: pruning released the ports and
// the membership row and stopped there.
func (cm *ClusterManager) pruneAndDeraft(ctx context.Context, a tenantAssignment) error {
	gone, err := cm.staleMemberRaftAddrs(ctx, a.ClusterID)
	if err != nil {
		return err
	}
	if len(gone) == 0 {
		return nil
	}

	removed, err := cm.pruneStaleClusterNodes(ctx, a.ClusterID)
	if err != nil {
		return fmt.Errorf("prune stale members: %w", err)
	}
	if len(removed) == 0 {
		return nil
	}

	survivors, err := cm.survivingNodes(ctx, a.ClusterID)
	if err != nil {
		return fmt.Errorf("read surviving members: %w", err)
	}
	if len(survivors) == 0 {
		// Nothing left to issue the removal through. Say so rather than
		// silently leaving the raft configuration wrong.
		cm.logger.Error("Pruned a departed member but no surviving member can be reached to remove it from raft; "+
			"the namespace still counts it toward quorum",
			zap.String("namespace", a.NamespaceName),
			zap.Strings("pruned", removed))
		return fmt.Errorf("no surviving member to remove %v from raft", removed)
	}

	for _, nodeID := range removed {
		raftAddr, ok := gone[nodeID]
		if !ok || raftAddr == "" {
			cm.logger.Warn("Pruned a member with no recorded raft address; nothing to remove from raft",
				zap.String("namespace", a.NamespaceName),
				zap.String("node_id", nodeID))
			continue
		}
		cm.removeDeadNodeFromRaft(ctx, raftAddr, survivors)
		cm.logger.Info("Removed a departed member from the namespace raft configuration",
			zap.String("namespace", a.NamespaceName),
			zap.String("node_id", nodeID),
			zap.String("raft_addr", raftAddr))
	}
	return nil
}

// staleMemberRaftAddrs returns the raft address of each member that the prune
// is about to remove, keyed by node id.
//
// Read before the prune because pruning deletes the allocation these come from.
func (cm *ClusterManager) staleMemberRaftAddrs(ctx context.Context, clusterID string) (map[string]string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	cutoff := time.Now().UTC().Add(-clusterNodePurgeStaleAfter).Format("2006-01-02 15:04:05")

	var rows []struct {
		NodeID     string `db:"node_id"`
		InternalIP string `db:"internal_ip"`
		RaftPort   int    `db:"rqlite_raft_port"`
	}
	if err := cm.db.Query(internalCtx, &rows, `
		SELECT cn.node_id,
		       COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip,
		       COALESCE(pa.rqlite_raft_port, 0) AS rqlite_raft_port
		  FROM namespace_cluster_nodes cn
		  JOIN dns_nodes dn ON cn.node_id = dn.id
		  LEFT JOIN namespace_port_allocations pa
		    ON pa.namespace_cluster_id = cn.namespace_cluster_id AND pa.node_id = cn.node_id
		 WHERE cn.namespace_cluster_id = ?
		   AND dn.status != 'active'
		   AND COALESCE(dn.last_seen, '') < ?`, clusterID, cutoff); err != nil {
		return nil, fmt.Errorf("read stale member addresses: %w", err)
	}

	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.InternalIP == "" || r.RaftPort == 0 {
			out[r.NodeID] = ""
			continue
		}
		out[r.NodeID] = fmt.Sprintf("%s:%d", r.InternalIP, r.RaftPort)
	}
	return out, nil
}

// survivingNodes returns the members that are still active, with the ports a
// raft removal is issued through.
func (cm *ClusterManager) survivingNodes(ctx context.Context, clusterID string) ([]survivingNodePorts, error) {
	var out []survivingNodePorts
	err := cm.db.Query(client.WithInternalAuth(ctx), &out, `
		SELECT pa.node_id,
		       COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip,
		       dn.ip_address,
		       pa.rqlite_http_port, pa.rqlite_raft_port,
		       pa.olric_http_port, pa.olric_memberlist_port, pa.gateway_http_port
		  FROM namespace_port_allocations pa
		  JOIN dns_nodes dn ON pa.node_id = dn.id
		  JOIN namespace_cluster_nodes cn
		    ON cn.namespace_cluster_id = pa.namespace_cluster_id AND cn.node_id = pa.node_id
		 WHERE pa.namespace_cluster_id = ? AND dn.status = 'active'`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("read surviving members: %w", err)
	}
	return out, nil
}

// pendingCleanupMaxAttempts bounds how long a stop is retried before it needs a
// human.
//
// It is not a give-up: the row stays, so an operator can still see the orphan.
// It stops the reconciler logging the same failure every minute for ever once
// it is clear the node is not going to answer — by which point the node is
// almost certainly on its way to being pruned anyway.
const pendingCleanupMaxAttempts = 30

// recordPendingCleanup remembers a remote stop that failed, so it is retried.
func (cm *ClusterManager) recordPendingCleanup(ctx context.Context, namespace, nodeID, nodeIP, action string, cause error) {
	_, err := cm.db.Exec(client.WithInternalAuth(ctx), `
		INSERT INTO namespace_pending_cleanup (namespace, node_id, node_ip, action, attempts, last_error, last_attempt_at)
		VALUES (?, ?, ?, ?, 1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(namespace, node_id, action) DO UPDATE SET
		  attempts = namespace_pending_cleanup.attempts + 1,
		  node_ip = excluded.node_ip,
		  last_error = excluded.last_error,
		  last_attempt_at = CURRENT_TIMESTAMP`,
		namespace, nodeID, nodeIP, action, cause.Error())
	if err != nil {
		cm.logger.Error("Could not record a failed remote stop for retry; the remote unit may keep holding its ports",
			zap.String("namespace", namespace),
			zap.String("node_id", nodeID),
			zap.String("action", action),
			zap.Error(err))
	}
}

// clearPendingCleanup forgets a stop that has now succeeded.
func (cm *ClusterManager) clearPendingCleanup(ctx context.Context, namespace, nodeID, action string) {
	if _, err := cm.db.Exec(client.WithInternalAuth(ctx),
		`DELETE FROM namespace_pending_cleanup WHERE namespace = ? AND node_id = ? AND action = ?`,
		namespace, nodeID, action); err != nil {
		cm.logger.Warn("Could not clear a completed cleanup record", zap.Error(err))
	}
}

// replayPendingCleanups retries remote stops that previously failed.
func (cm *ClusterManager) replayPendingCleanups(ctx context.Context) error {
	var rows []struct {
		Namespace string `db:"namespace"`
		NodeID    string `db:"node_id"`
		NodeIP    string `db:"node_ip"`
		Action    string `db:"action"`
		Attempts  int    `db:"attempts"`
	}
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows,
		`SELECT namespace, node_id, node_ip, action, attempts FROM namespace_pending_cleanup
		  WHERE attempts < ? ORDER BY created_at LIMIT 50`, pendingCleanupMaxAttempts); err != nil {
		return fmt.Errorf("read pending cleanups: %w", err)
	}

	for _, r := range rows {
		// sendStopRequest records or clears the row itself, so the outcome is
		// persisted whichever way it goes.
		if err := cm.sendStopRequest(ctx, r.NodeIP, r.Action, r.Namespace, r.NodeID); err == nil {
			cm.logger.Info("Completed a remote stop that had previously failed",
				zap.String("namespace", r.Namespace),
				zap.String("node_id", r.NodeID),
				zap.String("action", r.Action),
				zap.Int("previous_attempts", r.Attempts))
		}
	}
	return nil
}

// serviceRunning reports whether a unit is active, and whether the answer is
// KNOWN.
//
// `IsServiceActive`'s error was discarded at every call site, so a transient
// systemctl or D-Bus failure read as "the service is down" and triggered a
// re-spawn of something that was running perfectly well. Re-spawning is not
// free: it stops the unit first, so a momentary inability to ask the question
// caused the outage it was checking for.
func serviceRunning(cm *ClusterManager, namespace string, serviceType systemd.ServiceType) (running, known bool) {
	active, err := cm.systemdSpawner.systemdMgr.IsServiceActive(namespace, serviceType)
	if err != nil {
		cm.logger.Warn("Cannot read a unit's state; treating it as unknown rather than inactive",
			zap.String("namespace", namespace),
			zap.String("service", string(serviceType)),
			zap.Error(err))
		return false, false
	}
	return active, true
}
