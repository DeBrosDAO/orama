package namespace

import (
	"context"
	"fmt"
	"net/http"
)

// ErrCodeNamespaceTeardownPending is returned when a namespace of that name was
// deleted and a node has not yet confirmed tearing it down.
const ErrCodeNamespaceTeardownPending = "NAMESPACE_TEARDOWN_PENDING"

// teardownPendingRetryAfterSeconds is the Retry-After of that refusal: the
// tenant reconciler replays an owed teardown every sweep.
const teardownPendingRetryAfterSeconds = 60

// pendingTeardownNodesQuery names the active nodes that are still owed a
// cleanup for a namespace name: the previous namespace of that name may still
// have units, an Olric keyspace, env files and a data directory there. Only the
// raft directory is cleared by a fresh start, so a namespace created now would
// inherit the rest. A node that is no longer active is left out: it is not asked
// to tear anything down (deprovisioning skips it) and reaps what it holds itself
// when it returns, so it must not keep the name for ever.
const pendingTeardownNodesQuery = `SELECT DISTINCT p.node_id AS node_id
	FROM namespace_pending_cleanup p
	JOIN dns_nodes dn ON dn.id = p.node_id
	WHERE p.namespace = ? AND dn.status = 'active'
	ORDER BY p.node_id`

// pendingTeardownNodes returns the nodes still owed a cleanup for the name.
func (h *CreateHandler) pendingTeardownNodes(ctx context.Context, name string) ([]string, error) {
	var rows []struct {
		NodeID string `db:"node_id"`
	}
	if err := h.ormClient.Query(ctx, &rows, pendingTeardownNodesQuery, name); err != nil {
		return nil, err
	}
	nodes := make([]string, len(rows))
	for i, row := range rows {
		nodes[i] = row.NodeID
	}
	return nodes, nil
}

// refuseTeardownPending answers 409 for a name whose previous namespace is
// still being torn down: retryable. The nodes go to the log, not the caller:
// any wallet the creation policy permits can ask about any name.
func refuseTeardownPending(w http.ResponseWriter, name string) {
	w.Header().Set("Retry-After", fmt.Sprint(teardownPendingRetryAfterSeconds))
	writeCreateJSON(w, http.StatusConflict, map[string]any{
		"error": fmt.Sprintf("the previous namespace named %s is still being torn down; "+
			"try again once that has finished", name),
		"code":      ErrCodeNamespaceTeardownPending,
		"retryable": true,
	})
}
