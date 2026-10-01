package namespace

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// pendingCleanupMaxAttempts bounds how long a stop is retried before it needs a
// human.
//
// It is not a give-up: the row stays, so an operator can still see the orphan.
// It stops the reconciler logging the same failure every minute for ever once
// it is clear the node is not going to answer — by which point the node is
// almost certainly on its way to being pruned anyway.
const pendingCleanupMaxAttempts = 30

// pendingCleanupBatch is how many pending cleanups one sweep replays.
const pendingCleanupBatch = 50

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

// recordPendingCleanup remembers a remote stop that failed, so it is retried.
func (cm *ClusterManager) recordPendingCleanup(ctx context.Context, namespace, nodeID, nodeIP, action string, scope cleanupScope, cause error) {
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
		cm.logger.Error("Could not record a failed remote stop for retry; the remote unit may keep holding its ports",
			zap.String("namespace", namespace),
			zap.String("node_id", nodeID),
			zap.String("action", action),
			zap.Error(err))
	}
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

type pendingCleanupRow struct {
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

// replayPendingCleanups retries remote stops that previously failed.
//
// A destructive one is checked against the registry first, and dropped rather
// than sent when the namespace has been created again on its node; when the
// registry cannot answer it is not sent either.
func (cm *ClusterManager) replayPendingCleanups(ctx context.Context) error {
	var rows []pendingCleanupRow
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows,
		`SELECT namespace, node_id, node_ip, action, cluster_id, purge_data, attempts FROM namespace_pending_cleanup
		  WHERE attempts < ? ORDER BY created_at LIMIT ?`, pendingCleanupMaxAttempts, pendingCleanupBatch); err != nil {
		return fmt.Errorf("read pending cleanups: %w", err)
	}

	var errs []error
	for _, r := range rows {
		if isDestructiveCleanup(r.Action) {
			superseded, err := cm.pendingCleanupSuperseded(ctx, r)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if superseded {
				cm.logger.Warn("Dropping a pending teardown: the namespace has been created again on that node, and the teardown would delete the new one",
					zap.String("namespace", r.Namespace),
					zap.String("node_id", r.NodeID),
					zap.String("action", r.Action),
					zap.String("cluster_id", r.ClusterID))
				cm.clearPendingCleanup(ctx, r.Namespace, r.NodeID, r.Action)
				continue
			}
		}
		// sendStopRequest records or clears the row itself, so the outcome is
		// persisted whichever way it goes.
		scope := cleanupScope{ClusterID: r.ClusterID, PurgeData: r.PurgeData != 0}
		if err := cm.sendStopRequest(ctx, r.NodeIP, r.Action, r.Namespace, r.NodeID, scope); err == nil {
			cm.logger.Info("Completed a remote stop that had previously failed",
				zap.String("namespace", r.Namespace),
				zap.String("node_id", r.NodeID),
				zap.String("action", r.Action),
				zap.Int("previous_attempts", r.Attempts))
			if rerr := cm.releaseAllocationsOfCompletedTeardown(ctx, r); rerr != nil {
				errs = append(errs, rerr)
			}
		}
	}
	return errors.Join(errs...)
}

// releaseAllocationsOfCompletedTeardown frees the WebRTC allocations a teardown
// that failed first kept (releaseWebRTCPorts): the unit holding those ports is
// now gone. A cleanup that carries no cluster id predates the column and has
// nothing to name the rows by.
func (cm *ClusterManager) releaseAllocationsOfCompletedTeardown(ctx context.Context, r pendingCleanupRow) error {
	if r.ClusterID == "" {
		return nil
	}
	var services []string
	switch r.Action {
	case teardownSFUAction:
		services = []string{"sfu"}
	case teardownTURNAction:
		services = []string{"turn"}
	case teardownAction:
		services = []string{"sfu", "turn"}
	default:
		return nil
	}
	if err := cm.releaseWebRTCPortsOfNode(ctx, r.ClusterID, r.NodeID, services...); err != nil {
		return fmt.Errorf("release the WebRTC ports of %s on node %s after its teardown was completed: %w", r.Namespace, r.NodeID, err)
	}
	return nil
}

// withdrawPendingTeardowns deletes the destructive cleanups still owed for a
// namespace on a node, as the namespace is given that node: they belong to an
// incarnation that is gone.
func withdrawPendingTeardowns(ctx context.Context, db rqlite.Client, clusterID, nodeID string, actions ...string) error {
	for _, action := range actions {
		if _, err := db.Exec(client.WithInternalAuth(ctx), `
			DELETE FROM namespace_pending_cleanup
			 WHERE node_id = ? AND action = ?
			   AND namespace = (SELECT namespace_name FROM namespace_clusters WHERE id = ?)`,
			nodeID, action, clusterID); err != nil {
			return fmt.Errorf("withdraw the pending %s of node %s: %w", action, nodeID, err)
		}
	}
	return nil
}
