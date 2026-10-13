package namespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const (
	// staleDeprovisioningAfter is how long a cluster may sit in 'deprovisioning'
	// without its teardown refreshing its stamp before the teardown is declared
	// abandoned. A live teardown is over by DeprovisionTimeout and refreshes the
	// stamp between its steps; the margin keeps the threshold strictly beyond it,
	// so a teardown that is merely finishing its last write is never taken over.
	// 12 minutes.
	staleDeprovisioningAfter = DeprovisionTimeout + 2*time.Minute

	// maxConcurrentResumes bounds the abandoned teardowns one node carries on at
	// once. Each is a fan-out of stop requests to every member of its cluster.
	maxConcurrentResumes = 2

	// deprovisionFreshPredicate is "a teardown owns this cluster": it is
	// 'deprovisioning' and its stamp (registry clock, CURRENT_TIMESTAMP) is
	// within the window. An unparseable stamp is neither fresh nor stale: it never
	// satisfies "<" below, so it is left alone, and it does not satisfy this.
	deprovisionFreshPredicate = `status = 'deprovisioning' AND datetime(deprovisioning_at) >= datetime('now', ?)`

	// deprovisionStalePredicate is the opposite: stamped, and not within the
	// window. A cluster with no stamp is not stale: it was marked by a release
	// that did not stamp, whose delete may still be running (a rolling upgrade),
	// so it is stamped now (stampUnstampedDeprovisioning) and judged after a
	// whole window.
	deprovisionStalePredicate = `status = 'deprovisioning' AND datetime(deprovisioning_at) < datetime('now', ?)`

	staleDeprovisioningQuery = `SELECT id, namespace_id, namespace_name FROM namespace_clusters WHERE ` + deprovisionStalePredicate

	// claimStaleDeprovisioningSQL takes the cluster over: the guarded UPDATE is
	// the arbiter between nodes, as for stale provisioning. Exactly one node's
	// statement affects the row, and it restarts the window for as long as the
	// resumed teardown runs.
	claimStaleDeprovisioningSQL = `UPDATE namespace_clusters SET deprovisioning_at = CURRENT_TIMESTAMP WHERE id = ? AND ` + deprovisionStalePredicate

	stampUnstampedDeprovisioningSQL = `UPDATE namespace_clusters SET deprovisioning_at = CURRENT_TIMESTAMP WHERE status = 'deprovisioning' AND deprovisioning_at IS NULL`

	// beginDeprovisionSQL marks a namespace's cluster as being torn down by the
	// caller, unless a teardown already owns it.
	beginDeprovisionSQL = `UPDATE namespace_clusters SET status = 'deprovisioning', error_message = '', deprovisioning_at = CURRENT_TIMESTAMP
		WHERE namespace_id = ? AND NOT COALESCE(` + deprovisionFreshPredicate + `, 0)`

	releaseDeprovisionSQL = `UPDATE namespace_clusters SET deprovisioning_at = datetime('now', '-1 day') WHERE namespace_id = ? AND status = 'deprovisioning'`

	clusterOfNamespaceQuery = `SELECT COUNT(*) AS count FROM namespace_clusters WHERE namespace_id = ?`
)

// ErrDeprovisionInProgress is what BeginDeprovision returns for a namespace whose
// cluster is already owned by a teardown, on this gateway or another.
var ErrDeprovisionInProgress = errors.New("a teardown of this namespace's cluster is already running")

func deprovisionWindow() string {
	return fmt.Sprintf("-%d seconds", int(staleDeprovisioningAfter.Seconds()))
}

// BeginDeprovision is the registry-level claim a delete takes before it tears a
// namespace's cluster down: the cluster is marked 'deprovisioning' and stamped,
// unless a teardown already owns it (marked and stamped within the window), which
// is ErrDeprovisionInProgress. It is one guarded UPDATE, so two deletes on two
// gateways, or a delete and a reconciler's resume, cannot both pass it. A
// namespace with no cluster has nothing to claim and returns nil.
func BeginDeprovision(ctx context.Context, db rqlite.Client, namespaceID int64) error {
	res, err := db.Exec(ctx, beginDeprovisionSQL, namespaceID, deprovisionWindow())
	if err != nil {
		return fmt.Errorf("failed to claim the teardown of namespace %d: %w", namespaceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read the claim of the teardown of namespace %d: %w", namespaceID, err)
	}
	if n > 0 {
		return nil
	}
	var counts []struct {
		Count int `db:"count"`
	}
	if err := db.Query(ctx, &counts, clusterOfNamespaceQuery, namespaceID); err != nil {
		return fmt.Errorf("failed to check the cluster of namespace %d: %w", namespaceID, err)
	}
	if len(counts) > 0 && counts[0].Count > 0 {
		return ErrDeprovisionInProgress
	}
	return nil
}

// ReleaseDeprovision gives the claim up after a teardown failed and left the
// cluster in place: the stamp is aged out, so a retry of the delete is not
// refused for the rest of the window and the reconciler may resume it.
func ReleaseDeprovision(ctx context.Context, db rqlite.Client, namespaceID int64) error {
	if _, err := db.Exec(ctx, releaseDeprovisionSQL, namespaceID); err != nil {
		return fmt.Errorf("failed to release the teardown claim of namespace %d: %w", namespaceID, err)
	}
	return nil
}

// refreshDeprovisionLease restarts the window of the teardown that owns the
// cluster, between its steps, so that one that is slow but alive is never taken
// over. A failed refresh is logged: the step that follows needs the registry
// itself and reports its own failure.
func (cm *ClusterManager) refreshDeprovisionLease(ctx context.Context, clusterID string) {
	if _, err := cm.db.Exec(ctx,
		`UPDATE namespace_clusters SET deprovisioning_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'deprovisioning'`, clusterID); err != nil {
		cm.logger.Warn("Could not refresh the teardown's claim on its cluster; it may be taken over as abandoned",
			zap.String("cluster_id", clusterID), zap.Error(err))
	}
}

// resumeStaleDeprovisioning finishes the teardown of clusters whose delete
// stopped part way.
//
// A delete runs on the one node that took the request. If that node restarts,
// loses the registry or is cut off, the cluster stays 'deprovisioning' with its
// node rows 'running' and nothing carrying the teardown on; no other sweep
// looks at such a cluster (the restore and drift sweeps read ready and degraded
// ones only, and the orphan sweep reads the units on a node). Every node runs
// this; the guarded claim lets one resume each cluster.
//
// The sweep only claims. The resume itself runs off the sweep's goroutine (a
// teardown of a cluster of unreachable nodes takes minutes, and the sweep also
// restores services and replays cleanups), at most maxConcurrentResumes at a
// time and once per cluster.
//
// DeprovisionCluster is what resumes it, so it is the same teardown: units
// stopped and disabled on every active member, the ones that could not be
// reached recorded for replay, ports freed, DNS and membership withdrawn, the
// cluster row deleted. The namespace's own row, deployments and grants belong to
// the delete request; they remain until the owner (or an operator) repeats the
// delete, which finds no cluster and finishes them.
func (cm *ClusterManager) resumeStaleDeprovisioning(ctx context.Context) error {
	if _, err := cm.db.Exec(ctx, stampUnstampedDeprovisioningSQL); err != nil {
		return fmt.Errorf("failed to stamp clusters left in deprovisioning by a release that did not: %w", err)
	}
	window := deprovisionWindow()

	var clusters []NamespaceCluster
	if err := cm.db.Query(ctx, &clusters, staleDeprovisioningQuery, window); err != nil {
		return fmt.Errorf("failed to list clusters stuck in deprovisioning: %w", err)
	}

	var errs []error
	for i := range clusters {
		if err := cm.startResume(ctx, &clusters[i], window); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reserveResume takes a resume slot for the cluster, or reports that none is
// free or that this process already resumes it.
func (cm *ClusterManager) reserveResume(clusterID string) bool {
	cm.resumeMu.Lock()
	defer cm.resumeMu.Unlock()
	if cm.resuming == nil {
		cm.resuming = make(map[string]bool)
	}
	if cm.resuming[clusterID] || len(cm.resuming) >= maxConcurrentResumes {
		return false
	}
	cm.resuming[clusterID] = true
	return true
}

func (cm *ClusterManager) releaseResume(clusterID string) {
	cm.resumeMu.Lock()
	defer cm.resumeMu.Unlock()
	delete(cm.resuming, clusterID)
}

// startResume claims the cluster and carries its teardown on in a goroutine. The
// slot is taken before the claim, so a cluster this node has no room for stays
// stale for the next sweep, or another node.
func (cm *ClusterManager) startResume(ctx context.Context, c *NamespaceCluster, window string) error {
	if !cm.reserveResume(c.ID) {
		return nil
	}
	res, err := cm.db.Exec(ctx, claimStaleDeprovisioningSQL, c.ID, window)
	if err != nil {
		cm.releaseResume(c.ID)
		return fmt.Errorf("failed to claim cluster %s (%s), stuck in deprovisioning: %w", c.ID, c.NamespaceName, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		cm.releaseResume(c.ID)
		if err != nil {
			return fmt.Errorf("failed to read the claim of cluster %s (%s), stuck in deprovisioning: %w", c.ID, c.NamespaceName, err)
		}
		return nil
	}

	cm.logger.Warn("Resuming the teardown of a namespace cluster whose delete stopped part way",
		zap.String("namespace", c.NamespaceName), zap.String("cluster_id", c.ID))

	cm.resumeWG.Add(1)
	go func() {
		defer cm.resumeWG.Done()
		defer cm.releaseResume(c.ID)
		cm.resumeDeprovisioning(ctx, c)
	}()
	return nil
}

func (cm *ClusterManager) resumeDeprovisioning(ctx context.Context, c *NamespaceCluster) {
	ctx, cancel := context.WithTimeout(ctx, DeprovisionTimeout)
	defer cancel()
	err := cm.DeprovisionCluster(ctx, int64(c.NamespaceID))
	switch {
	case err == nil:
	case errors.Is(err, ErrTeardownIncomplete):
		// The registry side is done; what a node did not confirm is recorded and
		// replayed (ErrTeardownIncomplete).
		cm.logger.Warn("Resumed teardown finished, but a node has not confirmed its part; it stays recorded for replay",
			zap.String("namespace", c.NamespaceName), zap.Error(err))
	default:
		cm.logger.Error("Could not finish the resumed teardown of a namespace cluster; it is resumed again after the window",
			zap.String("namespace", c.NamespaceName), zap.String("cluster_id", c.ID), zap.Error(err))
	}
}
