package namespace

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// webrtcUnit is one WebRTC service of a namespace on one node: what an
// allocation row stands for.
type webrtcUnit struct {
	NodeID      string
	ServiceType string // "sfu" or "turn"
}

// releaseWebRTCPorts frees a namespace's WebRTC allocations, except those of
// the units in retained.
//
// An allocation is what keeps the next namespace off a unit's ports, so it must
// never be freed while the unit still holds them. A teardown that could not stop
// a unit (the node was unreachable, or systemd would not stop it in time) puts
// that unit in retained: its row stays, and the caller reports the failure. The
// row is freed when the teardown is eventually carried out
// (releaseWebRTCPortsOfNode, from the pending-cleanup replay) or by running the
// disable again.
func (cm *ClusterManager) releaseWebRTCPorts(ctx context.Context, clusterID string, retained []webrtcUnit) error {
	if len(retained) == 0 {
		return cm.webrtcPortAllocator.DeallocateAll(ctx, clusterID)
	}
	kept := make(map[webrtcUnit]bool, len(retained))
	for _, u := range retained {
		kept[u] = true
		cm.logger.Error("Keeping the WebRTC allocation of a unit that could not be torn down: it still holds its ports",
			zap.String("cluster_id", clusterID),
			zap.String("node_id", u.NodeID),
			zap.String("service", u.ServiceType))
	}
	blocks, err := cm.webrtcPortAllocator.GetAllPorts(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("read the WebRTC allocations of cluster %s to release those whose units are gone: %w", clusterID, err)
	}
	var errs []error
	for _, b := range blocks {
		u := webrtcUnit{NodeID: b.NodeID, ServiceType: b.ServiceType}
		if kept[u] {
			continue
		}
		if err := cm.webrtcPortAllocator.DeallocateByNode(ctx, clusterID, u.NodeID, u.ServiceType); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// releaseWebRTCPortsOfNode frees the WebRTC allocations a cluster holds on one
// node, once a teardown that was owed there has been carried out.
func (cm *ClusterManager) releaseWebRTCPortsOfNode(ctx context.Context, clusterID, nodeID string, serviceTypes ...string) error {
	var errs []error
	for _, svc := range serviceTypes {
		if err := cm.webrtcPortAllocator.DeallocateByNode(ctx, clusterID, nodeID, svc); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
