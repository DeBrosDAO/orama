package namespace

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
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

// releaseCorePortBlockOfNode frees a cluster's core port block on one node: the
// unit holding those ports is gone.
func (cm *ClusterManager) releaseCorePortBlockOfNode(ctx context.Context, clusterID, nodeID string) error {
	_, err := cm.db.Exec(client.WithInternalAuth(ctx),
		`DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`, clusterID, nodeID)
	return err
}
