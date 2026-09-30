package namespace

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const (
	provisioningClustersQuery = `SELECT id, namespace_id, namespace_name, provisioned_at
		FROM namespace_clusters WHERE status = 'provisioning'`

	// failStaleClusterSQL flips a cluster to failed only while it is still
	// 'provisioning'. The status guard is the arbiter between nodes: every node
	// runs the sweep, exactly one UPDATE affects a row, and only that node
	// rolls the cluster back.
	failStaleClusterSQL = `UPDATE namespace_clusters SET status = 'failed', error_message = ?
		WHERE id = ? AND status = 'provisioning'`
)

// failStaleProvisioning fails clusters whose provisioning goroutine is gone.
// Provisioning runs on the one node that took the create request, bounded by
// provisioningTimeout; if that process restarted or could not record its
// failure, the cluster would stay 'provisioning' forever and clients would poll
// until they gave up. A cluster older than the timeout plus a margin cannot
// still be running anywhere, so no node-to-node coordination is needed beyond
// the guarded UPDATE, and nodes on an older build simply do not sweep.
func (cm *ClusterManager) failStaleProvisioning(ctx context.Context) error {
	var clusters []NamespaceCluster
	if err := cm.db.Query(ctx, &clusters, provisioningClustersQuery); err != nil {
		return fmt.Errorf("failed to list clusters in provisioning: %w", err)
	}

	cutoff := time.Now().Add(-(provisioningTimeout + staleProvisioningMargin))
	var firstErr error
	for i := range clusters {
		c := &clusters[i]
		if !c.ProvisionedAt.Before(cutoff) || cm.isProvisioningLocally(c.NamespaceName) {
			continue
		}
		if err := cm.failStaleCluster(ctx, c); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (cm *ClusterManager) isProvisioningLocally(namespaceName string) bool {
	cm.provisioningMu.RLock()
	defer cm.provisioningMu.RUnlock()
	return cm.provisioning[namespaceName]
}

// failStaleCluster fails one abandoned cluster and, if this node won the
// guarded update, releases what its provisioning had claimed.
func (cm *ClusterManager) failStaleCluster(ctx context.Context, c *NamespaceCluster) error {
	res, err := cm.db.Exec(ctx, failStaleClusterSQL, staleProvisioningReason, c.ID)
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
	if err := cm.portAllocator.DeallocateAllPortBlocks(ctx, c.ID); err != nil {
		errs = append(errs, err)
	}
	if err := cm.removeClusterServingRecords(ctx, c); err != nil {
		errs = append(errs, err)
	}
	cm.logEvent(ctx, c.ID, EventClusterFailed, "", staleProvisioningReason, nil)
	if len(errs) > 0 {
		return fmt.Errorf("stale cluster %s (%s) failed but its cleanup is incomplete: %v", c.ID, c.NamespaceName, errs)
	}
	return nil
}
