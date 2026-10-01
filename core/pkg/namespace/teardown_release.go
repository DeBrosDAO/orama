package namespace

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// allocationTables are the registry tables that reserve a node's ports for a
// cluster: the core port blocks and the WebRTC allocations. A row in either
// keeps the allocator from handing the same ports to the next namespace, so it
// must outlive the cluster row of a namespace whose units may still hold them:
// the allocators count a row by node and range alone and never join it to
// namespace_clusters.
var allocationTables = []string{"namespace_port_allocations", "webrtc_port_allocations"}

// releaseConfirmedAllocations frees a cluster's port reservations on every node
// except the unconfirmed ones, whose teardown is still owed and whose units may
// still hold their ports (bugboard #275). A reservation of an unconfirmed node
// is freed when its recorded teardown is carried out
// (releaseAllocationsOfCompletedTeardown).
func (cm *ClusterManager) releaseConfirmedAllocations(ctx context.Context, clusterID string, unconfirmed []string) error {
	where := `namespace_cluster_id = ?`
	args := []any{clusterID}
	if len(unconfirmed) > 0 {
		where += ` AND node_id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(unconfirmed)), ",") + `)`
		for _, id := range unconfirmed {
			args = append(args, id)
		}
	}
	for _, table := range allocationTables {
		if _, err := cm.db.Exec(client.WithInternalAuth(ctx), `DELETE FROM `+table+` WHERE `+where, args...); err != nil {
			return fmt.Errorf("release the %s of cluster %s: %w", table, clusterID, err)
		}
	}
	return nil
}

// owedUnreleasedAllocations is what the teardown action kept reserved on its
// node: the WebRTC services it stops and, for the teardown of the whole
// namespace, the core port block. The stop-* actions free nothing.
func owedUnreleasedAllocations(action string) (services []string, core bool) {
	switch action {
	case teardownSFUAction:
		return []string{"sfu"}, false
	case teardownTURNAction:
		return []string{"turn"}, false
	case teardownAction:
		return []string{"sfu", "turn"}, true
	}
	return nil, false
}

// releaseOwedAllocations frees the reservations a cluster kept on one node for a
// cleanup that is settled: carried out, or dropped because the namespace was
// created again there or the node was removed. The rows are keyed by the
// cluster the cleanup was owed for, so another incarnation's are not touched.
func releaseOwedAllocations(ctx context.Context, db rqlite.Client, clusterID, nodeID, action string) error {
	ctx = client.WithInternalAuth(ctx)
	services, core := owedUnreleasedAllocations(action)
	for _, svc := range services {
		if _, err := db.Exec(ctx,
			`DELETE FROM webrtc_port_allocations WHERE namespace_cluster_id = ? AND node_id = ? AND service_type = ?`,
			clusterID, nodeID, svc); err != nil {
			return fmt.Errorf("release the %s ports of cluster %s on node %s: %w", svc, clusterID, nodeID, err)
		}
	}
	if core {
		if _, err := db.Exec(ctx,
			`DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`,
			clusterID, nodeID); err != nil {
			return fmt.Errorf("release the port block of cluster %s on node %s: %w", clusterID, nodeID, err)
		}
	}
	return nil
}

// releaseAllocationsExceptOwed frees a cluster's reservations on every node that
// is not owed a cleanup of it: an owed node's units may still hold their ports.
// A row recorded before the cluster_id column has no cluster to compare, so it
// holds the namespace's reservation on its node too.
func (cm *ClusterManager) releaseAllocationsExceptOwed(ctx context.Context, clusterID, namespace string) error {
	var owed []struct {
		NodeID string `db:"node_id"`
	}
	if err := cm.db.Query(client.WithInternalAuth(ctx), &owed, `
		SELECT DISTINCT node_id FROM namespace_pending_cleanup
		 WHERE namespace = ? AND (cluster_id = ? OR cluster_id = '')`, namespace, clusterID); err != nil {
		return fmt.Errorf("read the cleanups owed for %s: %w", namespace, err)
	}
	nodes := make([]string, len(owed))
	for i, o := range owed {
		nodes[i] = o.NodeID
	}
	return cm.releaseConfirmedAllocations(ctx, clusterID, nodes)
}
