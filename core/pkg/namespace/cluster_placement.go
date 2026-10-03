package namespace

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// placementAttempts bounds how often a cluster selects its nodes again after a
// node it chose filled up under it. Each such conflict means another cluster
// was placed, so the fleet only fills; a selection that then finds too few
// nodes with room fails on its own, which is the honest refusal.
const placementAttempts = 5

// errReleaseFailed marks a placement whose blocks could not all be given back.
// It ends the placement: selecting again could leave the unreleased block held
// by this cluster on a node that is not one of its members.
var errReleaseFailed = errors.New("a port block taken for the cluster could not be given back")

// placement is what placeCluster needs from the registry: choosing count nodes,
// taking a port block on one, and giving a block back.
type placement struct {
	selectNodes func(ctx context.Context, count int) ([]NodeCapacity, error)
	allocate    func(ctx context.Context, nodeID string) (*PortBlock, error)
	release     func(ctx context.Context, nodeID string) error
	logger      *zap.Logger
}

// placeCluster selects count nodes and takes a port block on each.
//
// Nodes are chosen from a read of the registry, and a cluster provisioning at
// the same time can take a chosen node's last block before this one writes.
// That node then has no block for this cluster, and the namespace failed with
// "no ports available on node" although the fleet had room for it elsewhere
// (stagenet e2e, 2026-10-03). The selection is made again from a fresh read,
// which no longer offers the full node. Any other failure ends it. The blocks
// already taken are given back before a new selection or a failure.
func placeCluster(ctx context.Context, count int, p placement) ([]NodeCapacity, []*PortBlock, error) {
	for attempt := 1; ; attempt++ {
		nodes, err := p.selectNodes(ctx, count)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to select nodes: %w", err)
		}
		blocks, err := allocateOnEach(ctx, nodes, p)
		if err == nil {
			return nodes, blocks, nil
		}
		if errors.Is(err, errReleaseFailed) || !errors.Is(err, ErrNoPortsAvailable) || attempt == placementAttempts {
			return nil, nil, err
		}
		p.logger.Info("A node chosen for the cluster filled up before its port block was taken; selecting again",
			zap.Int("attempt", attempt), zap.Error(err))
	}
}

// allocateOnEach takes a port block on every node, giving back the ones taken
// when one cannot be.
func allocateOnEach(ctx context.Context, nodes []NodeCapacity, p placement) ([]*PortBlock, error) {
	blocks := make([]*PortBlock, len(nodes))
	for i, node := range nodes {
		block, err := p.allocate(ctx, node.NodeID)
		if err != nil {
			allocErr := fmt.Errorf("failed to allocate ports on node %s: %w", node.NodeID, err)
			for _, taken := range nodes[:i] {
				if relErr := p.release(ctx, taken.NodeID); relErr != nil {
					allocErr = errors.Join(allocErr, fmt.Errorf("%w: node %s: %w", errReleaseFailed, taken.NodeID, relErr))
				}
			}
			return nil, allocErr
		}
		blocks[i] = block
	}
	return blocks, nil
}

// placeCluster is placeCluster on this manager's registry, for cluster clusterID.
// A placement that fails gives back every block recorded for the cluster, on a
// context of its own (the provisioning one may be what ran out): a release
// that failed during placement, or an allocation whose write committed but
// whose reply was lost, would otherwise keep a block reserved for a cluster
// that never ran.
func (cm *ClusterManager) placeCluster(ctx context.Context, clusterID string, bp Blueprint) ([]NodeCapacity, []*PortBlock, error) {
	nodes, blocks, err := placeCluster(ctx, bp.SelectCount, cm.placementFor(clusterID, bp))
	if err == nil {
		return nodes, blocks, nil
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if relErr := cm.releaseConfirmedAllocations(rctx, clusterID, nil); relErr != nil {
		err = errors.Join(err, relErr)
	}
	return nil, nil, err
}

// placementFor is the placement of cluster clusterID on this manager's registry.
func (cm *ClusterManager) placementFor(clusterID string, bp Blueprint) placement {
	return placement{
		selectNodes: cm.selectNodesWaitingForLeader,
		allocate: func(ctx context.Context, nodeID string) (*PortBlock, error) {
			return cm.allocatePortsWaitingForLeader(ctx, nodeID, clusterID, bp)
		},
		// A block is given back on a context of its own: the provisioning one
		// may be what just ran out, and the block would stay reserved.
		release: func(ctx context.Context, nodeID string) error {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			defer cancel()
			return cm.portAllocator.DeallocatePortBlock(rctx, clusterID, nodeID)
		},
		logger: cm.logger,
	}
}

// logPlacement records the nodes a cluster was placed on and the block taken on
// each.
func (cm *ClusterManager) logPlacement(ctx context.Context, clusterID string, nodes []NodeCapacity, blocks []*PortBlock) {
	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.NodeID
	}
	cm.logEvent(ctx, clusterID, EventNodesSelected, "", "Selected nodes for cluster", map[string]interface{}{"nodes": nodeIDs})
	for i, block := range blocks {
		cm.logEvent(ctx, clusterID, EventPortsAllocated, nodes[i].NodeID,
			fmt.Sprintf("Allocated ports %d-%d", block.PortStart, block.PortEnd), nil)
	}
}
