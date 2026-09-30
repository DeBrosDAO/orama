package namespace

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const (
	// provisioningTimeout bounds one async provisioning run.
	provisioningTimeout = 5 * time.Minute

	// markFailedTimeout bounds the write that records a provisioning failure.
	// It runs on a fresh context so an expired provisioning context cannot
	// swallow the status change, and is long enough to outlast a Raft election.
	markFailedTimeout = 2 * time.Minute

	// staleProvisioningMargin is added to provisioningTimeout before a cluster
	// still in 'provisioning' is declared abandoned, so a run that is merely
	// finishing its last write is never failed from under itself.
	staleProvisioningMargin = 2 * time.Minute

	registryRetryInitialBackoff = 250 * time.Millisecond
	registryRetryMaxBackoff     = 5 * time.Second

	staleProvisioningReason = "provisioning never completed: the node that started it stopped or lost the registry before recording a result"
)

// retryWhileNoLeader runs op and, while it fails because the cluster registry
// has no leader right now (a Raft election, a briefly unreachable leader),
// runs it again with exponential backoff until it succeeds or ctx ends. Any
// other error is returned at once: only the registry being unavailable is a
// state that clears on its own.
func retryWhileNoLeader(ctx context.Context, logger *zap.Logger, what string, op func(context.Context) error) error {
	backoff := registryRetryInitialBackoff
	for attempt := 1; ; attempt++ {
		err := op(ctx)
		if err == nil || rqlite.ClassifyBatchError(err) != rqlite.BatchCodeUnavailable {
			return err
		}
		logger.Warn("Cluster registry has no leader; waiting for one",
			zap.String("operation", what), zap.Int("attempt", attempt), zap.Error(err))

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s: registry still unavailable when %w: %v", what, ctx.Err(), err)
		case <-timer.C:
		}
		if backoff *= 2; backoff > registryRetryMaxBackoff {
			backoff = registryRetryMaxBackoff
		}
	}
}

// markProvisioningFailed records that provisioning of a cluster failed. The
// write uses its own context and waits out a registry election; if it still
// cannot be recorded the cluster stays 'provisioning' until the stale sweep
// fails it, and this says so loudly instead of dropping the error.
func (cm *ClusterManager) markProvisioningFailed(clusterID, namespaceName, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), markFailedTimeout)
	defer cancel()

	err := retryWhileNoLeader(ctx, cm.logger, "mark provisioning failed", func(ctx context.Context) error {
		return cm.updateClusterStatus(ctx, clusterID, ClusterStatusFailed, reason)
	})
	if err != nil {
		cm.logger.Error("Could not record a provisioning failure; the cluster stays in provisioning until the stale sweep fails it",
			zap.String("namespace", namespaceName),
			zap.String("cluster_id", clusterID),
			zap.String("failure", reason),
			zap.Error(err))
	}
}
