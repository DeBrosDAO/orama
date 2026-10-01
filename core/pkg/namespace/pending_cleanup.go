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
		`SELECT namespace, node_id, node_ip, action, cluster_id, purge_data, attempts FROM namespace_pending_cleanup
		  WHERE attempts < ? OR last_attempt_at IS NULL OR last_attempt_at <= datetime('now', ?)
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

// replayRow replays one pending cleanup. The error it returns is a failure of
// the registry's side (the supersession check, freeing allocations); a cleanup
// the node did not confirm is recorded again and logged, not returned.
func (cm *ClusterManager) replayRow(ctx context.Context, r pendingCleanupRow) error {
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
			cm.clearPendingCleanup(ctx, r.Namespace, r.NodeID, r.Action)
			return nil
		}
	}
	// replayCleanup records or clears the row itself, so the outcome is
	// persisted whichever way it goes.
	if err := cm.replayCleanup(ctx, r); err != nil {
		cm.logExhaustedCleanup(r, err)
		return nil
	}
	cm.logger.Info("Completed a stop that had previously failed",
		zap.String("namespace", r.Namespace),
		zap.String("node_id", r.NodeID),
		zap.String("action", r.Action),
		zap.Int("previous_attempts", r.Attempts))
	return cm.releaseAllocationsOfCompletedTeardown(ctx, r)
}

// replayCleanup carries out one owed cleanup on its node and records the
// outcome. A teardown owed by this node is run here; any other goes through the
// node's spawn endpoint, at the overlay address recorded with it or, when none
// was known then, the one the node has registered since.
func (cm *ClusterManager) replayCleanup(ctx context.Context, r pendingCleanupRow) error {
	scope := cleanupScope{ClusterID: r.ClusterID, PurgeData: r.PurgeData != 0}
	if r.NodeID == cm.localNodeID && r.Action == teardownAction {
		return cm.teardownLocalRecorded(ctx, r.NodeID, r.NodeIP, r.Namespace, scope)
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
	return cm.sendStopRequest(ctx, ip, r.Action, r.Namespace, r.NodeID, scope)
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
	if r.Action != teardownAction {
		return nil
	}
	if err := cm.releaseCorePortBlockOfNode(ctx, r.ClusterID, r.NodeID); err != nil {
		return fmt.Errorf("release the port block of %s on node %s after its teardown was completed: %w", r.Namespace, r.NodeID, err)
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
