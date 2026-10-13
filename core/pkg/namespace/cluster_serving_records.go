package namespace

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// deleteClusterMembersSQL removes a cluster's node membership rows. Each node's
// 30s DNS sweep re-advertises itself for every cluster it is a running gateway
// member of, so the membership has to go before the DNS rows do.
const deleteClusterMembersSQL = `DELETE FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`

// removeClusterServingRecords withdraws everything that makes a namespace
// cluster reachable: its node membership and its DNS records (gateway host,
// wildcard, TURN and stealth TURN).
//
// A cluster that is abandoned (a failed provision) or torn down must go through
// this. Before it existed, a failed provision only flipped the cluster row to
// 'failed' and CheckNamespaceCluster later deleted that row outright, so the DNS
// rows stayed behind with nothing left that knew to remove them: a later
// DeprovisionCluster finds no cluster and returns without touching DNS.
//
// Membership is deleted first, so no node can re-add a record between the two
// statements. Every step is attempted and the failures are joined: a partial
// cleanup must not hide behind its first error.
func (cm *ClusterManager) removeClusterServingRecords(ctx context.Context, cluster *NamespaceCluster) error {
	var errs []error
	if _, err := cm.db.Exec(ctx, deleteClusterMembersSQL, cluster.ID); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete cluster %s node membership: %w", cluster.ID, err))
	}
	if err := cm.dnsManager.DeleteNamespaceRecords(ctx, cluster.NamespaceName); err != nil {
		errs = append(errs, err)
	}
	if err := cm.dnsManager.DeleteTURNRecords(ctx, cluster.NamespaceName); err != nil {
		errs = append(errs, err)
	}
	if err := cm.dnsManager.DeleteStealthTURNRecords(ctx, cluster.NamespaceName); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// withdrawFailedCluster is removeClusterServingRecords for a provision that
// failed after its DNS records were written, where the failure is only logged:
// the cluster row stays 'failed' and the caller reports the original error.
func (cm *ClusterManager) withdrawFailedCluster(ctx context.Context, cluster *NamespaceCluster) {
	if err := cm.removeClusterServingRecords(ctx, cluster); err != nil {
		cm.logger.Error("Could not withdraw the DNS records of a failed cluster; the node-side orphan purge removes them once the namespace is deleted",
			zap.String("cluster_id", cluster.ID), zap.String("namespace", cluster.NamespaceName), zap.Error(err))
	}
}
