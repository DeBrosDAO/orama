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

// notOwedTeardownSQL narrows a read of port blocks (alias pa) to the blocks of
// members the cluster still uses. The block of a member the cluster evicted
// while its teardown was unconfirmed stays reserved until the recorded teardown
// is carried out, but the node is no longer part of the cluster: it must not
// reappear in the cluster state, the join lists or the surviving ports.
const notOwedTeardownSQL = `AND NOT EXISTS (SELECT 1 FROM namespace_pending_cleanup pc
		WHERE pc.node_id = pa.node_id AND pc.cluster_id = pa.namespace_cluster_id AND pc.action = '` + teardownAction + `')`

// evictMemberAllocations ends a node's part in a cluster that lives on: the
// namespace is torn down on the node, and its core port block and WebRTC
// allocations are freed only once the node confirmed the stop (a response from
// the node, or the result of the local stop), or is gone from the registry and
// took its units with it. A node that did not confirm it (dead, unreachable, no
// overlay address) is recorded in namespace_pending_cleanup with the cluster id,
// which keeps its reservations until the replay carries the teardown out
// (releaseOwedAllocations): freeing them under units that may still run hands
// the ports to the next namespace (bugboard #275). The error says the teardown
// is owed, not that the eviction failed.
func (cm *ClusterManager) evictMemberAllocations(ctx context.Context, clusterID, namespace, nodeID string) error {
	removed, err := cm.nodeRemoved(ctx, nodeID)
	if err != nil {
		return err
	}
	if !removed {
		if err := cm.teardownEvictedMember(ctx, clusterID, namespace, nodeID); err != nil {
			return err
		}
	}
	return releaseOwedAllocations(ctx, cm.db, clusterID, nodeID, teardownAction)
}

// teardownEvictedMember tears the namespace down on a registered node that left
// the cluster. A node that is not active is not asked: it cannot answer, and a
// request to it blocks for the whole spawn timeout, node after node when a
// cluster loses several. Its teardown is recorded as owed without being sent
// (stopStaleClusterServices does the same).
func (cm *ClusterManager) teardownEvictedMember(ctx context.Context, clusterID, namespace, nodeID string) error {
	scope := cleanupScope{ClusterID: clusterID}
	nodeIP := ""
	if ips, ipErr := cm.getNodeIPs(ctx, nodeID); ipErr == nil {
		nodeIP = ips.InternalIP
	}
	if nodeID != cm.localNodeID {
		inactive, err := cm.countRows(ctx, `SELECT COUNT(*) AS count FROM dns_nodes WHERE id = ? AND status != 'active'`, nodeID)
		if err != nil {
			return fmt.Errorf("check whether evicted node %s is active: %w", nodeID, err)
		}
		if inactive > 0 {
			cause := fmt.Errorf("node %s is not active, so it cannot confirm the teardown of %s", nodeID, namespace)
			if err := cm.recordPendingCleanup(ctx, namespace, nodeID, nodeIP, teardownAction, scope, cause); err != nil {
				return err
			}
			return fmt.Errorf("the teardown of %s on evicted node %s is owed, its ports stay reserved until it is replayed: %w", namespace, nodeID, cause)
		}
	}
	node := staleClusterNode{NodeID: nodeID, InternalIP: nodeIP}
	if err := cm.teardownNamespaceOnNode(ctx, node, namespace, scope); err != nil {
		return fmt.Errorf("the teardown of %s on evicted node %s is not confirmed, its ports stay reserved until it is replayed: %w", namespace, nodeID, err)
	}
	return nil
}
