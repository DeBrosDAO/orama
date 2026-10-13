package namespace

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/sfu"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// readLocalSFUPorts reads the ports this node's SFU of a namespace is
// configured to hold. found is false when the namespace has no SFU config on
// this node.
//
// The config is what the unit actually binds, so it is the evidence of what a
// running SFU holds when the registry has no row for it. It must also sit on
// the allocator's grid: a range that straddles two grid blocks would overlap
// both while the unique index saw neither.
func (cm *ClusterManager) readLocalSFUPorts(namespace string) (signaling, mediaStart, mediaEnd int, found bool, err error) {
	path := filepath.Join(cm.systemdSpawner.namespaceBase, namespace, "configs", fmt.Sprintf("sfu-%s.yaml", cm.localNodeID))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("read the SFU config %s: %w", path, err)
	}
	var cfg sfu.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0, 0, 0, false, fmt.Errorf("parse the SFU config %s: %w", path, err)
	}
	_, portStr, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("the SFU config %s has listen_addr %q: %w", path, cfg.ListenAddr, err)
	}
	signaling, err = strconv.Atoi(portStr)
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("the SFU config %s has listen_addr %q: %w", path, cfg.ListenAddr, err)
	}
	if signaling < SFUSignalingPortRangeStart || signaling > SFUSignalingPortRangeEnd {
		return 0, 0, 0, false, fmt.Errorf("the SFU config %s binds signaling port %d, outside %d-%d", path, signaling, SFUSignalingPortRangeStart, SFUSignalingPortRangeEnd)
	}
	mediaStart, mediaEnd = cfg.MediaPortStart, cfg.MediaPortEnd
	onGrid := mediaStart >= SFUMediaPortRangeStart && mediaEnd <= SFUMediaPortRangeEnd &&
		(mediaStart-SFUMediaPortRangeStart)%SFUMediaPortsPerNamespace == 0 &&
		mediaEnd-mediaStart+1 == SFUMediaPortsPerNamespace
	if !onGrid {
		return 0, 0, 0, false, fmt.Errorf("the SFU config %s holds media ports %d-%d, which is not one of the allocator's %d-port blocks in %d-%d",
			path, mediaStart, mediaEnd, SFUMediaPortsPerNamespace, SFUMediaPortRangeStart, SFUMediaPortRangeEnd)
	}
	return signaling, mediaStart, mediaEnd, true, nil
}

// backfillSFUAllocation makes the registry hold what this node's SFU of a
// WebRTC-enabled namespace is configured to bind.
//
// The allocator picks free ports from the rows in webrtc_port_allocations, so a
// unit that holds ports without a row has them handed to the next namespace,
// whose unit then crash-loops on "address already in use" for as long as the
// first one runs. A unit can be left without a row when its allocation was
// freed while the unit kept running (the teardown that was to stop it failed).
// The registry is the one place ports are decided, so the missing row is
// written rather than every allocation reading other namespaces' config files.
//
// Only a namespace the registry says has WebRTC enabled is recorded; the
// caller reconciles only those. A returned error means the registry and the
// unit disagree (another cluster holds these ports, or the row names others),
// and the caller must not treat the unit as unallocated and stop it.
func (cm *ClusterManager) backfillSFUAllocation(ctx context.Context, namespace, clusterID string) error {
	if cm.systemdSpawner == nil || cm.localNodeID == "" || clusterID == "" {
		return nil
	}
	signaling, mediaStart, mediaEnd, found, err := cm.readLocalSFUPorts(namespace)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	// A node that is no longer a member keeps its old config until its own
	// orphan sweep removes it; that SFU is not a role the registry should hold.
	members, err := cm.countRows(ctx, `SELECT COUNT(*) AS count FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ?`, clusterID, cm.localNodeID)
	if err != nil {
		return fmt.Errorf("check that this node is a member of namespace %s: %w", namespace, err)
	}
	if members == 0 {
		return nil
	}
	existing, err := cm.webrtcPortAllocator.GetSFUPorts(ctx, clusterID, cm.localNodeID)
	if err != nil {
		return fmt.Errorf("check the SFU allocation of namespace %s on this node: %w", namespace, err)
	}
	if _, err := cm.webrtcPortAllocator.RecordSFUPorts(ctx, cm.localNodeID, clusterID, signaling, mediaStart, mediaEnd); err != nil {
		return err
	}
	if existing == nil {
		cm.logger.Warn("Recorded the SFU ports of a running unit that had no allocation row, so no other namespace is given them",
			zap.String("namespace", namespace),
			zap.String("cluster_id", clusterID),
			zap.Int("signaling_port", signaling),
			zap.Int("media_start", mediaStart),
			zap.Int("media_end", mediaEnd))
	}
	return nil
}
