package namespace

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// pendingCleanupMaxAttempts is how many failed attempts a cleanup gets at the
// reconciler's every-sweep pace before it is exhausted.
//
// An exhausted cleanup is not given up on and not forgotten. Its row stays, so
// it keeps refusing a create of the namespace's name (the node may still hold
// the old namespace's state) and keeps the port blocks of the node reserved; it
// is retried every pendingCleanupExhaustedRetry instead of every sweep, and
// each failed retry is logged at Error. It leaves the table only when the node
// confirms the teardown, or when the namespace is created again on the node by
// another path (pendingCleanupSuperseded).
const pendingCleanupMaxAttempts = 30

// pendingCleanupExhaustedRetry is the SQLite datetime modifier that says how
// long after its last attempt an exhausted cleanup is tried again.
const pendingCleanupExhaustedRetry = "-1 hours"

// pendingCleanupBatch is how many pending cleanups one sweep replays.
const pendingCleanupBatch = 50

// pendingCleanupClaimLease is the SQLite datetime modifier for how long a
// gateway's claim on a pending cleanup holds. It has to outlast one replay (the
// spawn request's 60 second timeout and the bookkeeping around it); a gateway
// that dies holding a claim blocks the row for this long and no longer.
const pendingCleanupClaimLease = "+5 minutes"

// cleanupScope says what a remote request was owed for.
//
// ClusterID is the incarnation of the namespace the request was made for. A
// teardown deletes data, and the namespace may have been deleted and created
// again, on the same node, before the node accepts the replay: the replay
// must not tear down the new one. PurgeData asks a namespace teardown to also
// remove the namespace's tenant data (RemoveTenantData): it is the delete of
// the namespace, not a rollback or a move.
type cleanupScope struct {
	ClusterID string
	PurgeData bool
}

// isDestructiveCleanup reports whether the action deletes state, so that a
// replay of it has to be checked against the registry first. The stop-*
// actions only stop a unit and are safe to repeat.
func isDestructiveCleanup(action string) bool {
	switch action {
	case teardownAction, teardownSFUAction, teardownTURNAction:
		return true
	}
	return false
}

// errCleanupNotRecorded marks a failed stop or teardown whose retry record could
// not be written. Nothing owes the node the cleanup then, so a caller must not
// treat the failure as "recorded for replay".
var errCleanupNotRecorded = errors.New("the cleanup owed could not be recorded for replay")

// recordPendingCleanup remembers a stop or teardown that failed, so it is
// retried. It returns errCleanupNotRecorded, wrapping the cause, when the record
// could not be written.
func (cm *ClusterManager) recordPendingCleanup(ctx context.Context, namespace, nodeID, nodeIP, action string, scope cleanupScope, cause error) error {
	_, err := cm.db.Exec(client.WithInternalAuth(ctx), `
		INSERT INTO namespace_pending_cleanup (namespace, node_id, node_ip, action, cluster_id, purge_data, attempts, last_error, last_attempt_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(namespace, node_id, action) DO UPDATE SET
		  attempts = namespace_pending_cleanup.attempts + 1,
		  node_ip = excluded.node_ip,
		  cluster_id = CASE WHEN excluded.cluster_id = '' THEN namespace_pending_cleanup.cluster_id ELSE excluded.cluster_id END,
		  purge_data = MAX(namespace_pending_cleanup.purge_data, excluded.purge_data),
		  last_error = excluded.last_error,
		  last_attempt_at = CURRENT_TIMESTAMP`,
		namespace, nodeID, nodeIP, action, scope.ClusterID, boolToInt(scope.PurgeData), cause.Error())
	if err != nil {
		cm.logger.Error("Could not record a failed stop for retry; the unit may keep holding its ports",
			zap.String("namespace", namespace),
			zap.String("node_id", nodeID),
			zap.String("action", action),
			zap.Error(err))
		return fmt.Errorf("%w: %w", errCleanupNotRecorded, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clearPendingCleanup forgets a stop that has now succeeded.
func (cm *ClusterManager) clearPendingCleanup(ctx context.Context, namespace, nodeID, action string) {
	if _, err := cm.db.Exec(client.WithInternalAuth(ctx),
		`DELETE FROM namespace_pending_cleanup WHERE namespace = ? AND node_id = ? AND action = ?`,
		namespace, nodeID, action); err != nil {
		cm.logger.Warn("Could not clear a completed cleanup record", zap.Error(err))
	}
}

// clearPendingCleanupRow forgets the cleanup of one row, only if it is still the
// one that was replayed: a row recorded again for another incarnation of the
// namespace keeps its own.
func (cm *ClusterManager) clearPendingCleanupRow(ctx context.Context, r pendingCleanupRow) error {
	if _, err := cm.db.Exec(client.WithInternalAuth(ctx),
		`DELETE FROM namespace_pending_cleanup WHERE id = ? AND cluster_id = ?`, r.ID, r.ClusterID); err != nil {
		return fmt.Errorf("clear the completed %s of %s on node %s: %w", r.Action, r.Namespace, r.NodeID, err)
	}
	return nil
}

type pendingCleanupRow struct {
	ID        int64  `db:"id"`
	Namespace string `db:"namespace"`
	NodeID    string `db:"node_id"`
	NodeIP    string `db:"node_ip"`
	Action    string `db:"action"`
	ClusterID string `db:"cluster_id"`
	PurgeData int    `db:"purge_data"`
	Attempts  int    `db:"attempts"`
}

const (
	// otherIncarnationAssignedQuery counts the clusters of a namespace, other
	// than the one a cleanup was owed for, that the registry places on a node —
	// by membership or by port allocation, which is written before anything is
	// spawned. A cleanup with no recorded cluster (written before the column
	// existed) treats every cluster as another one.
	otherIncarnationAssignedQuery = `SELECT COUNT(*) AS count FROM namespace_clusters c
		WHERE c.namespace_name = ? AND c.id != ?
		  AND (EXISTS (SELECT 1 FROM namespace_cluster_nodes cn
		                WHERE cn.namespace_cluster_id = c.id AND cn.node_id = ?)
		    OR EXISTS (SELECT 1 FROM namespace_port_allocations pa
		                WHERE pa.namespace_cluster_id = c.id AND pa.node_id = ?))`

	// otherIncarnationWebRTCQuery counts the WebRTC allocations of one service
	// type that another cluster of the namespace holds on a node.
	otherIncarnationWebRTCQuery = `SELECT COUNT(*) AS count FROM webrtc_port_allocations wa
		JOIN namespace_clusters c ON c.id = wa.namespace_cluster_id
		WHERE c.namespace_name = ? AND c.id != ? AND wa.node_id = ? AND wa.service_type = ?`
)

// pendingCleanupSuperseded reports whether a destructive cleanup no longer
// applies because the namespace has been created again on the node: the
// registry places another cluster of the same name there. Sending it would tear
// down the live namespace and delete its data. The teardown of the old
// incarnation is then the node's own orphan handling: its local state carries
// the old cluster id (restoreAssigned).
func (cm *ClusterManager) pendingCleanupSuperseded(ctx context.Context, r pendingCleanupRow) (bool, error) {
	n, err := cm.countRows(ctx, otherIncarnationAssignedQuery, r.Namespace, r.ClusterID, r.NodeID, r.NodeID)
	if err != nil {
		return false, fmt.Errorf("check whether %s was created again on node %s: %w", r.Namespace, r.NodeID, err)
	}
	if n > 0 {
		return true, nil
	}
	var serviceType string
	switch r.Action {
	case teardownSFUAction:
		serviceType = "sfu"
	case teardownTURNAction:
		serviceType = "turn"
	default:
		return false, nil
	}
	n, err = cm.countRows(ctx, otherIncarnationWebRTCQuery, r.Namespace, r.ClusterID, r.NodeID, serviceType)
	if err != nil {
		return false, fmt.Errorf("check whether %s has %s again on node %s: %w", r.Namespace, serviceType, r.NodeID, err)
	}
	return n > 0, nil
}

// countRows runs a COUNT(*) AS count query.
func (cm *ClusterManager) countRows(ctx context.Context, query string, args ...any) (int, error) {
	var rows []struct {
		Count int `db:"count"`
	}
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows, query, args...); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

// replayPendingCleanups retries stops and teardowns that previously failed.
// A cleanup that has used up its attempts (pendingCleanupMaxAttempts) is retried
// only once pendingCleanupExhaustedRetry has passed since its last attempt.
//
// A destructive one is checked against the registry first, and dropped rather
// than sent when the namespace has been created again on its node; when the
// registry cannot answer it is not sent either.
func (cm *ClusterManager) replayPendingCleanups(ctx context.Context) error {
	var rows []pendingCleanupRow
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows,
		`SELECT id, namespace, node_id, node_ip, action, cluster_id, purge_data, attempts FROM namespace_pending_cleanup
		  WHERE (attempts < ? OR last_attempt_at IS NULL OR last_attempt_at <= datetime('now', ?))
		    AND (claimed_until IS NULL OR claimed_until <= datetime('now'))
		  ORDER BY created_at LIMIT ?`, pendingCleanupMaxAttempts, pendingCleanupExhaustedRetry, pendingCleanupBatch); err != nil {
		return fmt.Errorf("read pending cleanups: %w", err)
	}

	var errs []error
	for _, r := range rows {
		if err := cm.replayRow(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// claimPendingCleanup takes the lease on a row, and reports whether this
// gateway got it. The tenant reconciler runs on every node's gateway, so every
// gateway reads the same rows: the claim is an UPDATE that matches for one of
// them only. It matches the attempt count the row was read with, so a gateway
// that read the row before another one replayed and failed it finds the row
// changed and leaves it to the next sweep.
func (cm *ClusterManager) claimPendingCleanup(ctx context.Context, r pendingCleanupRow) (bool, error) {
	res, err := cm.db.Exec(client.WithInternalAuth(ctx), `
		UPDATE namespace_pending_cleanup SET claimed_until = datetime('now', ?)
		 WHERE id = ? AND attempts = ? AND cluster_id = ?
		   AND (claimed_until IS NULL OR claimed_until <= datetime('now'))`,
		pendingCleanupClaimLease, r.ID, r.Attempts, r.ClusterID)
	if err != nil {
		return false, fmt.Errorf("claim the pending %s of %s on node %s: %w", r.Action, r.Namespace, r.NodeID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read the claim of the pending %s of %s on node %s: %w", r.Action, r.Namespace, r.NodeID, err)
	}
	return n == 1, nil
}

// releasePendingClaim gives the lease back so the next sweep can retry at once.
// A failure is only logged: the lease lapses by itself.
func (cm *ClusterManager) releasePendingClaim(ctx context.Context, r pendingCleanupRow) {
	if _, err := cm.db.Exec(client.WithInternalAuth(context.WithoutCancel(ctx)),
		`UPDATE namespace_pending_cleanup SET claimed_until = NULL WHERE id = ?`, r.ID); err != nil {
		cm.logger.Warn("Could not release the claim on a pending cleanup; it lapses by itself",
			zap.String("namespace", r.Namespace), zap.String("node_id", r.NodeID), zap.Error(err))
	}
}

// nodeRemoved reports whether the registry has no record of the node at all: a
// node deleted from dns_nodes was removed from the cluster, and its units went
// with it. A node that is merely offline or inactive still has its row, and its
// cleanup stays owed.
func (cm *ClusterManager) nodeRemoved(ctx context.Context, nodeID string) (bool, error) {
	if nodeID == cm.localNodeID {
		return false, nil
	}
	n, err := cm.countRows(ctx, `SELECT COUNT(*) AS count FROM dns_nodes WHERE id = ?`, nodeID)
	if err != nil {
		return false, fmt.Errorf("check whether node %s is still registered: %w", nodeID, err)
	}
	return n == 0, nil
}

// settleCleanup ends a cleanup that no longer has to be sent: the allocations it
// kept are freed first, and only then is the row forgotten. The other order
// loses the ports for good when freeing them fails: nothing is left that owes
// them, and the allocators never look at a block whose cluster is gone.
func (cm *ClusterManager) settleCleanup(ctx context.Context, r pendingCleanupRow) error {
	if err := cm.releaseAllocationsOfCompletedTeardown(ctx, r); err != nil {
		return err
	}
	return cm.clearPendingCleanupRow(ctx, r)
}

// replayRow replays one pending cleanup. The error it returns is a failure of
// the registry's side (the claim, the supersession check, freeing allocations);
// a cleanup the node did not confirm is recorded again and logged, not
// returned.
func (cm *ClusterManager) replayRow(ctx context.Context, r pendingCleanupRow) error {
	claimed, err := cm.claimPendingCleanup(ctx, r)
	if err != nil || !claimed {
		return err
	}
	defer cm.releasePendingClaim(ctx, r)

	removed, err := cm.nodeRemoved(ctx, r.NodeID)
	if err != nil {
		return err
	}
	if removed {
		cm.logger.Warn("Dropping a pending cleanup: its node is no longer registered, so it was removed from the cluster and its units went with it",
			zap.String("namespace", r.Namespace),
			zap.String("node_id", r.NodeID),
			zap.String("action", r.Action),
			zap.String("cluster_id", r.ClusterID))
		return cm.settleCleanup(ctx, r)
	}
	if isDestructiveCleanup(r.Action) {
		superseded, err := cm.pendingCleanupSuperseded(ctx, r)
		if err != nil {
			return err
		}
		if superseded {
			cm.logger.Warn("Dropping a pending teardown: the namespace has been created again on that node, and the teardown would delete the new one",
				zap.String("namespace", r.Namespace),
				zap.String("node_id", r.NodeID),
				zap.String("action", r.Action),
				zap.String("cluster_id", r.ClusterID))
			// The old incarnation's blocks are keyed by its own cluster id, so
			// freeing them leaves the new one's alone.
			return cm.settleCleanup(ctx, r)
		}
	}
	// replayCleanup records a failure itself and leaves the row of a success in
	// place, for settleCleanup to clear once the allocations are freed.
	if err := cm.replayCleanup(ctx, r); err != nil {
		cm.logExhaustedCleanup(r, err)
		return nil
	}
	cm.logger.Info("Completed a stop that had previously failed",
		zap.String("namespace", r.Namespace),
		zap.String("node_id", r.NodeID),
		zap.String("action", r.Action),
		zap.Int("previous_attempts", r.Attempts))
	return cm.settleCleanup(ctx, r)
}

// replayCleanup carries out one owed cleanup on its node and records the
// outcome. A teardown owed by this node is run here; any other goes through the
// node's spawn endpoint, at the overlay address recorded with it or, when none
// was known then, the one the node has registered since.
func (cm *ClusterManager) replayCleanup(ctx context.Context, r pendingCleanupRow) error {
	scope := cleanupScope{ClusterID: r.ClusterID, PurgeData: r.PurgeData != 0}
	if r.NodeID == cm.localNodeID && r.Action == teardownAction {
		return cm.teardownLocalKeepingRow(ctx, r.NodeID, r.NodeIP, r.Namespace, scope)
	}
	ip := r.NodeIP
	if ip == "" {
		resolved, err := cm.nodeInternalIP(r.NodeID)
		if err != nil {
			cause := fmt.Errorf("cannot replay %s of %s: %w", r.Action, r.Namespace, err)
			if rerr := cm.recordPendingCleanup(ctx, r.Namespace, r.NodeID, "", r.Action, scope, cause); rerr != nil {
				return fmt.Errorf("%w; %w", cause, rerr)
			}
			return cause
		}
		ip = resolved
	}
	return cm.sendStopKeepingRow(ctx, ip, r.Action, r.Namespace, r.NodeID, scope)
}

// logExhaustedCleanup says, at Error, that a cleanup has failed as many times as
// pendingCleanupMaxAttempts allows (r.Attempts is the count before this
// failure). It is logged on every later retry too: an owed teardown that cannot
// be carried out keeps the name and the ports reserved until someone looks.
func (cm *ClusterManager) logExhaustedCleanup(r pendingCleanupRow, cause error) {
	if r.Attempts+1 < pendingCleanupMaxAttempts {
		return
	}
	cm.logger.Error("A teardown owed to a node keeps failing: the namespace's name and the node's ports stay reserved until it succeeds",
		zap.String("namespace", r.Namespace),
		zap.String("node_id", r.NodeID),
		zap.String("action", r.Action),
		zap.Int("attempts", r.Attempts+1),
		zap.Error(cause))
}

// releaseAllocationsOfCompletedTeardown frees the allocations a teardown that
// failed first kept: the unit holding those ports is now gone. That is the
// WebRTC allocations of the services it stopped (releaseWebRTCPorts), and, for
// the teardown of the whole namespace, the node's core port block. A cleanup
// that carries no cluster id predates the column and has nothing to name the
// rows by.
func (cm *ClusterManager) releaseAllocationsOfCompletedTeardown(ctx context.Context, r pendingCleanupRow) error {
	if r.ClusterID == "" {
		return nil
	}
	if err := releaseOwedAllocations(ctx, cm.db, r.ClusterID, r.NodeID, r.Action); err != nil {
		return fmt.Errorf("release the ports of %s on node %s after its cleanup was settled: %w", r.Namespace, r.NodeID, err)
	}
	return nil
}

// withdrawPendingTeardowns deletes the destructive cleanups still owed for a
// namespace on a node, as the namespace is given that node: they belong to an
// incarnation that is gone. The allocations that incarnation kept for the node
// are freed first (they are keyed by its cluster id, so clusterID's own are not
// touched): with the row gone nothing would owe them.
func withdrawPendingTeardowns(ctx context.Context, db rqlite.Client, clusterID, nodeID string, actions ...string) error {
	ctx = client.WithInternalAuth(ctx)
	for _, action := range actions {
		var owed []struct {
			ClusterID string `db:"cluster_id"`
		}
		if err := db.Query(ctx, &owed, `
			SELECT cluster_id FROM namespace_pending_cleanup
			 WHERE node_id = ? AND action = ?
			   AND namespace = (SELECT namespace_name FROM namespace_clusters WHERE id = ?)`,
			nodeID, action, clusterID); err != nil {
			return fmt.Errorf("read the pending %s of node %s: %w", action, nodeID, err)
		}
		for _, o := range owed {
			if o.ClusterID == "" || o.ClusterID == clusterID {
				continue
			}
			if err := releaseOwedAllocations(ctx, db, o.ClusterID, nodeID, action); err != nil {
				return fmt.Errorf("release the ports of cluster %s on node %s before withdrawing its pending %s: %w", o.ClusterID, nodeID, action, err)
			}
		}
		if _, err := db.Exec(ctx, `
			DELETE FROM namespace_pending_cleanup
			 WHERE node_id = ? AND action = ?
			   AND namespace = (SELECT namespace_name FROM namespace_clusters WHERE id = ?)`,
			nodeID, action, clusterID); err != nil {
			return fmt.Errorf("withdraw the pending %s of node %s: %w", action, nodeID, err)
		}
	}
	return nil
}
