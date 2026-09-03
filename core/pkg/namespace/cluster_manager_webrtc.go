package namespace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"github.com/DeBrosOfficial/network/pkg/systemd"
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

	// Encrypt TURN secret before storing in RQLite
	storedSecret := turnSecret
	if cm.turnEncryptionKey != nil {
		encrypted, encErr := secrets.Encrypt(turnSecret, cm.turnEncryptionKey)
		if encErr != nil {
			return fmt.Errorf("failed to encrypt TURN secret: %w", encErr)
		}
		storedSecret = encrypted
	}

	// 4. Insert namespace_webrtc_config
	webrtcConfigID := uuid.New().String()
	_, err = cm.db.Exec(internalCtx,
		`INSERT INTO namespace_webrtc_config (id, namespace_cluster_id, namespace_name, enabled, turn_shared_secret, turn_credential_ttl, sfu_node_count, turn_node_count, enabled_by, enabled_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		webrtcConfigID, cluster.ID, namespaceName,
		storedSecret, DefaultTURNCredentialTTL,
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

	// Bugboard #283: record how many TURN nodes we ACTUALLY got, not how many we
	// asked for. The config row is inserted with the requested count before
	// selection runs, so webrtc/status reported turn_node_count: 2 while only one
	// relay existed — the same allocate-and-report-without-verifying pattern as
	// bugboard #277.
	if len(turnNodes) != DefaultTURNNodeCount {
		if _, err := cm.db.Exec(ctx,
			`UPDATE namespace_webrtc_config SET turn_node_count = ? WHERE namespace_cluster_id = ?`,
			len(turnNodes), cluster.ID); err != nil {
			cm.logger.Warn("Failed to record actual TURN node count",
				zap.String("namespace", namespaceName), zap.Error(err))
		}
	}

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

	// 11. Bring TURN up for this namespace.
	//
	// TURN is host-level since bugboard #283 part 2: one shared server per node
	// serves every namespace allocated there, so this is not a per-node spawn.
	// Each host derives its own tenant set from the allocations just written and
	// applies it — locally right now, and on every other selected node within one
	// reconcile tick. Config is never pushed between hosts: a host owning its own
	// TURN config is what makes the shared server safe to change concurrently.
	//
	// The namespace therefore gains its remote relays shortly after this returns
	// rather than during it. That is safe because a TURN DNS record is published
	// only once the shared server is actually serving, so clients are never
	// pointed at a relay that is not yet up.
	cm.ReconcileHostTURN(ctx)
	if len(turnNodes) > 0 {
		cm.logger.Info("TURN allocated; hosts converge on their next reconcile",
			zap.String("namespace", namespaceName),
			zap.Int("turn_nodes", len(turnNodes)))

		for _, node := range turnNodes {
			blk := turnBlocks[node.NodeID]
			cm.logEvent(ctx, cluster.ID, EventTURNStarted, node.NodeID,
				fmt.Sprintf("TURN allocated on %s (relay ports %d-%d), served by that host's shared TURN server",
					node.NodeID, blk.TURNRelayPortStart, blk.TURNRelayPortEnd), nil)
		}
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
	cm.updateClusterStateWithWebRTC(ctx, cluster, clusterNodes, sfuBlocks, turnBlocks, turnDomain, "", turnSecret)

	// 15. Restart namespace gateways with WebRTC config so they register WebRTC routes
	cm.restartGatewaysWithWebRTC(ctx, cluster, clusterNodes, nodePortBlocks, sfuBlocks, turnDomain, "", turnSecret)

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

	// 7. Delete TURN DNS records (both the regular and the feat-124 stealth
	// records — a full WebRTC teardown must not orphan stealth A records when
	// the namespace had stealth enabled). Delete-by-tag is a no-op when the
	// stealth records are absent, so this is safe unconditionally.
	if err := cm.dnsManager.DeleteTURNRecords(ctx, namespaceName); err != nil {
		cm.logger.Warn("Failed to delete TURN DNS records", zap.Error(err))
	}
	if err := cm.dnsManager.DeleteStealthTURNRecords(ctx, namespaceName); err != nil {
		cm.logger.Warn("Failed to delete stealth TURN DNS records", zap.Error(err))
	}

	// 8. Clean up DB tables
	cm.db.Exec(internalCtx, `DELETE FROM webrtc_rooms WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(internalCtx, `DELETE FROM namespace_webrtc_config WHERE namespace_cluster_id = ?`, cluster.ID)

	// 9. Update cluster-state.json to remove WebRTC info
	cm.updateClusterStateWithWebRTC(ctx, cluster, clusterNodes, nil, nil, "", "", "")

	// 10. Restart namespace gateways without WebRTC config so they unregister WebRTC routes
	portBlocks, err := cm.portAllocator.GetAllPortBlocks(ctx, cluster.ID)
	if err == nil {
		nodePortBlocks := make(map[string]*PortBlock)
		for i := range portBlocks {
			nodePortBlocks[portBlocks[i].NodeID] = &portBlocks[i]
		}
		cm.restartGatewaysWithWebRTC(ctx, cluster, clusterNodes, nodePortBlocks, nil, "", "", "")
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
// Transparently decrypts the TURN shared secret if it was encrypted at rest.
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

	// Decrypt TURN secret if encrypted (handles plaintext passthrough for backward compat)
	if cm.turnEncryptionKey != nil && secrets.IsEncrypted(configs[0].TURNSharedSecret) {
		decrypted, decErr := secrets.Decrypt(configs[0].TURNSharedSecret, cm.turnEncryptionKey)
		if decErr != nil {
			return nil, fmt.Errorf("failed to decrypt TURN secret: %w", decErr)
		}
		configs[0].TURNSharedSecret = decrypted
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

// getLocalNodePublicIP resolves this node's public IP from its dns_nodes record.
// The boot-time TURN restore path historically passed an empty PublicIP relying
// on a "spawner resolves it" promise that never existed, producing a TURN
// config that crash-loops on `public_ip must not be empty` (bugboard #846).
// Returns an actionable error if no public IP is recorded so the caller can
// skip the spawn instead of crash-looping.
func (cm *ClusterManager) getLocalNodePublicIP(ctx context.Context) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	var rows []struct {
		IP string `db:"ip_address"`
	}
	if err := cm.db.Query(internalCtx, &rows, `SELECT ip_address FROM dns_nodes WHERE id = ?`, cm.localNodeID); err != nil {
		return "", fmt.Errorf("failed to query public IP for local node %s: %w", cm.localNodeID, err)
	}
	if len(rows) == 0 || rows[0].IP == "" {
		return "", fmt.Errorf("no public IP recorded for local node %s in dns_nodes", cm.localNodeID)
	}
	return rows[0].IP, nil
}

// selectTURNNodes selects up to count nodes to run TURN for a namespace.
//
// Bugboard #283, part 2: TURN binds the fixed ports 3478/5349, which are
// exclusive per HOST. This used to skip any host already running TURN for
// another namespace — correct at the time (a second per-namespace TURN unit
// crash-looped on bind), but it capped a namespace at the hosts no one else had
// taken, so on a 3-node fleet the second namespace to enable WebRTC got one
// relay and no redundancy.
//
// A single shared TURN server now serves every namespace on a host, so
// occupancy is no longer a reason to skip a node and the check is gone. Which
// namespaces a host serves is reconciled by ReconcileHostTURN.
func (cm *ClusterManager) selectTURNNodes(ctx context.Context, nodes []clusterNodeInfo, count int) []clusterNodeInfo {
	result := make([]clusterNodeInfo, 0, count)
	for _, node := range nodes {
		if len(result) >= count {
			break
		}
		result = append(result, node)
	}

	if len(result) < count {
		cm.logger.Warn("Fewer TURN nodes available than requested",
			zap.Int("requested", count), zap.Int("selected", len(result)))
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
		"action":          "spawn-sfu",
		"namespace":       cfg.Namespace,
		"node_id":         cfg.NodeID,
		"sfu_listen_addr": cfg.ListenAddr,
		"sfu_media_start": cfg.MediaPortStart,
		"sfu_media_end":   cfg.MediaPortEnd,
		"turn_servers":    turnServers,
		"turn_secret":     cfg.TURNSecret,
		"turn_cred_ttl":   cfg.TURNCredTTL,
		"rqlite_dsn":      cfg.RQLiteDSN,
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
	turnDomain, turnStealthDomain, turnSecret string,
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
		state.TURNStealthDomain = turnStealthDomain
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
	turnDomain, turnStealthDomain, turnSecret string,
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
			TURNStealthDomain:     turnStealthDomain,
			TURNSecret:            turnSecret,
			// Bugboard #837 follow-up: preserve the secrets key on WebRTC
			// restarts so enabling WebRTC doesn't drop secrets management.
			SecretsEncryptionKey: cm.secretsEncryptionKey,
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
		"action":                      "restart-gateway",
		"namespace":                   cfg.Namespace,
		"node_id":                     cfg.NodeID,
		"gateway_http_port":           cfg.HTTPPort,
		"gateway_base_domain":         cfg.BaseDomain,
		"gateway_rqlite_dsn":          cfg.RQLiteDSN,
		"gateway_global_rqlite_dsn":   cfg.GlobalRQLiteDSN,
		"gateway_olric_servers":       cfg.OlricServers,
		"gateway_olric_timeout":       olricTimeout,
		"ipfs_cluster_api_url":        cfg.IPFSClusterAPIURL,
		"ipfs_api_url":                cfg.IPFSAPIURL,
		"ipfs_timeout":                ipfsTimeout,
		"ipfs_replication_factor":     cfg.IPFSReplicationFactor,
		"gateway_webrtc_enabled":      cfg.WebRTCEnabled,
		"gateway_sfu_port":            cfg.SFUPort,
		"gateway_turn_domain":         cfg.TURNDomain,
		"gateway_turn_stealth_domain": cfg.TURNStealthDomain,
		"gateway_turn_secret":         cfg.TURNSecret,
		// Bugboard #837 follow-up: preserve the secrets key on WebRTC restarts.
		"gateway_secrets_encryption_key": cfg.SecretsEncryptionKey,
		// Bugboard #274: preserve the ntfy base URL across WebRTC restarts.
		"gateway_ntfy_base_url": cfg.NtfyBaseURL,
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

// --- WebRTC role reconciliation (bugboard #161) ---
//
// Node replacement migrates the CORE roles (gateway, olric, rqlite) to the
// replacement node but leaves `webrtc_port_allocations` pointing at the node that
// left. The namespace then holds its TURN/SFU roles on a machine set that no
// longer matches its cluster membership. Observed on devnet anchat-test: one of
// two TURN roles sat on a node removed weeks earlier (so only ONE live relay, no
// redundancy), the replacement node held no WebRTC role at all (so its SFU config
// had no ports to source and crash-looped ~300k times), and a node that had left
// the cluster still held an SFU role.

// webrtcAllocRef identifies one WebRTC role assignment.
type webrtcAllocRef struct {
	NodeID      string
	ServiceType string // "turn" | "sfu"
}

// webrtcReallocationPlan is the decision produced by planWebRTCReallocation.
type webrtcReallocationPlan struct {
	Deallocate   []webrtcAllocRef // roles held by nodes that are no longer live members
	AllocateSFU  []string         // live members that must gain an SFU role
	AllocateTURN []string         // live members that must gain a TURN role
}

func (p webrtcReallocationPlan) empty() bool {
	return len(p.Deallocate) == 0 && len(p.AllocateSFU) == 0 && len(p.AllocateTURN) == 0
}

// planWebRTCReallocation decides how to bring WebRTC role assignments back in
// line with live cluster membership. Pure so the decision is unit-testable
// without a cluster: given the current allocations, the live member IDs, and the
// desired TURN fan-out, it returns what to drop and what to add.
//
// Rules:
//   - Any role held by a node that is not a VIABLE member is dropped.
//   - Every live member should hold an SFU role (SFU runs on all nodes).
//   - TURN runs on min(desiredTURN, len(viable)) members — never more, so a
//     shrunken cluster does not over-provision relays.
//
// Candidate selection is sorted so every node computes the identical plan; that
// is what lets the coordinator election below be a pure local decision.
func planWebRTCReallocation(current []webrtcAllocRef, memberNodeIDs, liveNodeIDs []string, desiredTURN int) webrtcReallocationPlan {
	// TWO distinct sets, deliberately — but NOT the raw namespace_cluster_nodes
	// row set the parameter name suggests. Production passes:
	//   members (memberNodeIDs) — the VIABLE set from getWebRTCMemberStatus:
	//             members that are currently active OR were seen within
	//             webrtcMemberGracePeriod. Losing viability is what revokes a
	//             role here. The raw namespace_cluster_nodes row survives
	//             much longer than this — see pruneStaleClusterNodes
	//             (cluster_recovery.go, bugboard #173) for the row's own,
	//             longer-horizon removal.
	//   live    — the subset of the SAME read whose dns_nodes.status is
	//             'active'. Used solely to pick ALLOCATION targets.
	// Keying revocation on raw liveness (dns_nodes.status alone, no grace
	// window) would let a 120s heartbeat gap (a rolling restart, a brief
	// rqlite stall) strip a healthy node's roles — the exact thrash this
	// reconciler exists to prevent.
	//
	// Both sets are evaluated against `datetime('now', ...)` at READ time
	// (getWebRTCMemberStatus / cluster_manager_webrtc.go), not a stored,
	// stable snapshot — so two coordinators computing a plan on either side of
	// a member's exact grace-boundary instant CAN see different viable sets
	// and therefore different plans. This is bounded, not oscillating: a
	// member's viability only ages out ONCE (last_seen never moves backward,
	// so once it crosses the boundary it stays excluded on every later read)
	// or jumps straight back to fully viable on its next heartbeat (no
	// partial/hovering state) — so at most one sweep around the transition
	// can disagree with the next, and the following sweep converges. The
	// trim-surplus-TURN path below produces the identical, deterministic plan
	// from whatever set the coordinator for that sweep actually saw, so
	// convergence holds even though the two sets are not literally identical
	// across every possible pair of sweeps.
	member := make(map[string]bool, len(memberNodeIDs))
	for _, id := range memberNodeIDs {
		member[id] = true
	}
	live := make(map[string]bool, len(liveNodeIDs))
	for _, id := range liveNodeIDs {
		live[id] = true
	}

	var plan webrtcReallocationPlan
	hasSFU := map[string]bool{}
	hasTURN := map[string]bool{}
	for _, a := range current {
		if !member[a.NodeID] {
			plan.Deallocate = append(plan.Deallocate, a)
			continue
		}
		switch a.ServiceType {
		case "sfu":
			hasSFU[a.NodeID] = true
		case "turn":
			hasTURN[a.NodeID] = true
		}
	}
	sort.Slice(plan.Deallocate, func(i, j int) bool {
		if plan.Deallocate[i].NodeID != plan.Deallocate[j].NodeID {
			return plan.Deallocate[i].NodeID < plan.Deallocate[j].NodeID
		}
		return plan.Deallocate[i].ServiceType < plan.Deallocate[j].ServiceType
	})

	sorted := append([]string(nil), liveNodeIDs...)
	sort.Strings(sorted)

	// SFU on every live member. The member[] check is defense-in-depth rather
	// than load-bearing today: production derives memberNodeIDs and
	// liveNodeIDs from a single read (getWebRTCMemberStatus, bugboard #170),
	// so live is now structurally a subset of member and this is always true
	// for production callers. It is kept because planWebRTCReallocation is a
	// pure function exercised directly in tests with hand-built inputs, and a
	// caller that ever passes an inconsistent pair (live containing an id
	// absent from member) must not have that id both dropped above and
	// silently re-added here in the same pass.
	for _, id := range sorted {
		if member[id] && !hasSFU[id] {
			plan.AllocateSFU = append(plan.AllocateSFU, id)
		}
	}

	// TURN up to the desired fan-out. Both the cap and the current count are
	// measured over MEMBERS, not live nodes: a member that is briefly down still
	// holds its role, so counting only live holders would allocate a replacement
	// and leave the namespace over-provisioned (3 relays) once it returns. A node
	// that is genuinely gone loses membership, which frees its role above.
	want := desiredTURN
	if want > len(memberNodeIDs) {
		want = len(memberNodeIDs)
	}
	have := 0
	for id := range hasTURN {
		if member[id] {
			have++
		}
	}
	for _, id := range sorted {
		if have >= want {
			break
		}
		if member[id] && !hasTURN[id] {
			plan.AllocateTURN = append(plan.AllocateTURN, id)
			have++
		}
	}

	// Trim surplus TURN. Two nodes in overlapping majorities can both self-elect
	// and each add a relay, pushing past the desired fan-out; nothing else would
	// ever remove the excess, and every extra relay locks 3478/5349 against other
	// namespaces on that host. Drop the highest-sorted holders so the choice is
	// identical on every node.
	if have > want {
		holders := make([]string, 0, len(hasTURN))
		for id := range hasTURN {
			if member[id] {
				holders = append(holders, id)
			}
		}
		sort.Strings(holders)
		for i := len(holders) - 1; i >= 0 && have > want; i-- {
			plan.Deallocate = append(plan.Deallocate, webrtcAllocRef{NodeID: holders[i], ServiceType: "turn"})
			have--
		}
	}
	return plan
}

// webrtcReconcileQuorumOK reports whether a node may reshape role assignments
// from the membership view it currently sees.
//
// A strict majority is required so a partitioned or heartbeat-starved minority
// can never conclude it is the last survivor and pull every role onto itself.
// Pure, so the arithmetic that stands between a partition and a role stampede is
// actually testable.
func webrtcReconcileQuorumOK(live, members int) bool {
	if live == 0 {
		return false
	}
	return live*2 > members
}

// webrtcReconcileCoordinator picks the single node that may apply a reallocation
// plan: the lowest-sorted live member.
//
// Allocation is cluster-wide state, so letting every node reconcile concurrently
// could double-allocate a role. This is a deterministic election — every node
// computes the same answer from the same membership list with no coordination,
// no lock, and no leader lookup — so exactly one applies the plan and the rest
// no-op. Mirrors the bootstrap election used for the namespace rqlite leader.
func webrtcReconcileCoordinator(liveNodeIDs []string) string {
	if len(liveNodeIDs) == 0 {
		return ""
	}
	sorted := append([]string(nil), liveNodeIDs...)
	sort.Strings(sorted)
	return sorted[0]
}

// webrtcMemberGracePeriod bounds how long a cluster member may be non-live
// before its WebRTC roles are treated as abandoned and released to a live
// node.
//
// Node replacement (ReplaceClusterNode) and cluster repair (RepairCluster)
// are both supposed to keep namespace_cluster_nodes in sync with reality, but
// the bugboard #161 postmortem found a departed node's row left behind
// indefinitely on live devnet: RepairCluster only ever ADDS members without
// removing stale ones, and the dead-node health monitor had not fired for
// every node that actually died (bugboard #173 traced this to a race between
// the DNS heartbeat loop and the ring-based health monitor — see
// pruneStaleClusterNodes in cluster_recovery.go). A membership row like that
// inflates both the quorum denominator and the "already has a role" count
// forever, so live members could never reclaim TURN/SFU capacity — the
// reconciler was permanently deadlocked by data it had no way to age out.
// pruneStaleClusterNodes now removes that row outright on a much longer
// horizon (clusterNodePurgeStaleAfter, 15m); this grace period is the
// SHORTER-lived signal used to decide role HOLDING in the meantime, well
// before the row itself is eligible for removal.
//
// Set well above a single node's restart/rolling-upgrade window (many
// multiples of webrtcReconcileInterval) so a routine restart never triggers a
// role migration — only a member still gone after several sweeps is treated
// as abandoned.
const webrtcMemberGracePeriod = 10 * time.Minute

// webrtcViableMemberSQL selects cluster members whose WebRTC roles should
// still count as held: members that are currently active, or that have been
// seen within webrtcMemberGracePeriod. Also selects dn.status so a single
// execution can serve both the viable set (every returned row) AND the live
// subset (rows with status = 'active') — see getWebRTCMemberStatus. Exported
// as a query constant (matching the ensureTURNRecordSQL /
// ensureNamespaceHostRecordSQL pattern in dns_manager.go) so the exact
// production query is what gets exercised in tests. Args: clusterID, "-N
// seconds" (webrtcMemberGracePeriod as a SQLite datetime modifier).
const webrtcViableMemberSQL = `
	SELECT ncn.node_id, dn.status
	FROM namespace_cluster_nodes ncn
	JOIN dns_nodes dn ON ncn.node_id = dn.id
	WHERE ncn.namespace_cluster_id = ?
	  AND (dn.status = 'active' OR dn.last_seen > datetime('now', ?))
	GROUP BY ncn.node_id
`

// webrtcMemberStatusRow is one row of webrtcViableMemberSQL.
type webrtcMemberStatusRow struct {
	NodeID string `db:"node_id"`
	Status string `db:"status"`
}

// getWebRTCMemberStatus returns the viable and live member sets for a
// cluster from a SINGLE query (bugboard #170), so live is structurally a
// subset of viable: both are derived from the exact same DB read, rather than
// two separate reads that a node's status could flip between (a node
// transitioning active -> inactive right between them used to be able to
// land in "live" without being in "viable", which the quorum check assumes
// can never happen).
//
// A member down longer than webrtcMemberGracePeriod is excluded from viable
// (and therefore from live too) even though its namespace_cluster_nodes row
// still exists — that row cannot be trusted to have been cleaned up
// (bugboard #161; pruneStaleClusterNodes now does eventually clean it up, on
// a longer horizon — see cluster_recovery.go, bugboard #173).
func (cm *ClusterManager) getWebRTCMemberStatus(ctx context.Context, clusterID string) (viable, live []string, err error) {
	internalCtx := client.WithInternalAuth(ctx)
	var rows []webrtcMemberStatusRow
	graceModifier := fmt.Sprintf("-%d seconds", int(webrtcMemberGracePeriod.Seconds()))
	if err := cm.db.Query(internalCtx, &rows, webrtcViableMemberSQL, clusterID, graceModifier); err != nil {
		return nil, nil, err
	}
	viable = make([]string, 0, len(rows))
	live = make([]string, 0, len(rows))
	for _, r := range rows {
		viable = append(viable, r.NodeID)
		if r.Status == "active" {
			live = append(live, r.NodeID)
		}
	}
	return viable, live, nil
}

// webrtcRawMemberCountSQL counts every namespace_cluster_nodes row for a
// cluster, independent of dns_nodes entirely. This is the denominator
// webrtcReconcileMajorityHeld (bugboard #171) checks the viable set against,
// so a majority of RECORDED membership can never silently age out of the
// viable set within a single sweep and still be treated as authoritative.
const webrtcRawMemberCountSQL = `SELECT COUNT(DISTINCT node_id) AS count FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`

// getRawClusterMemberCount returns the total number of distinct nodes
// recorded as members of a cluster, regardless of dns_nodes status.
func (cm *ClusterManager) getRawClusterMemberCount(ctx context.Context, clusterID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)
	var rows []struct {
		Count int `db:"count"`
	}
	if err := cm.db.Query(internalCtx, &rows, webrtcRawMemberCountSQL, clusterID); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

// webrtcReconcileMajorityHeld reports whether the viable set still represents
// a majority of every recorded member (bugboard #171).
//
// webrtcReconcileQuorumOK alone stops being a useful safeguard once live and
// viable are both derived from the same liveness signal (bugboard #170's
// fix): past webrtcMemberGracePeriod, viable can never exceed live by much,
// so live*2 > viable can only fail when viable itself is inflated beyond
// live — which is exactly the situation a lone surviving node after a
// cluster-wide outage does NOT produce (viable shrinks to match it). A LONE
// node always "passes" 1*2 > 1. This second, independent check bounds how
// much of the RAW recorded membership may have aged out of the viable set at
// once: if fewer than half of it is still viable, that is a mass outage (or
// its tail), not a routine departure, and the survivor(s) must not act as if
// they were the whole cluster.
func webrtcReconcileMajorityHeld(viable, rawMembers int) bool {
	return viable >= (rawMembers+1)/2
}

// webrtcReconcileStartupGrace bounds how soon after this node's own process
// start it will act as WebRTC reconcile coordinator (bugboard #171). Mirrors
// the reasoning behind health.DefaultStartupGracePeriod
// (pkg/node/health/monitor.go, 5m — cannot import directly, pkg/node imports
// pkg/namespace, see cluster_manager.go's startedAt field doc): a node that
// has JUST come back up has not yet had time to observe its peers report back
// in, so its very first read of cluster membership can look exactly like "I
// am the only survivor" during totally routine startup, not an outage.
const webrtcReconcileStartupGrace = 5 * time.Minute

// ReconcileWebRTCAllocations brings a namespace's WebRTC role assignments back in
// line with its live cluster membership (bugboard #161).
//
// Applied by ONE node per sweep — the lowest-sorted live member — because
// allocations are cluster-wide state and concurrent reconcilers could
// double-allocate a role. Every node computes the same coordinator locally, so
// this needs no lock and no leader lookup.
//
// Only the DB assignment is changed here. Each node converges its own local
// TURN/SFU services to its own allocations on the restore path, so a node that
// gains a role spawns it and a node that loses one stops advertising it. Doing it
// this way avoids the blunt alternative (disable+enable WebRTC), which
// regenerates the namespace TURN secret and would invalidate every client
// credential in flight and drop active calls.
//
// Idempotent: a healthy cluster produces an empty plan and writes nothing.
func (cm *ClusterManager) ReconcileWebRTCAllocations(ctx context.Context, clusterID, namespaceName string, desiredTURN int) error {
	// bugboard #171: a node that has not been running long enough to have
	// observed its peers cannot be trusted to reconcile or coordinate — see
	// webrtcReconcileStartupGrace. time.Since of a zero-valued startedAt (a
	// ClusterManager built directly, as tests do, rather than via
	// NewClusterManager) is enormous, so this is a no-op unless a test
	// deliberately sets startedAt to simulate a fresh boot.
	if since := time.Since(cm.startedAt); since < webrtcReconcileStartupGrace {
		cm.logger.Debug("WebRTC reconcile skipped: this node is inside its startup grace period",
			zap.String("namespace", namespaceName),
			zap.Duration("since_start", since))
		return nil
	}

	viableIDs, liveIDs, err := cm.getWebRTCMemberStatus(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("get cluster member status for webrtc reconcile: %w", err)
	}
	if len(viableIDs) == 0 {
		cm.logger.Warn("WebRTC reconcile skipped: no viable cluster members recorded — refusing to treat this as grounds for mass deallocation",
			zap.String("namespace", namespaceName))
		return nil
	}

	// Quorum floor: never reshape roles from a minority view. A partition or a
	// cluster-wide heartbeat stall must not let one node conclude it is the only
	// survivor and pull every role onto itself.
	//
	// The denominator is VIABLE members (live, or seen within the grace
	// period), not every historical namespace_cluster_nodes row — a stale row
	// for a node that is never coming back must not be able to make this
	// quorum permanently unreachable (bugboard #161: exactly that happened,
	// silently, forever, until this log line existed).
	if !webrtcReconcileQuorumOK(len(liveIDs), len(viableIDs)) {
		cm.logger.Warn("WebRTC reconcile skipped: no quorum — live node count is not a strict majority of viable cluster members",
			zap.String("namespace", namespaceName),
			zap.Int("live", len(liveIDs)),
			zap.Int("viable_members", len(viableIDs)))
		return nil
	}

	// bugboard #171 regression guard — see webrtcReconcileMajorityHeld doc.
	rawMemberCount, err := cm.getRawClusterMemberCount(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("get raw cluster member count for webrtc reconcile: %w", err)
	}
	if !webrtcReconcileMajorityHeld(len(viableIDs), rawMemberCount) {
		cm.logger.Warn("WebRTC reconcile skipped: majority of recorded cluster membership is not viable — refusing to reallocate off what looks like a mass outage",
			zap.String("namespace", namespaceName),
			zap.Int("viable_members", len(viableIDs)),
			zap.Int("raw_members", rawMemberCount))
		return nil
	}

	if coordinator := webrtcReconcileCoordinator(liveIDs); coordinator != cm.localNodeID {
		cm.logger.Debug("WebRTC reconcile skipped: this node is not the elected coordinator for this sweep",
			zap.String("namespace", namespaceName),
			zap.String("coordinator", coordinator),
			zap.String("local_node", cm.localNodeID))
		return nil
	}

	if desiredTURN <= 0 {
		desiredTURN = DefaultTURNNodeCount
	}

	blocks, err := cm.webrtcPortAllocator.GetAllPorts(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("get webrtc allocations for reconcile: %w", err)
	}
	current := make([]webrtcAllocRef, 0, len(blocks))
	for _, b := range blocks {
		current = append(current, webrtcAllocRef{NodeID: b.NodeID, ServiceType: b.ServiceType})
	}

	plan := planWebRTCReallocation(current, viableIDs, liveIDs, desiredTURN)

	// Mirrors planWebRTCReallocation's own cap (want = min(desiredTURN,
	// len(memberNodeIDs)), memberNodeIDs here being viableIDs) rather than
	// liveIDs — the old condition checked liveIDs, which the planner never
	// actually caps on, so it could warn ("cannot reach desired count") in
	// cases the planner had already fully satisfied, and stay silent in cases
	// it hadn't. Logged at Info, not Warn: a namespace with fewer than
	// desiredTURN viable members is an expected steady state (e.g. a 2-node
	// cluster), not an actionable problem, and this fires on every sweep for
	// as long as it's true.
	if len(viableIDs) < desiredTURN {
		cm.logger.Info("WebRTC TURN fan-out is capped by viable membership, below the desired count",
			zap.String("namespace", namespaceName),
			zap.Int("desired_turn", desiredTURN),
			zap.Int("viable_members", len(viableIDs)))
	}

	if plan.empty() {
		return nil
	}

	cm.logger.Info("WebRTC role assignments drifted from cluster membership — reallocating (bugboard #161)",
		zap.String("namespace", namespaceName),
		zap.Int("deallocate", len(plan.Deallocate)),
		zap.Strings("allocate_sfu", plan.AllocateSFU),
		zap.Strings("allocate_turn", plan.AllocateTURN))

	var errs []error
	for _, d := range plan.Deallocate {
		if derr := cm.webrtcPortAllocator.DeallocateByNode(ctx, clusterID, d.NodeID, d.ServiceType); derr != nil {
			errs = append(errs, fmt.Errorf("deallocate %s on %s: %w", d.ServiceType, d.NodeID, derr))
		}
	}
	for _, id := range plan.AllocateSFU {
		if _, aerr := cm.webrtcPortAllocator.AllocateSFUPorts(ctx, id, clusterID); aerr != nil {
			errs = append(errs, fmt.Errorf("allocate sfu on %s: %w", id, aerr))
		}
	}
	// No host-occupancy check here (bugboard #283 part 2). TURN still binds the
	// fixed 3478/5349, but a single shared server per host now serves every
	// namespace allocated there, so a host already relaying for someone else can
	// take this namespace too. Skipping such hosts — which this did, matching
	// selectTURNNodes at the time — is what left the second namespace on a fleet
	// with one relay and no redundancy after a node replacement.
	for _, id := range plan.AllocateTURN {
		if _, aerr := cm.webrtcPortAllocator.AllocateTURNPorts(ctx, id, clusterID); aerr != nil {
			errs = append(errs, fmt.Errorf("allocate turn on %s: %w", id, aerr))
		}
	}
	return errors.Join(errs...)
}

// webrtcReconcileInterval is how often each node re-checks WebRTC role
// assignments. Long enough that a rolling restart settles between sweeps, short
// enough that a node replacement converges in about a minute.
const webrtcReconcileInterval = 60 * time.Second

// ensureNamespaceHostRecordIfServing re-asserts this node's `ns-<namespace>` DNS
// record, but only while its namespace gateway is actually answering (bugboard
// #286).
//
// Advertising is gated on real evidence rather than on the node merely believing
// it holds the role: a record pointing at a gateway that is down is the #161
// symptom in a different place — clients round-robin onto a dead endpoint.
func (cm *ClusterManager) ensureNamespaceHostRecordIfServing(ctx context.Context, state *ClusterLocalState) {
	port := state.LocalPorts.GatewayHTTPPort
	if port <= 0 {
		return
	}
	if err := probeTCP(fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		// Not serving yet — say nothing and retry on the next tick. This is the
		// normal state for the first minute after a restart.
		return
	}

	pip, perr := cm.getLocalNodePublicIP(ctx)
	if perr != nil || pip == "" {
		cm.logger.Warn("Cannot re-advertise namespace host DNS record: public IP unavailable",
			zap.String("namespace", state.NamespaceName), zap.Error(perr))
		return
	}
	if derr := cm.dnsManager.EnsureNamespaceHostRecordActiveForNode(ctx, state.NamespaceName, pip); derr != nil {
		cm.logger.Warn("Periodic namespace host DNS ensure failed",
			zap.String("namespace", state.NamespaceName), zap.Error(derr))
	}
}

// StartWebRTCReconciler runs the periodic WebRTC role reconcile until ctx is
// cancelled. Safe to call once at node boot.
//
// Scope: this sweep reconciles the DB assignments, STARTS a role this node has
// gained, STOPS one it no longer holds, and keeps the TURN DNS record current —
// so it converges in both directions without waiting for a restart.
//
// The stop side still demands positive evidence (see
// stopUnallocatedWebRTCServices) and the start side is backed off on failure: a
// reconciler that moves services between machines must never act on a guess in
// either direction.
func (cm *ClusterManager) StartWebRTCReconciler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(webrtcReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cm.reconcileWebRTCForLocalNamespaces(ctx)
			}
		}
	}()
}

// reconcileWebRTCForLocalNamespaces reconciles every namespace this node holds
// local state for: first the cluster-wide assignments (coordinator-gated), then
// this node's own services against its own allocations.
func (cm *ClusterManager) reconcileWebRTCForLocalNamespaces(ctx context.Context) {
	if cm.localNodeID == "" {
		return // cannot reason about "this node's" roles without an identity
	}
	pattern := filepath.Join(cm.baseDataDir, "*", "cluster-state.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	var turnNamespaces []turnDNSCandidate
	for _, path := range matches {
		if ctx.Err() != nil {
			return
		}
		state, lerr := loadLocalState(path)
		if lerr != nil || state == nil || state.ClusterID == "" {
			continue
		}
		// cluster-state.json is never validated against the DB. If a namespace was
		// deprovisioned and re-provisioned while this node's state file survived
		// (the delete fan-out is best-effort), state.ClusterID is stale — and a
		// lookup against a stale ID returns "no rows" CLEANLY, which the stop
		// sweep would read as positive evidence and use to stop a service the
		// node legitimately holds under the new cluster. Resolve identity from
		// the DB and skip on any mismatch.
		cluster, cerr := cm.GetClusterByNamespace(ctx, state.NamespaceName)
		if cerr != nil || cluster == nil || cluster.ID != state.ClusterID {
			continue
		}

		// Prune permanently-gone cluster-node rows before anything else reads
		// membership (bugboard #173). Unconditional — not WebRTC-specific, and
		// the ring-based dead-node health monitor cannot be relied on to ever
		// catch this itself (see pruneStaleClusterNodes doc comment for why).
		// Idempotent DELETE, so running it from every WebRTC-enabled node's
		// periodic sweep needs no coordinator election.
		if removed, perr := cm.pruneStaleClusterNodes(ctx, cluster.ID); perr != nil {
			cm.logger.Warn("Periodic stale cluster-node prune failed",
				zap.String("namespace", state.NamespaceName), zap.Error(perr))
		} else if len(removed) > 0 {
			cm.logger.Warn("Periodic sweep pruned permanently-gone cluster node assignments (bugboard #173)",
				zap.String("namespace", state.NamespaceName), zap.Strings("removed_nodes", removed))
			// Bugboard #280: membership just changed, so cluster-state.json — and
			// therefore the namespace gateway's olric_servers and rqlite join list —
			// now names nodes that are gone. Regeneration was only wired into
			// RepairCluster, which nothing calls for a cluster that is not degraded,
			// so a namespace could keep pointing at departed nodes indefinitely.
			// Doing it here gives it a periodic catch-up on every node.
			if rerr := cm.regenerateClusterState(ctx, cluster); rerr != nil {
				cm.logger.Warn("Periodic cluster-state regeneration failed — namespace config may still name departed nodes",
					zap.String("namespace", state.NamespaceName), zap.Error(rerr))
			}
		}

		// Bugboard #286: re-advertise this node in the `ns-<ns>` round-robin.
		//
		// The boot-time ensure needs this node's public IP from the MAIN rqlite,
		// which is not up that early in boot, so it routinely loses that race and
		// gives up — leaving a node that serves 200 absent from DNS for minutes
		// after every restart, with nothing reporting it. During a rolling upgrade
		// the lag compounds: at one point both devnet namespaces resolved to a
		// single node while two healthy nodes sat idle.
		//
		// By the time this sweep runs, the cluster lookup above has already proven
		// rqlite is reachable. Same discipline as the TURN ensure below: holding
		// the role is not sufficient — require the local gateway to be ACTUALLY
		// serving, so this can never advertise a node whose gateway is down.
		cm.ensureNamespaceHostRecordIfServing(ctx, state)

		webrtcCfg, werr := cm.GetWebRTCConfig(ctx, state.NamespaceName)
		if werr != nil || webrtcCfg == nil {
			continue // WebRTC not enabled for this namespace
		}
		if aerr := cm.ReconcileWebRTCAllocations(ctx, cluster.ID, state.NamespaceName, webrtcCfg.TURNNodeCount); aerr != nil {
			cm.logger.Warn("Periodic WebRTC allocation reconcile failed",
				zap.String("namespace", state.NamespaceName), zap.Error(aerr))
		}
		// Start what we hold, then stop what we don't — in that order, so a role
		// that MOVED to this node is serving before the DNS ensure below can
		// advertise it.
		cm.spawnAllocatedWebRTCServices(ctx, state, webrtcCfg)
		cm.stopUnallocatedWebRTCServices(ctx, state.ClusterID, state.NamespaceName)

		// Collect this namespace for the TURN DNS pass that runs after the
		// host-level TURN reconcile below. Advertising is deliberately NOT done
		// here: TURN is one shared server per host now (#283 part 2), so whether
		// this node relays at all is a host fact, not a per-namespace one, and it
		// has to be decided after that server is reconciled. Holding the
		// allocation is necessary but not sufficient — DNS must never advertise a
		// relay that is not up (the #161 symptom, clients round-robin onto a dead
		// endpoint).
		turnNamespaces = append(turnNamespaces, turnDNSCandidate{
			namespace: state.NamespaceName,
			clusterID: state.ClusterID,
			webrtcCfg: webrtcCfg,
		})
	}

	// TURN is host-level (bugboard #283 part 2): one shared server per node,
	// serving every namespace that holds an allocation here. Reconcile it ONCE,
	// after every namespace's allocations have been reconciled above, then
	// advertise — so a namespace that just gained TURN is serving before its DNS
	// record appears, which is the ordering #161 requires.
	served := cm.ReconcileHostTURN(ctx)
	cm.ensureTURNRecordsForServingNamespaces(ctx, turnNamespaces, served)
}

// turnDNSCandidate is a namespace on this node that may need its TURN DNS
// record advertised, collected during the sweep.
type turnDNSCandidate struct {
	namespace string
	clusterID string
	webrtcCfg *WebRTCConfig
}

// ensureTURNRecordsForServingNamespaces advertises this node's TURN record for
// each namespace it actually relays for.
//
// Holding the allocation is necessary but NOT sufficient: DNS must never
// advertise a relay that is not up, or clients round-robin onto a dead endpoint
// (the #161 symptom, recreated from the advertising side). Since #283 part 2 the
// evidence is the SHARED unit being active AND this namespace being one of the
// tenants it was actually configured with — the per-namespace unit this used to
// check no longer exists, and gating on it would silently stop advertising TURN
// for every namespace on the fleet.
func (cm *ClusterManager) ensureTURNRecordsForServingNamespaces(ctx context.Context, candidates []turnDNSCandidate, served []string) {
	if len(candidates) == 0 || len(served) == 0 || cm.systemdSpawner == nil || cm.systemdSpawner.systemdMgr == nil {
		return
	}
	// Advertise ONLY namespaces the shared server is actually configured to relay
	// for. Holding an allocation is not enough under the shared model: a
	// namespace with no shared secret is dropped from the config, and a failed
	// config write leaves the previous tenant set running — advertising on the
	// allocation alone points clients at a relay that rejects their credentials,
	// which is the #161 symptom recreated from the advertising side.
	relaying := make(map[string]bool, len(served))
	for _, ns := range served {
		relaying[ns] = true
	}
	turnUp, err := cm.systemdSpawner.systemdMgr.IsHostTURNActive()
	if err != nil || !turnUp {
		return
	}
	publicIP, perr := cm.getLocalNodePublicIP(ctx)
	if perr != nil || publicIP == "" {
		return
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return
		}
		if !relaying[c.namespace] {
			continue
		}
		blk, berr := cm.webrtcPortAllocator.GetTURNPorts(ctx, c.clusterID, cm.localNodeID)
		if berr != nil || blk == nil {
			continue
		}
		if derr := cm.dnsManager.EnsureTURNRecordForNode(ctx, c.namespace, publicIP,
			cm.stealthDomainFor(c.namespace, c.webrtcCfg)); derr != nil {
			cm.logger.Warn("Periodic TURN DNS ensure failed",
				zap.String("namespace", c.namespace), zap.Error(derr))
		}
	}
}

// stopUnallocatedWebRTCServices stops this node's TURN/SFU only when the
// allocator gives POSITIVE evidence that the allocation is gone.
//
// The distinction is load-bearing. An earlier version treated "query failed" and
// "no allocation" identically, so a transient main-rqlite failure — a leader
// election, a WireGuard blip — would stop TURN and SFU on every node at once.
// Because gaining a role only takes effect on the next restore (see
// StartWebRTCReconciler), nothing would restart them: a ten-second hiccup became
// a fleet-wide WebRTC outage. Stopping a live relay must never be easier than
// starting one, so an unreadable allocator means DO NOTHING.
func (cm *ClusterManager) stopUnallocatedWebRTCServices(ctx context.Context, clusterID, namespaceName string) {
	if cm.systemdSpawner == nil || cm.systemdSpawner.systemdMgr == nil {
		return
	}
	// A node that cannot identify itself holds NO positive evidence about itself.
	// GetTURNPorts(clusterID, "") is a perfectly clean read that returns no rows,
	// which the predicate below would otherwise read as "role revoked" and stop
	// every WebRTC service — permanently, since the restore path keys on the same
	// empty ID. Every other empty-ID path in this package fails closed; so must
	// this one.
	if cm.localNodeID == "" || clusterID == "" {
		cm.logger.Warn("Skipping WebRTC stop sweep: local node identity or cluster ID unknown — cannot distinguish \"no allocation\" from \"cannot ask\"",
			zap.String("namespace", namespaceName))
		return
	}
	// definitelyUnallocated reports true ONLY for a clean read that returned no
	// block. A read error yields false — leave the service alone.
	definitelyUnallocated := func(b *WebRTCPortBlock, err error) bool {
		return err == nil && b == nil
	}
	// TURN is not listed here: it is host-level since bugboard #283 part 2, so
	// losing an allocation drops this namespace from the SHARED server's tenant
	// list rather than stopping a per-namespace unit. ReconcileHostTURN derives
	// that list from the allocations and owns both directions.
	for _, svc := range []struct {
		typ  systemd.ServiceType
		gone func() bool
	}{
		{systemd.ServiceTypeSFU, func() bool {
			return definitelyUnallocated(cm.webrtcPortAllocator.GetSFUPorts(ctx, clusterID, cm.localNodeID))
		}},
	} {
		running, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(namespaceName, svc.typ)
		if !running || !svc.gone() {
			continue
		}
		cm.logger.Info("Stopping WebRTC service: this node no longer holds the allocation (bugboard #161)",
			zap.String("namespace", namespaceName), zap.String("service", string(svc.typ)))
		if serr := cm.systemdSpawner.systemdMgr.StopService(namespaceName, svc.typ); serr != nil {
			cm.logger.Warn("Failed to stop unallocated WebRTC service — its ports stay bound while the allocator considers them free",
				zap.String("namespace", namespaceName), zap.String("service", string(svc.typ)), zap.Error(serr))
			continue
		}
		// Verify it actually stopped. The allocator has already freed these ports,
		// so a process that is still bound can collide with the next allocation
		// (an overlapping relay range, or 3478 taken from under another
		// namespace). A failed stop must be loud, not assumed.
		if stillUp, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(namespaceName, svc.typ); stillUp {
			cm.logger.Error("WebRTC service still active after stop — ports remain bound although the allocation was released; a later allocation on this host may collide",
				zap.String("namespace", namespaceName), zap.String("service", string(svc.typ)))
		}
	}
}

// webrtcSpawnBackoff is how long a namespace is skipped after a failed spawn.
//
// IsServiceActive reports a crash-looping unit as NOT running, so without a
// backoff the reconciler rewrites its config and issues `systemctl start` every
// single tick, forever — which also resets systemd's own StartLimitBurst so the
// unit never settles. Worse, each attempt blocks up to 30s in waitForService and
// namespaces are reconciled SERIALLY, so one wedged unit starves the stop sweep
// and DNS ensure for every namespace after it on this node. Back off instead.
const webrtcSpawnBackoff = 10 * time.Minute

// spawnBackoffActive reports whether a namespace is still inside its post-failure
// cooldown, and is the only reader/writer of the cooldown map besides
// recordSpawnFailure — both take the mutex.
func (cm *ClusterManager) spawnBackoffActive(namespace string) bool {
	cm.webrtcSpawnMu.Lock()
	defer cm.webrtcSpawnMu.Unlock()
	until, ok := cm.webrtcSpawnCooldown[namespace]
	return ok && time.Now().Before(until)
}

func (cm *ClusterManager) recordSpawnFailure(namespace string) {
	cm.webrtcSpawnMu.Lock()
	defer cm.webrtcSpawnMu.Unlock()
	if cm.webrtcSpawnCooldown == nil {
		cm.webrtcSpawnCooldown = make(map[string]time.Time)
	}
	cm.webrtcSpawnCooldown[namespace] = time.Now().Add(webrtcSpawnBackoff)
}

// spawnAllocatedWebRTCServices starts TURN/SFU that this node HOLDS an allocation
// for but is not currently running.
//
// This is the counterpart to stopUnallocatedWebRTCServices, and it is what makes
// the reconciler symmetric. Without it the sweep could only ever REDUCE the
// number of running relays: a coordinator could move TURN from node B to node C,
// B would stop on its next tick, and C would not start until it happened to
// reboot — so the namespace silently dropped a relay. A reconciler that stops
// faster than it starts is a liability, not a repair.
//
// Only ever touches THIS node's own services, driven by THIS node's own
// allocation, so it is safe to run unguarded on every node.
func (cm *ClusterManager) spawnAllocatedWebRTCServices(ctx context.Context, state *ClusterLocalState, webrtcCfg *WebRTCConfig) {
	if cm.systemdSpawner == nil || cm.systemdSpawner.systemdMgr == nil || cm.localNodeID == "" {
		return
	}
	if cm.spawnBackoffActive(state.NamespaceName) {
		return
	}
	// These come from cluster-state.json, which this package elsewhere treats as
	// untrustworthy for exactly these fields. An empty LocalIP would bind the SFU
	// signaling socket to ALL interfaces including the public one, not just the
	// WireGuard address; a zero RQLite port yields http://localhost:0.
	if state.LocalIP == "" || state.LocalPorts.RQLiteHTTPPort <= 0 {
		return
	}
	turnDomain := fmt.Sprintf("turn.ns-%s.%s", state.NamespaceName, cm.baseDomain)

	// TURN is deliberately absent here. It binds 3478/5349, which are exclusive
	// per host, so a single shared server serves every namespace on this node
	// (bugboard #283 part 2) and is reconciled once per sweep by
	// ReconcileHostTURN — not once per namespace. Driving it from here would
	// re-derive the whole host's tenant set once for every namespace on the node.

	// SFU — same shape; sfuPortBlockSpawnable refuses a zero media range.
	if blk, err := cm.webrtcPortAllocator.GetSFUPorts(ctx, state.ClusterID, cm.localNodeID); err == nil && sfuPortBlockSpawnable(blk) {
		if running, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(state.NamespaceName, systemd.ServiceTypeSFU); !running {
			if serr := cm.systemdSpawner.SpawnSFU(ctx, state.NamespaceName, cm.localNodeID, SFUInstanceConfig{
				Namespace:      state.NamespaceName,
				NodeID:         cm.localNodeID,
				ListenAddr:     fmt.Sprintf("%s:%d", state.LocalIP, blk.SFUSignalingPort),
				MediaPortStart: blk.SFUMediaPortStart,
				MediaPortEnd:   blk.SFUMediaPortEnd,
				TURNServers: []sfu.TURNServerConfig{
					{Host: turnDomain, Port: TURNDefaultPort, Secure: false},
					{Host: turnDomain, Port: TURNSPort, Secure: true},
				},
				TURNSecret:  webrtcCfg.TURNSharedSecret,
				TURNCredTTL: webrtcCfg.TURNCredentialTTL,
				RQLiteDSN:   fmt.Sprintf("http://localhost:%d", state.LocalPorts.RQLiteHTTPPort),
			}); serr != nil {
				cm.recordSpawnFailure(state.NamespaceName)
				cm.logger.Warn("Failed to start newly-allocated SFU (backing off)",
					zap.String("namespace", state.NamespaceName), zap.Error(serr))
			} else {
				cm.logger.Info("Started SFU for a newly-allocated role (bugboard #161)",
					zap.String("namespace", state.NamespaceName))
			}
		}
	}
}
