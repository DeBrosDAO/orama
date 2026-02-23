package namespace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// EnableWebRTC enables WebRTC (SFU + TURN) for an existing namespace cluster.
// Allocates ports, spawns SFU on all 3 nodes and TURN on 2 nodes,
// creates TURN DNS records, and updates cluster state.
func (cm *ClusterManager) EnableWebRTC(ctx context.Context, namespaceName, enabledBy string) error {
	internalCtx := client.WithInternalAuth(ctx)

	// 1. Verify cluster exists and is ready
	cluster, err := cm.GetClusterByNamespace(ctx, namespaceName)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		return ErrClusterNotFound
	}
	if cluster.Status != ClusterStatusReady {
		return &ClusterError{Message: fmt.Sprintf("cluster status is %q, must be %q to enable WebRTC", cluster.Status, ClusterStatusReady)}
	}

	// 2. Check if WebRTC is already enabled
	var existingConfigs []WebRTCConfig
	if err := cm.db.Query(internalCtx, &existingConfigs,
		`SELECT * FROM namespace_webrtc_config WHERE namespace_cluster_id = ? AND enabled = 1`, cluster.ID); err == nil && len(existingConfigs) > 0 {
		return ErrWebRTCAlreadyEnabled
	}

	cm.logger.Info("Enabling WebRTC for namespace",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", cluster.ID),
	)

	// 3. Generate TURN shared secret (32 bytes, crypto/rand)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("failed to generate TURN secret: %w", err)
	}
	turnSecret := base64.StdEncoding.EncodeToString(secretBytes)

	// 4. Insert namespace_webrtc_config
	webrtcConfigID := uuid.New().String()
	_, err = cm.db.Exec(internalCtx,
		`INSERT INTO namespace_webrtc_config (id, namespace_cluster_id, namespace_name, enabled, turn_shared_secret, turn_credential_ttl, sfu_node_count, turn_node_count, enabled_by, enabled_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		webrtcConfigID, cluster.ID, namespaceName,
		turnSecret, DefaultTURNCredentialTTL,
		DefaultSFUNodeCount, DefaultTURNNodeCount,
		enabledBy, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert WebRTC config: %w", err)
	}

	// 5. Get cluster nodes with IPs
	clusterNodes, err := cm.getClusterNodesWithIPs(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}
	if len(clusterNodes) < 3 {
		return fmt.Errorf("cluster has %d nodes, need at least 3 for WebRTC", len(clusterNodes))
	}

	// 6. Allocate SFU ports on all nodes
	sfuBlocks := make(map[string]*WebRTCPortBlock) // nodeID -> block
	for _, node := range clusterNodes {
		block, err := cm.webrtcPortAllocator.AllocateSFUPorts(ctx, node.NodeID, cluster.ID)
		if err != nil {
			cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
			return fmt.Errorf("failed to allocate SFU ports on node %s: %w", node.NodeID, err)
		}
		sfuBlocks[node.NodeID] = block
	}

	// 7. Select TURN nodes (prefer nodes without existing TURN allocations)
	turnNodes := cm.selectTURNNodes(ctx, clusterNodes, DefaultTURNNodeCount)

	// 8. Allocate TURN ports on selected nodes
	turnBlocks := make(map[string]*WebRTCPortBlock) // nodeID -> block
	for _, node := range turnNodes {
		block, err := cm.webrtcPortAllocator.AllocateTURNPorts(ctx, node.NodeID, cluster.ID)
		if err != nil {
			cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
			return fmt.Errorf("failed to allocate TURN ports on node %s: %w", node.NodeID, err)
		}
		turnBlocks[node.NodeID] = block
	}

	// 9. Build TURN server list for SFU config
	turnDomain := fmt.Sprintf("turn.ns-%s.%s", namespaceName, cm.baseDomain)
	turnServers := []sfu.TURNServerConfig{
		{Host: turnDomain, Port: TURNDefaultPort, Secure: false},
		{Host: turnDomain, Port: TURNSPort, Secure: true},
	}

	// 10. Get port blocks for RQLite DSN
	portBlocks, err := cm.portAllocator.GetAllPortBlocks(ctx, cluster.ID)
	if err != nil {
		cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
		return fmt.Errorf("failed to get port blocks: %w", err)
	}

	// Build nodeID -> PortBlock map
	nodePortBlocks := make(map[string]*PortBlock)
	for i := range portBlocks {
		nodePortBlocks[portBlocks[i].NodeID] = &portBlocks[i]
	}

	// 11. Spawn TURN on selected nodes
	for _, node := range turnNodes {
		turnBlock := turnBlocks[node.NodeID]
		turnCfg := TURNInstanceConfig{
			Namespace:       namespaceName,
			NodeID:          node.NodeID,
			ListenAddr:      fmt.Sprintf("0.0.0.0:%d", turnBlock.TURNListenPort),
			TURNSListenAddr: fmt.Sprintf("0.0.0.0:%d", turnBlock.TURNTLSPort),
			PublicIP:        node.PublicIP,
			Realm:           cm.baseDomain,
			AuthSecret:      turnSecret,
			RelayPortStart:  turnBlock.TURNRelayPortStart,
			RelayPortEnd:    turnBlock.TURNRelayPortEnd,
		}

		if err := cm.spawnTURNOnNode(ctx, node, namespaceName, turnCfg); err != nil {
			cm.logger.Error("Failed to spawn TURN",
				zap.String("namespace", namespaceName),
				zap.String("node_id", node.NodeID),
				zap.Error(err))
			cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
			return fmt.Errorf("failed to spawn TURN on node %s: %w", node.NodeID, err)
		}

		cm.logEvent(ctx, cluster.ID, EventTURNStarted, node.NodeID,
			fmt.Sprintf("TURN started on %s (relay ports %d-%d)", node.NodeID, turnBlock.TURNRelayPortStart, turnBlock.TURNRelayPortEnd), nil)
	}

	// 12. Spawn SFU on all nodes
	for _, node := range clusterNodes {
		sfuBlock := sfuBlocks[node.NodeID]
		pb := nodePortBlocks[node.NodeID]
		rqliteDSN := fmt.Sprintf("http://localhost:%d", pb.RQLiteHTTPPort)

		sfuCfg := SFUInstanceConfig{
			Namespace:      namespaceName,
			NodeID:         node.NodeID,
			ListenAddr:     fmt.Sprintf("%s:%d", node.InternalIP, sfuBlock.SFUSignalingPort),
			MediaPortStart: sfuBlock.SFUMediaPortStart,
			MediaPortEnd:   sfuBlock.SFUMediaPortEnd,
			TURNServers:    turnServers,
			TURNSecret:     turnSecret,
			TURNCredTTL:    DefaultTURNCredentialTTL,
			RQLiteDSN:      rqliteDSN,
		}

		if err := cm.spawnSFUOnNode(ctx, node, namespaceName, sfuCfg); err != nil {
			cm.logger.Error("Failed to spawn SFU",
				zap.String("namespace", namespaceName),
				zap.String("node_id", node.NodeID),
				zap.Error(err))
			cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
			return fmt.Errorf("failed to spawn SFU on node %s: %w", node.NodeID, err)
		}

		cm.logEvent(ctx, cluster.ID, EventSFUStarted, node.NodeID,
			fmt.Sprintf("SFU started on %s:%d", node.InternalIP, sfuBlock.SFUSignalingPort), nil)
	}

	// 13. Create TURN DNS records
	var turnIPs []string
	for _, node := range turnNodes {
		turnIPs = append(turnIPs, node.PublicIP)
	}
	if err := cm.dnsManager.CreateTURNRecords(ctx, namespaceName, turnIPs); err != nil {
		cm.logger.Error("Failed to create TURN DNS records, aborting WebRTC enablement",
			zap.String("namespace", namespaceName),
			zap.Error(err))
		cm.cleanupWebRTCOnError(ctx, cluster.ID, namespaceName, clusterNodes)
		return fmt.Errorf("failed to create TURN DNS records: %w", err)
	}

	// 14. Update cluster-state.json on all nodes with WebRTC info
	cm.updateClusterStateWithWebRTC(ctx, cluster, clusterNodes, sfuBlocks, turnBlocks, turnDomain, turnSecret)

	// 15. Restart namespace gateways with WebRTC config so they register WebRTC routes
	cm.restartGatewaysWithWebRTC(ctx, cluster, clusterNodes, nodePortBlocks, sfuBlocks, turnDomain, turnSecret)

	cm.logEvent(ctx, cluster.ID, EventWebRTCEnabled, "",
		fmt.Sprintf("WebRTC enabled: SFU on %d nodes, TURN on %d nodes", len(clusterNodes), len(turnNodes)), nil)

	cm.logger.Info("WebRTC enabled successfully",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", cluster.ID),
		zap.Int("sfu_nodes", len(clusterNodes)),
		zap.Int("turn_nodes", len(turnNodes)),
	)

	return nil
}

// DisableWebRTC disables WebRTC for a namespace cluster.
// Stops SFU/TURN services, deallocates ports, and cleans up DNS/DB.
func (cm *ClusterManager) DisableWebRTC(ctx context.Context, namespaceName string) error {
	internalCtx := client.WithInternalAuth(ctx)

	// 1. Verify cluster exists
	cluster, err := cm.GetClusterByNamespace(ctx, namespaceName)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		return ErrClusterNotFound
	}

	// 2. Verify WebRTC is enabled
	var configs []WebRTCConfig
	if err := cm.db.Query(internalCtx, &configs,
		`SELECT * FROM namespace_webrtc_config WHERE namespace_cluster_id = ? AND enabled = 1`, cluster.ID); err != nil || len(configs) == 0 {
		return ErrWebRTCNotEnabled
	}

	cm.logger.Info("Disabling WebRTC for namespace",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", cluster.ID),
	)

	// 3. Get cluster nodes with IPs
	clusterNodes, err := cm.getClusterNodesWithIPs(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	// 4. Stop SFU on all nodes
	for _, node := range clusterNodes {
		cm.stopSFUOnNode(ctx, node.NodeID, node.InternalIP, namespaceName)
		cm.logEvent(ctx, cluster.ID, EventSFUStopped, node.NodeID, "SFU stopped", nil)
	}

	// 5. Stop TURN on nodes that have TURN allocations
	turnBlocks, _ := cm.getWebRTCBlocksByType(ctx, cluster.ID, "turn")
	for _, block := range turnBlocks {
		nodeIP := cm.getNodeIP(clusterNodes, block.NodeID)
		cm.stopTURNOnNode(ctx, block.NodeID, nodeIP, namespaceName)
		cm.logEvent(ctx, cluster.ID, EventTURNStopped, block.NodeID, "TURN stopped", nil)
	}

	// 6. Deallocate all WebRTC ports
	if err := cm.webrtcPortAllocator.DeallocateAll(ctx, cluster.ID); err != nil {
		cm.logger.Warn("Failed to deallocate WebRTC ports", zap.Error(err))
	}

	// 7. Delete TURN DNS records
	if err := cm.dnsManager.DeleteTURNRecords(ctx, namespaceName); err != nil {
		cm.logger.Warn("Failed to delete TURN DNS records", zap.Error(err))
	}

	// 8. Clean up DB tables
	cm.db.Exec(internalCtx, `DELETE FROM webrtc_rooms WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(internalCtx, `DELETE FROM namespace_webrtc_config WHERE namespace_cluster_id = ?`, cluster.ID)

	// 9. Update cluster-state.json to remove WebRTC info
	cm.updateClusterStateWithWebRTC(ctx, cluster, clusterNodes, nil, nil, "", "")

	// 10. Restart namespace gateways without WebRTC config so they unregister WebRTC routes
	portBlocks, err := cm.portAllocator.GetAllPortBlocks(ctx, cluster.ID)
	if err == nil {
		nodePortBlocks := make(map[string]*PortBlock)
		for i := range portBlocks {
			nodePortBlocks[portBlocks[i].NodeID] = &portBlocks[i]
		}
		cm.restartGatewaysWithWebRTC(ctx, cluster, clusterNodes, nodePortBlocks, nil, "", "")
	} else {
		cm.logger.Warn("Failed to get port blocks for gateway restart after WebRTC disable", zap.Error(err))
	}

	cm.logEvent(ctx, cluster.ID, EventWebRTCDisabled, "", "WebRTC disabled", nil)

	cm.logger.Info("WebRTC disabled successfully",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", cluster.ID),
	)

	return nil
}

// GetWebRTCConfig returns the WebRTC configuration for a namespace.
func (cm *ClusterManager) GetWebRTCConfig(ctx context.Context, namespaceName string) (*WebRTCConfig, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var configs []WebRTCConfig
	err := cm.db.Query(internalCtx, &configs,
		`SELECT * FROM namespace_webrtc_config WHERE namespace_name = ? AND enabled = 1`, namespaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to query WebRTC config: %w", err)
	}
	if len(configs) == 0 {
		return nil, nil
	}
	return &configs[0], nil
}

// GetWebRTCStatus returns the WebRTC config as an interface{} for the WebRTCManager interface.
func (cm *ClusterManager) GetWebRTCStatus(ctx context.Context, namespaceName string) (interface{}, error) {
	cfg, err := cm.GetWebRTCConfig(ctx, namespaceName)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return cfg, nil
}

// --- Internal helpers ---

// clusterNodeInfo holds node info needed for WebRTC operations
type clusterNodeInfo struct {
	NodeID     string
	InternalIP string // WireGuard IP
	PublicIP   string // Public IP for TURN
}

// getClusterNodesWithIPs returns cluster nodes with both internal and public IPs.
func (cm *ClusterManager) getClusterNodesWithIPs(ctx context.Context, clusterID string) ([]clusterNodeInfo, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type nodeRow struct {
		NodeID     string `db:"node_id"`
		InternalIP string `db:"internal_ip"`
		PublicIP   string `db:"public_ip"`
	}
	var rows []nodeRow
	query := `
		SELECT ncn.node_id,
			   COALESCE(dn.internal_ip, dn.ip_address) as internal_ip,
			   dn.ip_address as public_ip
		FROM namespace_cluster_nodes ncn
		JOIN dns_nodes dn ON ncn.node_id = dn.id
		WHERE ncn.namespace_cluster_id = ?
		GROUP BY ncn.node_id
	`
	if err := cm.db.Query(internalCtx, &rows, query, clusterID); err != nil {
		return nil, err
	}

	nodes := make([]clusterNodeInfo, len(rows))
	for i, r := range rows {
		nodes[i] = clusterNodeInfo{
			NodeID:     r.NodeID,
			InternalIP: r.InternalIP,
			PublicIP:   r.PublicIP,
		}
	}
	return nodes, nil
}

// selectTURNNodes selects the best N nodes for TURN, preferring nodes without existing TURN allocations.
func (cm *ClusterManager) selectTURNNodes(ctx context.Context, nodes []clusterNodeInfo, count int) []clusterNodeInfo {
	if count >= len(nodes) {
		return nodes
	}

	// Prefer nodes without existing TURN allocations
	var preferred, fallback []clusterNodeInfo
	for _, node := range nodes {
		hasTURN, err := cm.webrtcPortAllocator.NodeHasTURN(ctx, node.NodeID)
		if err != nil || !hasTURN {
			preferred = append(preferred, node)
		} else {
			fallback = append(fallback, node)
		}
	}

	// Take from preferred first, then fallback
	result := make([]clusterNodeInfo, 0, count)
	for _, node := range preferred {
		if len(result) >= count {
			break
		}
		result = append(result, node)
	}
	for _, node := range fallback {
		if len(result) >= count {
			break
		}
		result = append(result, node)
	}
	return result
}

// spawnSFUOnNode spawns SFU on a node (local or remote)
func (cm *ClusterManager) spawnSFUOnNode(ctx context.Context, node clusterNodeInfo, namespace string, cfg SFUInstanceConfig) error {
	if node.NodeID == cm.localNodeID {
		return cm.systemdSpawner.SpawnSFU(ctx, namespace, node.NodeID, cfg)
	}
	return cm.spawnSFURemote(ctx, node.InternalIP, cfg)
}

// spawnTURNOnNode spawns TURN on a node (local or remote)
func (cm *ClusterManager) spawnTURNOnNode(ctx context.Context, node clusterNodeInfo, namespace string, cfg TURNInstanceConfig) error {
	if node.NodeID == cm.localNodeID {
		return cm.systemdSpawner.SpawnTURN(ctx, namespace, node.NodeID, cfg)
	}
	return cm.spawnTURNRemote(ctx, node.InternalIP, cfg)
}

// stopSFUOnNode stops SFU on a node (local or remote)
func (cm *ClusterManager) stopSFUOnNode(ctx context.Context, nodeID, nodeIP, namespace string) {
	if nodeID == cm.localNodeID {
		cm.systemdSpawner.StopSFU(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-sfu", namespace, nodeID)
	}
}

// stopTURNOnNode stops TURN on a node (local or remote)
func (cm *ClusterManager) stopTURNOnNode(ctx context.Context, nodeID, nodeIP, namespace string) {
	if nodeID == cm.localNodeID {
		cm.systemdSpawner.StopTURN(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-turn", namespace, nodeID)
	}
}

// spawnSFURemote sends a spawn-sfu request to a remote node
func (cm *ClusterManager) spawnSFURemote(ctx context.Context, nodeIP string, cfg SFUInstanceConfig) error {
	// Serialize TURN servers for transport
	turnServers := make([]map[string]interface{}, len(cfg.TURNServers))
	for i, ts := range cfg.TURNServers {
		turnServers[i] = map[string]interface{}{
			"host":   ts.Host,
			"port":   ts.Port,
			"secure": ts.Secure,
		}
	}

	_, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":           "spawn-sfu",
		"namespace":        cfg.Namespace,
		"node_id":          cfg.NodeID,
		"sfu_listen_addr":  cfg.ListenAddr,
		"sfu_media_start":  cfg.MediaPortStart,
		"sfu_media_end":    cfg.MediaPortEnd,
		"turn_servers":     turnServers,
		"turn_secret":      cfg.TURNSecret,
		"turn_cred_ttl":    cfg.TURNCredTTL,
		"rqlite_dsn":       cfg.RQLiteDSN,
	})
	return err
}

// spawnTURNRemote sends a spawn-turn request to a remote node
func (cm *ClusterManager) spawnTURNRemote(ctx context.Context, nodeIP string, cfg TURNInstanceConfig) error {
	_, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":            "spawn-turn",
		"namespace":         cfg.Namespace,
		"node_id":           cfg.NodeID,
		"turn_listen_addr":  cfg.ListenAddr,
		"turn_turns_addr":   cfg.TURNSListenAddr,
		"turn_public_ip":    cfg.PublicIP,
		"turn_realm":        cfg.Realm,
		"turn_auth_secret":  cfg.AuthSecret,
		"turn_relay_start":  cfg.RelayPortStart,
		"turn_relay_end":    cfg.RelayPortEnd,
	})
	return err
}

// getWebRTCBlocksByType returns all WebRTC port blocks of a given type for a cluster.
func (cm *ClusterManager) getWebRTCBlocksByType(ctx context.Context, clusterID, serviceType string) ([]WebRTCPortBlock, error) {
	allBlocks, err := cm.webrtcPortAllocator.GetAllPorts(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	var filtered []WebRTCPortBlock
	for _, b := range allBlocks {
		if b.ServiceType == serviceType {
			filtered = append(filtered, b)
		}
	}
	return filtered, nil
}

// getNodeIP looks up the internal IP for a node ID from a list.
func (cm *ClusterManager) getNodeIP(nodes []clusterNodeInfo, nodeID string) string {
	for _, n := range nodes {
		if n.NodeID == nodeID {
			return n.InternalIP
		}
	}
	return ""
}

// cleanupWebRTCOnError cleans up partial WebRTC allocations when EnableWebRTC fails mid-way.
func (cm *ClusterManager) cleanupWebRTCOnError(ctx context.Context, clusterID, namespaceName string, nodes []clusterNodeInfo) {
	cm.logger.Warn("Cleaning up partial WebRTC enablement",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", clusterID))

	internalCtx := client.WithInternalAuth(ctx)

	// Stop any spawned SFU/TURN services
	for _, node := range nodes {
		cm.stopSFUOnNode(ctx, node.NodeID, node.InternalIP, namespaceName)
		cm.stopTURNOnNode(ctx, node.NodeID, node.InternalIP, namespaceName)
	}

	// Deallocate ports
	cm.webrtcPortAllocator.DeallocateAll(ctx, clusterID)

	// Remove config row
	cm.db.Exec(internalCtx, `DELETE FROM namespace_webrtc_config WHERE namespace_cluster_id = ?`, clusterID)
}

// updateClusterStateWithWebRTC updates the cluster-state.json on all nodes
// to include (or remove) WebRTC port information.
// Pass nil maps and empty strings to clear WebRTC state (when disabling).
func (cm *ClusterManager) updateClusterStateWithWebRTC(
	ctx context.Context,
	cluster *NamespaceCluster,
	nodes []clusterNodeInfo,
	sfuBlocks map[string]*WebRTCPortBlock,
	turnBlocks map[string]*WebRTCPortBlock,
	turnDomain, turnSecret string,
) {
	// Get existing port blocks for base state
	portBlocks, err := cm.portAllocator.GetAllPortBlocks(ctx, cluster.ID)
	if err != nil {
		cm.logger.Warn("Failed to get port blocks for state update", zap.Error(err))
		return
	}

	// Build nodeID -> PortBlock map
	nodePortMap := make(map[string]*PortBlock)
	for i := range portBlocks {
		nodePortMap[portBlocks[i].NodeID] = &portBlocks[i]
	}

	// Build AllNodes list
	var allStateNodes []ClusterLocalStateNode
	for _, node := range nodes {
		pb := nodePortMap[node.NodeID]
		if pb == nil {
			continue
		}
		allStateNodes = append(allStateNodes, ClusterLocalStateNode{
			NodeID:              node.NodeID,
			InternalIP:          node.InternalIP,
			RQLiteHTTPPort:      pb.RQLiteHTTPPort,
			RQLiteRaftPort:      pb.RQLiteRaftPort,
			OlricHTTPPort:       pb.OlricHTTPPort,
			OlricMemberlistPort: pb.OlricMemberlistPort,
		})
	}

	// Save state on each node
	for _, node := range nodes {
		pb := nodePortMap[node.NodeID]
		if pb == nil {
			continue
		}

		state := &ClusterLocalState{
			ClusterID:     cluster.ID,
			NamespaceName: cluster.NamespaceName,
			LocalNodeID:   node.NodeID,
			LocalIP:       node.InternalIP,
			LocalPorts: ClusterLocalStatePorts{
				RQLiteHTTPPort:      pb.RQLiteHTTPPort,
				RQLiteRaftPort:      pb.RQLiteRaftPort,
				OlricHTTPPort:       pb.OlricHTTPPort,
				OlricMemberlistPort: pb.OlricMemberlistPort,
				GatewayHTTPPort:     pb.GatewayHTTPPort,
			},
			AllNodes:   allStateNodes,
			HasGateway: true,
			BaseDomain: cm.baseDomain,
			SavedAt:    time.Now(),
		}

		// Add WebRTC fields if enabling
		if sfuBlocks != nil {
			if sfuBlock, ok := sfuBlocks[node.NodeID]; ok {
				state.HasSFU = true
				state.SFUSignalingPort = sfuBlock.SFUSignalingPort
				state.SFUMediaPortStart = sfuBlock.SFUMediaPortStart
				state.SFUMediaPortEnd = sfuBlock.SFUMediaPortEnd
			}
		}
		if turnBlocks != nil {
			if turnBlock, ok := turnBlocks[node.NodeID]; ok {
				state.HasTURN = true
				state.TURNListenPort = turnBlock.TURNListenPort
				state.TURNTLSPort = turnBlock.TURNTLSPort
				state.TURNRelayPortStart = turnBlock.TURNRelayPortStart
				state.TURNRelayPortEnd = turnBlock.TURNRelayPortEnd
			}
		}
		// Persist TURN domain and secret so gateways can be restored on cold start
		state.TURNDomain = turnDomain
		state.TURNSharedSecret = turnSecret

		if node.NodeID == cm.localNodeID {
			if err := cm.saveLocalState(state); err != nil {
				cm.logger.Warn("Failed to save local cluster state",
					zap.String("namespace", cluster.NamespaceName),
					zap.Error(err))
			}
		} else {
			cm.saveRemoteState(ctx, node.InternalIP, cluster.NamespaceName, state)
		}
	}
}

// saveRemoteState sends cluster state to a remote node for persistence.
func (cm *ClusterManager) saveRemoteState(ctx context.Context, nodeIP, namespace string, state *ClusterLocalState) {
	_, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":        "save-cluster-state",
		"namespace":     namespace,
		"cluster_state": state,
	})
	if err != nil {
		cm.logger.Warn("Failed to save cluster state on remote node",
			zap.String("node_ip", nodeIP),
			zap.Error(err))
	}
}

// restartGatewaysWithWebRTC restarts namespace gateways on all nodes with updated WebRTC config.
// Pass nil sfuBlocks and empty turnDomain/turnSecret to disable WebRTC on gateways.
func (cm *ClusterManager) restartGatewaysWithWebRTC(
	ctx context.Context,
	cluster *NamespaceCluster,
	nodes []clusterNodeInfo,
	portBlocks map[string]*PortBlock,
	sfuBlocks map[string]*WebRTCPortBlock,
	turnDomain, turnSecret string,
) {
	// Build Olric server addresses from port blocks + node IPs
	var olricServers []string
	for _, node := range nodes {
		if pb, ok := portBlocks[node.NodeID]; ok {
			olricServers = append(olricServers, fmt.Sprintf("%s:%d", node.InternalIP, pb.OlricHTTPPort))
		}
	}

	for _, node := range nodes {
		pb, ok := portBlocks[node.NodeID]
		if !ok {
			cm.logger.Warn("No port block for node, skipping gateway restart",
				zap.String("node_id", node.NodeID))
			continue
		}

		// Build gateway config with WebRTC fields
		webrtcEnabled := false
		sfuPort := 0
		if sfuBlocks != nil {
			if sfuBlock, ok := sfuBlocks[node.NodeID]; ok {
				webrtcEnabled = true
				sfuPort = sfuBlock.SFUSignalingPort
			}
		}

		cfg := gateway.InstanceConfig{
			Namespace:             cluster.NamespaceName,
			NodeID:                node.NodeID,
			HTTPPort:              pb.GatewayHTTPPort,
			BaseDomain:            cm.baseDomain,
			RQLiteDSN:             fmt.Sprintf("http://localhost:%d", pb.RQLiteHTTPPort),
			GlobalRQLiteDSN:       cm.globalRQLiteDSN,
			OlricServers:          olricServers,
			OlricTimeout:          30 * time.Second,
			IPFSClusterAPIURL:     cm.ipfsClusterAPIURL,
			IPFSAPIURL:            cm.ipfsAPIURL,
			IPFSTimeout:           cm.ipfsTimeout,
			IPFSReplicationFactor: cm.ipfsReplicationFactor,
			WebRTCEnabled:         webrtcEnabled,
			SFUPort:               sfuPort,
			TURNDomain:            turnDomain,
			TURNSecret:            turnSecret,
		}

		if node.NodeID == cm.localNodeID {
			if err := cm.systemdSpawner.RestartGateway(ctx, cluster.NamespaceName, node.NodeID, cfg); err != nil {
				cm.logger.Error("Failed to restart local gateway with WebRTC config",
					zap.String("namespace", cluster.NamespaceName),
					zap.String("node_id", node.NodeID),
					zap.Error(err))
			} else {
				cm.logger.Info("Restarted local gateway with WebRTC config",
					zap.String("namespace", cluster.NamespaceName),
					zap.Bool("webrtc_enabled", webrtcEnabled))
			}
		} else {
			cm.restartGatewayRemote(ctx, node.InternalIP, cfg)
		}
	}
}

// restartGatewayRemote sends a restart-gateway request to a remote node.
func (cm *ClusterManager) restartGatewayRemote(ctx context.Context, nodeIP string, cfg gateway.InstanceConfig) {
	ipfsTimeout := ""
	if cfg.IPFSTimeout > 0 {
		ipfsTimeout = cfg.IPFSTimeout.String()
	}
	olricTimeout := ""
	if cfg.OlricTimeout > 0 {
		olricTimeout = cfg.OlricTimeout.String()
	}

	_, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":                    "restart-gateway",
		"namespace":                 cfg.Namespace,
		"node_id":                   cfg.NodeID,
		"gateway_http_port":         cfg.HTTPPort,
		"gateway_base_domain":       cfg.BaseDomain,
		"gateway_rqlite_dsn":        cfg.RQLiteDSN,
		"gateway_global_rqlite_dsn": cfg.GlobalRQLiteDSN,
		"gateway_olric_servers":     cfg.OlricServers,
		"gateway_olric_timeout":     olricTimeout,
		"ipfs_cluster_api_url":      cfg.IPFSClusterAPIURL,
		"ipfs_api_url":              cfg.IPFSAPIURL,
		"ipfs_timeout":              ipfsTimeout,
		"ipfs_replication_factor":   cfg.IPFSReplicationFactor,
		"gateway_webrtc_enabled":    cfg.WebRTCEnabled,
		"gateway_sfu_port":          cfg.SFUPort,
		"gateway_turn_domain":       cfg.TURNDomain,
		"gateway_turn_secret":       cfg.TURNSecret,
	})
	if err != nil {
		cm.logger.Error("Failed to restart remote gateway with WebRTC config",
			zap.String("node_ip", nodeIP),
			zap.String("namespace", cfg.Namespace),
			zap.Error(err))
	} else {
		cm.logger.Info("Restarted remote gateway with WebRTC config",
			zap.String("node_ip", nodeIP),
			zap.String("namespace", cfg.Namespace),
			zap.Bool("webrtc_enabled", cfg.WebRTCEnabled))
	}
}
