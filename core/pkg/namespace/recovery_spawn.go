package namespace

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The spawns of node replacement and cluster repair onto the node chosen for
// cluster. Locally they run under AdmitSpawn; remotely the node's spawn handler
// admits them, given the cluster's id. Either way a spawn for a cluster that is
// being deleted, or is no longer the namespace's, is refused.

func (cm *ClusterManager) spawnRQLiteOnNode(ctx context.Context, cluster *NamespaceCluster, node *NodeCapacity, cfg rqlite.InstanceConfig) error {
	if node.NodeID == cm.localNodeID {
		return cm.spawnAdmitted(ctx, cluster.NamespaceName, cluster.ID, func() error { return cm.spawnRQLiteWithSystemd(ctx, cfg) })
	}
	_, err := cm.spawnRQLiteRemote(ctx, cluster.ID, node.InternalIP, cfg)
	return err
}

func (cm *ClusterManager) spawnOlricOnNode(ctx context.Context, cluster *NamespaceCluster, node *NodeCapacity, cfg olric.InstanceConfig) error {
	if node.NodeID == cm.localNodeID {
		return cm.spawnAdmitted(ctx, cluster.NamespaceName, cluster.ID, func() error { return cm.spawnOlricWithSystemd(ctx, cfg) })
	}
	_, err := cm.spawnOlricRemote(ctx, cluster.ID, node.InternalIP, cfg)
	return err
}

func (cm *ClusterManager) spawnGatewayOnNode(ctx context.Context, cluster *NamespaceCluster, node *NodeCapacity, cfg gatewayspec.InstanceConfig) error {
	if node.NodeID == cm.localNodeID {
		return cm.spawnAdmitted(ctx, cluster.NamespaceName, cluster.ID, func() error { return cm.spawnGatewayWithSystemd(ctx, cfg) })
	}
	_, err := cm.spawnGatewayRemote(ctx, cluster.ID, node.InternalIP, cfg)
	return err
}
