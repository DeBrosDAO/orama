package namespace

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

const (
	// Staleness is judged in SQL, on the registry's clock, never by comparing
	// another node's timestamp with this node's clock: provisioned_at is written
	// with CURRENT_TIMESTAMP (insertCluster), so both sides of the comparison
	// come from the registry. Argument: a "-N seconds" datetime modifier.
	//
	// datetime() returns NULL for a value it cannot parse, which never satisfies
	// the "<" test: an unreadable provisioned_at is reported, not treated as
	// ancient and used to fail a cluster that may be live.
	staleProvisioningPredicate = `status = 'provisioning' AND datetime(provisioned_at) < datetime('now', ?)`

	staleClustersQuery = `SELECT id, namespace_id, namespace_name, provisioned_at
		FROM namespace_clusters WHERE ` + staleProvisioningPredicate

	unreadableProvisionedAtQuery = `SELECT id, namespace_name
		FROM namespace_clusters
		WHERE status = 'provisioning' AND datetime(provisioned_at) IS NULL`

	// failStaleClusterSQL flips a cluster to failed only while it is still
	// 'provisioning' and still past the threshold. The guard is the arbiter
	// between nodes: every node runs the sweep, exactly one UPDATE affects a
	// row, and only that node releases the cluster's ports.
	failStaleClusterSQL = `UPDATE namespace_clusters SET status = 'failed', error_message = ?
		WHERE id = ? AND ` + staleProvisioningPredicate

	// staleClusterNodesQuery lists the active nodes holding a port block of the
	// cluster: the nodes whose services must be stopped before the block is
	// released. A node that is gone has nothing to stop. A node marked inactive
	// is left out on purpose: a permanently departed node would otherwise pin
	// every stale cluster it held in provisioning for ever.
	//
	// The inactive ones are not asked: a node silent for two minutes may be
	// restarting or partitioned with its units still running, so its teardown is
	// owed instead (staleClusterOwedNodesQuery) and its block stays reserved.
	//
	// Only the overlay address is used. The stop is a signed request in plain
	// HTTP; sending it to a public address would carry it off the WireGuard
	// mesh, so a node with none cannot be stopped and keeps its ports.
	staleClusterNodesQuery = `SELECT DISTINCT pa.node_id AS node_id,
		COALESCE(dn.internal_ip, '') AS internal_ip
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ? AND dn.status = 'active'`

	// staleClusterOwedNodesQuery lists the registered nodes that hold a port
	// block of the cluster and are not active: they cannot confirm a stop.
	staleClusterOwedNodesQuery = `SELECT DISTINCT pa.node_id AS node_id,
		COALESCE(dn.internal_ip, '') AS internal_ip
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ? AND dn.status != 'active'`
)

type staleClusterNode struct {
	NodeID     string `db:"node_id"`
	InternalIP string `db:"internal_ip"`
}

// failStaleProvisioning fails clusters whose provisioning goroutine is gone.
// Provisioning runs on the one node that took the create request, bounded by
// provisioningTimeout; if that process restarted or could not record its
// failure, the cluster would stay 'provisioning' forever and clients would poll
// until they gave up. A cluster older than staleProvisioningAfter cannot still
// be running anywhere, so no node-to-node coordination is needed beyond the
// guarded UPDATE, and nodes on an older build simply do not sweep.
func (cm *ClusterManager) failStaleProvisioning(ctx context.Context) error {
	modifier := fmt.Sprintf("-%d seconds", int(staleProvisioningAfter.Seconds()))

	cm.reportUnreadableProvisionedAt(ctx)

	var clusters []NamespaceCluster
	if err := cm.db.Query(ctx, &clusters, staleClustersQuery, modifier); err != nil {
		return fmt.Errorf("failed to list clusters stuck in provisioning: %w", err)
	}

	var errs []error
	for i := range clusters {
		c := &clusters[i]
		if cm.isProvisioningLocally(c.NamespaceName) {
			continue
		}
		if err := cm.failStaleCluster(ctx, c, modifier); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reportUnreadableProvisionedAt logs provisioning clusters whose age cannot be
// judged. They are left alone: failing a cluster on a guess could tear down a
// live one.
func (cm *ClusterManager) reportUnreadableProvisionedAt(ctx context.Context) {
	var rows []struct {
		ID            string `db:"id"`
		NamespaceName string `db:"namespace_name"`
	}
	if err := cm.db.Query(ctx, &rows, unreadableProvisionedAtQuery); err != nil {
		cm.logger.Warn("Could not check for provisioning clusters with an unreadable provisioned_at", zap.Error(err))
		return
	}
	for _, r := range rows {
		cm.logger.Error("Cluster in provisioning has a NULL or unparseable provisioned_at; the stale sweep cannot judge its age and skips it",
			zap.String("cluster_id", r.ID), zap.String("namespace", r.NamespaceName))
	}
}

func (cm *ClusterManager) isProvisioningLocally(namespaceName string) bool {
	cm.provisioningMu.RLock()
	defer cm.provisioningMu.RUnlock()
	return cm.provisioning[namespaceName]
}

// failStaleCluster fails one abandoned cluster. Order matters:
//
//  1. Stop the services provisioning may have started, on every node holding a
//     port block. Stopping is idempotent, so every node may do it. If any stop
//     fails the cluster stays 'provisioning' and the next sweep tries again; a
//     failed remote stop is also recorded for replay (sendStopRequest). Ports
//     are never released under a process that may still be bound to them, or
//     the next namespace given the same block collides with it.
//  2. The guarded UPDATE; exactly one node wins it.
//  3. The winner releases the ports of the nodes that confirmed (every node
//     but those owed a teardown, releaseAllocationsExceptOwed) and withdraws
//     DNS and membership.
func (cm *ClusterManager) failStaleCluster(ctx context.Context, c *NamespaceCluster, modifier string) error {
	if err := cm.stopStaleClusterServices(ctx, c); err != nil {
		return fmt.Errorf("stale provisioning cluster %s (%s) keeps its ports until its services are stopped: %w", c.ID, c.NamespaceName, err)
	}

	res, err := cm.db.Exec(ctx, failStaleClusterSQL, staleProvisioningReason, c.ID, modifier)
	if err != nil {
		return fmt.Errorf("failed to fail stale provisioning cluster %s (%s): %w", c.ID, c.NamespaceName, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("failed to read result of failing stale cluster %s (%s): %w", c.ID, c.NamespaceName, err)
	} else if n == 0 {
		return nil
	}

	cm.logger.Error("Failed a cluster stuck in provisioning past its timeout",
		zap.String("namespace", c.NamespaceName),
		zap.String("cluster_id", c.ID),
		zap.Time("provisioned_at", c.ProvisionedAt))

	var errs []error
	if err := cm.releaseAllocationsExceptOwed(ctx, c.ID, c.NamespaceName); err != nil {
		errs = append(errs, err)
	}
	if err := cm.removeClusterServingRecords(ctx, c); err != nil {
		errs = append(errs, err)
	}
	cm.logEvent(ctx, c.ID, EventClusterFailed, "", staleProvisioningReason, nil)
	if len(errs) > 0 {
		return fmt.Errorf("stale cluster %s (%s) failed but its cleanup is incomplete: %w", c.ID, c.NamespaceName, errors.Join(errs...))
	}
	return nil
}

// stopStaleClusterServices tears the cluster down on every active node holding
// one of its port blocks, attempting all of them and joining the failures. An
// inactive node cannot confirm it: its teardown is recorded as owed.
// Teardown, not stop: the stale cluster is about to be marked failed, and a
// failed cluster's units must not be left enabled for the next upgrade to start.
func (cm *ClusterManager) stopStaleClusterServices(ctx context.Context, c *NamespaceCluster) error {
	var nodes []staleClusterNode
	if err := cm.db.Query(ctx, &nodes, staleClusterNodesQuery, c.ID); err != nil {
		return fmt.Errorf("failed to list the nodes of cluster %s: %w", c.ID, err)
	}
	scope := cleanupScope{ClusterID: c.ID}
	errs := []error{cm.teardownNamespaceOnNodes(ctx, nodes, c.NamespaceName, scope)}

	var inactive []staleClusterNode
	if err := cm.db.Query(ctx, &inactive, staleClusterOwedNodesQuery, c.ID); err != nil {
		return errors.Join(append(errs, fmt.Errorf("failed to list the inactive nodes of cluster %s: %w", c.ID, err))...)
	}
	for _, node := range inactive {
		cause := fmt.Errorf("node %s is not active, so it cannot confirm the teardown of %s", node.NodeID, c.NamespaceName)
		if err := cm.recordPendingCleanup(ctx, c.NamespaceName, node.NodeID, node.InternalIP, teardownAction, scope, cause); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
