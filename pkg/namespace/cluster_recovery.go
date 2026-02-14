package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// nodeIPInfo holds both internal (WireGuard) and public IPs for a node.
type nodeIPInfo struct {
	InternalIP string `db:"internal_ip"`
	IPAddress  string `db:"ip_address"`
}

// survivingNodePorts holds port and IP info for surviving cluster nodes.
type survivingNodePorts struct {
	NodeID              string `db:"node_id"`
	InternalIP          string `db:"internal_ip"`
	IPAddress           string `db:"ip_address"`
	RQLiteHTTPPort      int    `db:"rqlite_http_port"`
	RQLiteRaftPort      int    `db:"rqlite_raft_port"`
	OlricHTTPPort       int    `db:"olric_http_port"`
	OlricMemberlistPort int    `db:"olric_memberlist_port"`
	GatewayHTTPPort     int    `db:"gateway_http_port"`
}

// HandleDeadNode processes the death of a network node by recovering all affected
// namespace clusters. It finds all clusters with assignments to the dead node and
// replaces it with a healthy node in each cluster.
func (cm *ClusterManager) HandleDeadNode(ctx context.Context, deadNodeID string) {
	cm.logger.Error("Handling dead node — starting recovery",
		zap.String("dead_node", deadNodeID),
	)

	// Mark node as offline in dns_nodes
	if err := cm.markNodeOffline(ctx, deadNodeID); err != nil {
		cm.logger.Warn("Failed to mark node offline", zap.Error(err))
	}

	// Find all affected clusters
	clusters, err := cm.getClustersByNodeID(ctx, deadNodeID)
	if err != nil {
		cm.logger.Error("Failed to find affected clusters for dead node",
			zap.String("dead_node", deadNodeID), zap.Error(err))
		return
	}

	if len(clusters) == 0 {
		cm.logger.Info("Dead node had no namespace cluster assignments",
			zap.String("dead_node", deadNodeID))
		return
	}

	cm.logger.Info("Found affected namespace clusters",
		zap.String("dead_node", deadNodeID),
		zap.Int("cluster_count", len(clusters)),
	)

	// Recover each cluster sequentially (avoid overloading replacement nodes)
	successCount := 0
	for _, cluster := range clusters {
		recoveryKey := "recovery:" + cluster.ID
		cm.provisioningMu.Lock()
		if cm.provisioning[recoveryKey] {
			cm.provisioningMu.Unlock()
			cm.logger.Info("Recovery already in progress for cluster, skipping",
				zap.String("cluster_id", cluster.ID),
				zap.String("namespace", cluster.NamespaceName))
			continue
		}
		cm.provisioning[recoveryKey] = true
		cm.provisioningMu.Unlock()

		clusterCopy := cluster
		err := func() error {
			defer func() {
				cm.provisioningMu.Lock()
				delete(cm.provisioning, recoveryKey)
				cm.provisioningMu.Unlock()
			}()
			return cm.ReplaceClusterNode(ctx, &clusterCopy, deadNodeID)
		}()

		if err != nil {
			cm.logger.Error("Failed to recover cluster",
				zap.String("cluster_id", clusterCopy.ID),
				zap.String("namespace", clusterCopy.NamespaceName),
				zap.String("dead_node", deadNodeID),
				zap.Error(err),
			)
			cm.logEvent(ctx, clusterCopy.ID, EventRecoveryFailed, deadNodeID,
				fmt.Sprintf("Recovery failed: %s", err), nil)
		} else {
			successCount++
		}
	}

	cm.logger.Info("Dead node recovery completed",
		zap.String("dead_node", deadNodeID),
		zap.Int("clusters_total", len(clusters)),
		zap.Int("clusters_recovered", successCount),
	)
}

// HandleRecoveredNode handles a previously-dead node coming back online.
// It checks if the node was replaced during downtime and cleans up orphaned services.
func (cm *ClusterManager) HandleRecoveredNode(ctx context.Context, nodeID string) {
	cm.logger.Info("Handling recovered node — checking for orphaned services",
		zap.String("node_id", nodeID),
	)

	// Check if the node still has any cluster assignments
	type assignmentCheck struct {
		Count int `db:"count"`
	}
	var results []assignmentCheck
	query := `SELECT COUNT(*) as count FROM namespace_cluster_nodes WHERE node_id = ?`
	if err := cm.db.Query(ctx, &results, query, nodeID); err != nil {
		cm.logger.Warn("Failed to check node assignments", zap.Error(err))
		return
	}

	if len(results) > 0 && results[0].Count > 0 {
		// Node still has legitimate assignments — mark active and repair degraded clusters
		cm.logger.Info("Recovered node still has cluster assignments, marking active",
			zap.String("node_id", nodeID),
			zap.Int("assignments", results[0].Count))
		cm.markNodeActive(ctx, nodeID)

		// Trigger repair for any degraded clusters this node belongs to
		cm.repairDegradedClusters(ctx, nodeID)
		return
	}

	// Node has no assignments — it was replaced. Clean up orphaned services.
	cm.logger.Warn("Recovered node was replaced during downtime, cleaning up orphaned services",
		zap.String("node_id", nodeID))

	// Get the node's internal IP to send stop requests
	ips, err := cm.getNodeIPs(ctx, nodeID)
	if err != nil {
		cm.logger.Warn("Failed to get recovered node IPs for cleanup", zap.Error(err))
		cm.markNodeActive(ctx, nodeID)
		return
	}

	// Find which namespaces were moved away by querying recovery events
	type eventInfo struct {
		NamespaceName string `db:"namespace_name"`
	}
	var events []eventInfo
	cutoff := time.Now().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	eventsQuery := `
		SELECT DISTINCT c.namespace_name
		FROM namespace_cluster_events e
		JOIN namespace_clusters c ON e.namespace_cluster_id = c.id
		WHERE e.node_id = ? AND e.event_type = ? AND e.created_at > ?
	`
	if err := cm.db.Query(ctx, &events, eventsQuery, nodeID, EventRecoveryStarted, cutoff); err != nil {
		cm.logger.Warn("Failed to query recovery events for cleanup", zap.Error(err))
	}

	// Send stop requests for each orphaned namespace
	for _, evt := range events {
		cm.logger.Info("Stopping orphaned namespace services on recovered node",
			zap.String("node_id", nodeID),
			zap.String("namespace", evt.NamespaceName))
		cm.sendStopRequest(ctx, ips.InternalIP, "stop-all", evt.NamespaceName, nodeID)
		// Also delete the stale cluster-state.json
		cm.sendSpawnRequest(ctx, ips.InternalIP, map[string]interface{}{
			"action":    "delete-cluster-state",
			"namespace": evt.NamespaceName,
			"node_id":   nodeID,
		})
	}

	// Mark node as active again — it's available for future use
	cm.markNodeActive(ctx, nodeID)

	cm.logger.Info("Recovered node cleanup completed",
		zap.String("node_id", nodeID),
		zap.Int("namespaces_cleaned", len(events)))
}

// ReplaceClusterNode replaces a dead node in a specific namespace cluster.
// It selects a new node, allocates ports, spawns services, updates DNS, and cleans up.
func (cm *ClusterManager) ReplaceClusterNode(ctx context.Context, cluster *NamespaceCluster, deadNodeID string) error {
	cm.logger.Info("Starting node replacement in cluster",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
		zap.String("dead_node", deadNodeID),
	)
	cm.logEvent(ctx, cluster.ID, EventRecoveryStarted, deadNodeID,
		fmt.Sprintf("Recovery started: replacing dead node %s", deadNodeID), nil)

	// 1. Mark dead node's assignments as failed
	if err := cm.updateClusterNodeStatus(ctx, cluster.ID, deadNodeID, NodeStatusFailed); err != nil {
		cm.logger.Warn("Failed to mark node as failed in cluster", zap.Error(err))
	}

	// 2. Mark cluster as degraded
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusDegraded,
		fmt.Sprintf("Node %s is dead, recovery in progress", deadNodeID))
	cm.logEvent(ctx, cluster.ID, EventClusterDegraded, deadNodeID, "Cluster degraded due to dead node", nil)

	// 3. Get all current cluster nodes and their info
	clusterNodes, err := cm.getClusterNodes(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	// Build exclude list (all current cluster members)
	excludeIDs := make([]string, 0, len(clusterNodes))
	for _, cn := range clusterNodes {
		excludeIDs = append(excludeIDs, cn.NodeID)
	}

	// 4. Select replacement node
	replacement, err := cm.nodeSelector.SelectReplacementNode(ctx, excludeIDs)
	if err != nil {
		return fmt.Errorf("failed to select replacement node: %w", err)
	}

	cm.logger.Info("Selected replacement node",
		zap.String("namespace", cluster.NamespaceName),
		zap.String("replacement_node", replacement.NodeID),
		zap.String("replacement_ip", replacement.InternalIP),
	)

	// 5. Allocate ports on replacement node
	portBlock, err := cm.portAllocator.AllocatePortBlock(ctx, replacement.NodeID, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to allocate ports on replacement node: %w", err)
	}

	// 6. Get surviving nodes' port info
	var surviving []survivingNodePorts
	portsQuery := `
		SELECT pa.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip, dn.ip_address,
			pa.rqlite_http_port, pa.rqlite_raft_port, pa.olric_http_port,
			pa.olric_memberlist_port, pa.gateway_http_port
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ? AND pa.node_id != ?
	`
	if err := cm.db.Query(ctx, &surviving, portsQuery, cluster.ID, deadNodeID); err != nil {
		// Rollback port allocation
		cm.portAllocator.DeallocatePortBlock(ctx, cluster.ID, replacement.NodeID)
		return fmt.Errorf("failed to query surviving node ports: %w", err)
	}

	// 7. Determine dead node's roles
	deadNodeRoles := make(map[NodeRole]bool)
	var deadNodeRaftPort int
	for _, cn := range clusterNodes {
		if cn.NodeID == deadNodeID {
			deadNodeRoles[cn.Role] = true
			if cn.Role == NodeRoleRQLiteLeader || cn.Role == NodeRoleRQLiteFollower {
				deadNodeRaftPort = cn.RQLiteRaftPort
			}
		}
	}

	// 8. Remove dead node from RQLite Raft cluster (before joining replacement)
	if deadNodeRoles[NodeRoleRQLiteLeader] || deadNodeRoles[NodeRoleRQLiteFollower] {
		deadIPs, err := cm.getNodeIPs(ctx, deadNodeID)
		if err == nil && deadNodeRaftPort > 0 {
			deadRaftAddr := fmt.Sprintf("%s:%d", deadIPs.InternalIP, deadNodeRaftPort)
			cm.removeDeadNodeFromRaft(ctx, deadRaftAddr, surviving)
		}
	}

	spawnErrors := 0

	// 9. Spawn RQLite follower on replacement
	if deadNodeRoles[NodeRoleRQLiteLeader] || deadNodeRoles[NodeRoleRQLiteFollower] {
		var joinAddr string
		for _, s := range surviving {
			if s.RQLiteRaftPort > 0 {
				joinAddr = fmt.Sprintf("%s:%d", s.InternalIP, s.RQLiteRaftPort)
				break
			}
		}

		rqliteCfg := rqlite.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         replacement.NodeID,
			HTTPPort:       portBlock.RQLiteHTTPPort,
			RaftPort:       portBlock.RQLiteRaftPort,
			HTTPAdvAddress: fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.RQLiteHTTPPort),
			RaftAdvAddress: fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.RQLiteRaftPort),
			JoinAddresses:  []string{joinAddr},
			IsLeader:       false,
		}

		var spawnErr error
		if replacement.NodeID == cm.localNodeID {
			spawnErr = cm.spawnRQLiteWithSystemd(ctx, rqliteCfg)
		} else {
			_, spawnErr = cm.spawnRQLiteRemote(ctx, replacement.InternalIP, rqliteCfg)
		}
		if spawnErr != nil {
			cm.logger.Error("Failed to spawn RQLite follower on replacement",
				zap.String("node", replacement.NodeID), zap.Error(spawnErr))
			spawnErrors++
		} else {
			cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleRQLiteFollower, portBlock)
			cm.logEvent(ctx, cluster.ID, EventRQLiteStarted, replacement.NodeID,
				"RQLite follower started on replacement node", nil)
		}
	}

	// 10. Spawn Olric on replacement
	if deadNodeRoles[NodeRoleOlric] {
		var olricPeers []string
		for _, s := range surviving {
			if s.OlricMemberlistPort > 0 {
				olricPeers = append(olricPeers, fmt.Sprintf("%s:%d", s.InternalIP, s.OlricMemberlistPort))
			}
		}

		olricCfg := olric.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         replacement.NodeID,
			HTTPPort:       portBlock.OlricHTTPPort,
			MemberlistPort: portBlock.OlricMemberlistPort,
			BindAddr:       replacement.InternalIP,
			AdvertiseAddr:  replacement.InternalIP,
			PeerAddresses:  olricPeers,
		}

		var spawnErr error
		if replacement.NodeID == cm.localNodeID {
			spawnErr = cm.spawnOlricWithSystemd(ctx, olricCfg)
		} else {
			_, spawnErr = cm.spawnOlricRemote(ctx, replacement.InternalIP, olricCfg)
		}
		if spawnErr != nil {
			cm.logger.Error("Failed to spawn Olric on replacement",
				zap.String("node", replacement.NodeID), zap.Error(spawnErr))
			spawnErrors++
		} else {
			cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleOlric, portBlock)
			cm.logEvent(ctx, cluster.ID, EventOlricStarted, replacement.NodeID,
				"Olric started on replacement node", nil)
		}
	}

	// 11. Spawn Gateway on replacement
	if deadNodeRoles[NodeRoleGateway] {
		// Build Olric server addresses — all nodes including replacement
		var olricServers []string
		for _, s := range surviving {
			if s.OlricHTTPPort > 0 {
				olricServers = append(olricServers, fmt.Sprintf("%s:%d", s.InternalIP, s.OlricHTTPPort))
			}
		}
		olricServers = append(olricServers, fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.OlricHTTPPort))

		gwCfg := gateway.InstanceConfig{
			Namespace:             cluster.NamespaceName,
			NodeID:                replacement.NodeID,
			HTTPPort:              portBlock.GatewayHTTPPort,
			BaseDomain:            cm.baseDomain,
			RQLiteDSN:             fmt.Sprintf("http://localhost:%d", portBlock.RQLiteHTTPPort),
			GlobalRQLiteDSN:       cm.globalRQLiteDSN,
			OlricServers:          olricServers,
			OlricTimeout:          30 * time.Second,
			IPFSClusterAPIURL:     cm.ipfsClusterAPIURL,
			IPFSAPIURL:            cm.ipfsAPIURL,
			IPFSTimeout:           cm.ipfsTimeout,
			IPFSReplicationFactor: cm.ipfsReplicationFactor,
		}

		var spawnErr error
		if replacement.NodeID == cm.localNodeID {
			spawnErr = cm.spawnGatewayWithSystemd(ctx, gwCfg)
		} else {
			_, spawnErr = cm.spawnGatewayRemote(ctx, replacement.InternalIP, gwCfg)
		}
		if spawnErr != nil {
			cm.logger.Error("Failed to spawn Gateway on replacement",
				zap.String("node", replacement.NodeID), zap.Error(spawnErr))
			spawnErrors++
		} else {
			cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleGateway, portBlock)
			cm.logEvent(ctx, cluster.ID, EventGatewayStarted, replacement.NodeID,
				"Gateway started on replacement node", nil)
		}
	}

	// 12. Update DNS: swap dead node's PUBLIC IP for replacement's PUBLIC IP
	deadIPs, err := cm.getNodeIPs(ctx, deadNodeID)
	if err == nil && deadIPs.IPAddress != "" {
		dnsManager := NewDNSRecordManager(cm.db, cm.baseDomain, cm.logger)
		if err := dnsManager.UpdateNamespaceRecord(ctx, cluster.NamespaceName, deadIPs.IPAddress, replacement.IPAddress); err != nil {
			cm.logger.Error("Failed to update DNS records",
				zap.String("namespace", cluster.NamespaceName),
				zap.String("old_ip", deadIPs.IPAddress),
				zap.String("new_ip", replacement.IPAddress),
				zap.Error(err))
		} else {
			cm.logger.Info("DNS records updated",
				zap.String("namespace", cluster.NamespaceName),
				zap.String("old_ip", deadIPs.IPAddress),
				zap.String("new_ip", replacement.IPAddress))
			cm.logEvent(ctx, cluster.ID, EventDNSCreated, replacement.NodeID,
				fmt.Sprintf("DNS updated: %s → %s", deadIPs.IPAddress, replacement.IPAddress), nil)
		}
	}

	// 13. Clean up dead node's port allocations and cluster assignments
	cm.portAllocator.DeallocatePortBlock(ctx, cluster.ID, deadNodeID)
	cm.removeClusterNodeAssignment(ctx, cluster.ID, deadNodeID)

	// 14. Update cluster-state.json on all nodes
	cm.updateClusterStateAfterRecovery(ctx, cluster)

	// 15. Update cluster status
	if spawnErrors == 0 {
		cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusReady, "")
	}
	// If there were spawn errors, cluster stays degraded

	cm.logEvent(ctx, cluster.ID, EventNodeReplaced, replacement.NodeID,
		fmt.Sprintf("Dead node %s replaced by %s", deadNodeID, replacement.NodeID),
		map[string]interface{}{
			"dead_node":        deadNodeID,
			"replacement_node": replacement.NodeID,
			"spawn_errors":     spawnErrors,
		})
	cm.logEvent(ctx, cluster.ID, EventRecoveryComplete, "", "Recovery completed", nil)

	cm.logger.Info("Node replacement completed",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
		zap.String("dead_node", deadNodeID),
		zap.String("replacement", replacement.NodeID),
		zap.Int("spawn_errors", spawnErrors),
	)

	return nil
}

// --- Helper methods ---

// getClustersByNodeID returns all ready/degraded clusters that have the given node assigned.
func (cm *ClusterManager) getClustersByNodeID(ctx context.Context, nodeID string) ([]NamespaceCluster, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type clusterRef struct {
		ClusterID string `db:"namespace_cluster_id"`
	}
	var refs []clusterRef
	query := `
		SELECT DISTINCT cn.namespace_cluster_id
		FROM namespace_cluster_nodes cn
		JOIN namespace_clusters c ON cn.namespace_cluster_id = c.id
		WHERE cn.node_id = ? AND c.status IN ('ready', 'degraded')
	`
	if err := cm.db.Query(internalCtx, &refs, query, nodeID); err != nil {
		return nil, fmt.Errorf("failed to query clusters by node: %w", err)
	}

	var clusters []NamespaceCluster
	for _, ref := range refs {
		cluster, err := cm.GetCluster(internalCtx, ref.ClusterID)
		if err != nil || cluster == nil {
			continue
		}
		clusters = append(clusters, *cluster)
	}
	return clusters, nil
}

// updateClusterNodeStatus marks a specific cluster node assignment with a new status.
func (cm *ClusterManager) updateClusterNodeStatus(ctx context.Context, clusterID, nodeID string, status NodeStatus) error {
	query := `UPDATE namespace_cluster_nodes SET status = ?, updated_at = ? WHERE namespace_cluster_id = ? AND node_id = ?`
	_, err := cm.db.Exec(ctx, query, status, time.Now().Format("2006-01-02 15:04:05"), clusterID, nodeID)
	return err
}

// removeClusterNodeAssignment deletes all node assignments for a node in a cluster.
func (cm *ClusterManager) removeClusterNodeAssignment(ctx context.Context, clusterID, nodeID string) {
	query := `DELETE FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ?`
	if _, err := cm.db.Exec(ctx, query, clusterID, nodeID); err != nil {
		cm.logger.Warn("Failed to remove cluster node assignment",
			zap.String("cluster_id", clusterID),
			zap.String("node_id", nodeID),
			zap.Error(err))
	}
}

// getNodeIPs returns both the internal (WireGuard) and public IP for a node.
func (cm *ClusterManager) getNodeIPs(ctx context.Context, nodeID string) (*nodeIPInfo, error) {
	var results []nodeIPInfo
	query := `SELECT COALESCE(internal_ip, ip_address) as internal_ip, ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	if err := cm.db.Query(ctx, &results, query, nodeID); err != nil || len(results) == 0 {
		return nil, fmt.Errorf("node %s not found in dns_nodes", nodeID)
	}
	return &results[0], nil
}

// markNodeOffline sets a node's status to 'offline' in dns_nodes.
func (cm *ClusterManager) markNodeOffline(ctx context.Context, nodeID string) error {
	query := `UPDATE dns_nodes SET status = 'offline', updated_at = ? WHERE id = ?`
	_, err := cm.db.Exec(ctx, query, time.Now().Format("2006-01-02 15:04:05"), nodeID)
	return err
}

// markNodeActive sets a node's status to 'active' in dns_nodes.
func (cm *ClusterManager) markNodeActive(ctx context.Context, nodeID string) {
	query := `UPDATE dns_nodes SET status = 'active', updated_at = ? WHERE id = ?`
	if _, err := cm.db.Exec(ctx, query, time.Now().Format("2006-01-02 15:04:05"), nodeID); err != nil {
		cm.logger.Warn("Failed to mark node active", zap.String("node_id", nodeID), zap.Error(err))
	}
}

// repairDegradedClusters finds degraded clusters that the recovered node
// belongs to and triggers RepairCluster for each one.
func (cm *ClusterManager) repairDegradedClusters(ctx context.Context, nodeID string) {
	type clusterRef struct {
		NamespaceName string `db:"namespace_name"`
	}
	var refs []clusterRef
	query := `
		SELECT DISTINCT c.namespace_name
		FROM namespace_cluster_nodes cn
		JOIN namespace_clusters c ON cn.namespace_cluster_id = c.id
		WHERE cn.node_id = ? AND c.status = 'degraded'
	`
	if err := cm.db.Query(ctx, &refs, query, nodeID); err != nil {
		cm.logger.Warn("Failed to query degraded clusters for recovered node",
			zap.String("node_id", nodeID), zap.Error(err))
		return
	}

	for _, ref := range refs {
		cm.logger.Info("Triggering repair for degraded cluster after node recovery",
			zap.String("namespace", ref.NamespaceName),
			zap.String("recovered_node", nodeID))
		if err := cm.RepairCluster(ctx, ref.NamespaceName); err != nil {
			cm.logger.Warn("Failed to repair degraded cluster",
				zap.String("namespace", ref.NamespaceName),
				zap.Error(err))
		}
	}
}

// removeDeadNodeFromRaft sends a DELETE request to a surviving RQLite node
// to remove the dead node from the Raft voter set.
func (cm *ClusterManager) removeDeadNodeFromRaft(ctx context.Context, deadRaftAddr string, survivingNodes []survivingNodePorts) {
	if deadRaftAddr == "" {
		return
	}

	payload, _ := json.Marshal(map[string]string{"id": deadRaftAddr})

	for _, s := range survivingNodes {
		if s.RQLiteHTTPPort == 0 {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/remove", s.InternalIP, s.RQLiteHTTPPort)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		httpClient := &http.Client{Timeout: 10 * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			cm.logger.Warn("Failed to remove dead node from Raft via this node",
				zap.String("target", s.NodeID), zap.Error(err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			cm.logger.Info("Removed dead node from Raft cluster",
				zap.String("dead_raft_addr", deadRaftAddr),
				zap.String("via_node", s.NodeID))
			return
		}
		cm.logger.Warn("Raft removal returned unexpected status",
			zap.String("via_node", s.NodeID),
			zap.Int("status", resp.StatusCode))
	}
	cm.logger.Warn("Could not remove dead node from Raft cluster (best-effort)",
		zap.String("dead_raft_addr", deadRaftAddr))
}

// updateClusterStateAfterRecovery rebuilds and distributes cluster-state.json
// to all current nodes in the cluster (surviving + replacement).
func (cm *ClusterManager) updateClusterStateAfterRecovery(ctx context.Context, cluster *NamespaceCluster) {
	// Re-query all current nodes and ports
	var allPorts []survivingNodePorts
	query := `
		SELECT pa.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip, dn.ip_address,
			pa.rqlite_http_port, pa.rqlite_raft_port, pa.olric_http_port,
			pa.olric_memberlist_port, pa.gateway_http_port
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ?
	`
	if err := cm.db.Query(ctx, &allPorts, query, cluster.ID); err != nil {
		cm.logger.Warn("Failed to query ports for state update", zap.Error(err))
		return
	}

	// Convert to the format expected by saveClusterStateToAllNodes
	nodes := make([]NodeCapacity, len(allPorts))
	portBlocks := make([]*PortBlock, len(allPorts))
	for i, np := range allPorts {
		nodes[i] = NodeCapacity{
			NodeID:     np.NodeID,
			InternalIP: np.InternalIP,
			IPAddress:  np.IPAddress,
		}
		portBlocks[i] = &PortBlock{
			RQLiteHTTPPort:      np.RQLiteHTTPPort,
			RQLiteRaftPort:      np.RQLiteRaftPort,
			OlricHTTPPort:       np.OlricHTTPPort,
			OlricMemberlistPort: np.OlricMemberlistPort,
			GatewayHTTPPort:     np.GatewayHTTPPort,
		}
	}

	cm.saveClusterStateToAllNodes(ctx, cluster, nodes, portBlocks)
}

// RepairCluster checks a namespace cluster for missing nodes and adds replacements
// without touching surviving nodes. This is used to repair under-provisioned clusters
// (e.g., after manual node removal) without data loss or downtime.
func (cm *ClusterManager) RepairCluster(ctx context.Context, namespaceName string) error {
	cm.logger.Info("Starting cluster repair",
		zap.String("namespace", namespaceName),
	)

	// 1. Look up the cluster
	cluster, err := cm.GetClusterByNamespace(ctx, namespaceName)
	if err != nil {
		return fmt.Errorf("failed to look up cluster: %w", err)
	}
	if cluster == nil {
		return ErrClusterNotFound
	}

	if cluster.Status != ClusterStatusReady && cluster.Status != ClusterStatusDegraded {
		return fmt.Errorf("cluster status is %s, can only repair ready or degraded clusters", cluster.Status)
	}

	// 2. Acquire per-cluster lock
	repairKey := "repair:" + cluster.ID
	cm.provisioningMu.Lock()
	if cm.provisioning[repairKey] {
		cm.provisioningMu.Unlock()
		return ErrRecoveryInProgress
	}
	cm.provisioning[repairKey] = true
	cm.provisioningMu.Unlock()
	defer func() {
		cm.provisioningMu.Lock()
		delete(cm.provisioning, repairKey)
		cm.provisioningMu.Unlock()
	}()

	// 3. Get current cluster nodes
	clusterNodes, err := cm.getClusterNodes(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	// Count unique physical nodes with active services
	activeNodes := make(map[string]bool)
	for _, cn := range clusterNodes {
		if cn.Status == NodeStatusRunning || cn.Status == NodeStatusStarting {
			activeNodes[cn.NodeID] = true
		}
	}

	// Expected node count is the cluster's configured RQLite count (each physical node
	// runs all 3 services: rqlite + olric + gateway)
	expectedCount := cluster.RQLiteNodeCount
	activeCount := len(activeNodes)
	missingCount := expectedCount - activeCount

	if missingCount <= 0 {
		cm.logger.Info("Cluster has expected number of active nodes, no repair needed",
			zap.String("namespace", namespaceName),
			zap.Int("active_nodes", activeCount),
			zap.Int("expected", expectedCount),
		)
		return nil
	}

	cm.logger.Info("Cluster needs repair — adding missing nodes",
		zap.String("namespace", namespaceName),
		zap.Int("active_nodes", activeCount),
		zap.Int("expected", expectedCount),
		zap.Int("missing", missingCount),
	)

	cm.logEvent(ctx, cluster.ID, EventRecoveryStarted, "",
		fmt.Sprintf("Cluster repair started: %d of %d nodes active, adding %d", activeCount, expectedCount, missingCount), nil)

	// 4. Build the current node exclude list (all physical node IDs in the cluster)
	excludeIDs := make([]string, 0)
	nodeIDSet := make(map[string]bool)
	for _, cn := range clusterNodes {
		if !nodeIDSet[cn.NodeID] {
			nodeIDSet[cn.NodeID] = true
			excludeIDs = append(excludeIDs, cn.NodeID)
		}
	}

	// 5. Get surviving nodes' port info for joining
	var surviving []survivingNodePorts
	portsQuery := `
		SELECT pa.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip, dn.ip_address,
			pa.rqlite_http_port, pa.rqlite_raft_port, pa.olric_http_port,
			pa.olric_memberlist_port, pa.gateway_http_port
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ?
	`
	if err := cm.db.Query(ctx, &surviving, portsQuery, cluster.ID); err != nil {
		return fmt.Errorf("failed to query surviving node ports: %w", err)
	}

	if len(surviving) == 0 {
		return fmt.Errorf("no surviving nodes found with port allocations")
	}

	// 6. Add missing nodes one at a time
	addedCount := 0
	for i := 0; i < missingCount; i++ {
		replacement, portBlock, err := cm.addNodeToCluster(ctx, cluster, excludeIDs, surviving)
		if err != nil {
			cm.logger.Error("Failed to add node during cluster repair",
				zap.String("namespace", namespaceName),
				zap.Int("node_index", i+1),
				zap.Int("missing", missingCount),
				zap.Error(err),
			)
			cm.logEvent(ctx, cluster.ID, EventRecoveryFailed, "",
				fmt.Sprintf("Repair failed on node %d of %d: %s", i+1, missingCount, err), nil)
			break
		}

		addedCount++

		// Update exclude list and surviving list for next iteration
		excludeIDs = append(excludeIDs, replacement.NodeID)
		surviving = append(surviving, survivingNodePorts{
			NodeID:              replacement.NodeID,
			InternalIP:          replacement.InternalIP,
			IPAddress:           replacement.IPAddress,
			RQLiteHTTPPort:      portBlock.RQLiteHTTPPort,
			RQLiteRaftPort:      portBlock.RQLiteRaftPort,
			OlricHTTPPort:       portBlock.OlricHTTPPort,
			OlricMemberlistPort: portBlock.OlricMemberlistPort,
			GatewayHTTPPort:     portBlock.GatewayHTTPPort,
		})
	}

	if addedCount == 0 {
		return fmt.Errorf("failed to add any replacement nodes")
	}

	// 7. Update cluster-state.json on all nodes
	cm.updateClusterStateAfterRecovery(ctx, cluster)

	// 8. Mark cluster ready
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusReady, "")

	cm.logEvent(ctx, cluster.ID, EventRecoveryComplete, "",
		fmt.Sprintf("Cluster repair completed: added %d of %d missing nodes", addedCount, missingCount),
		map[string]interface{}{"added_nodes": addedCount, "missing_nodes": missingCount})

	cm.logger.Info("Cluster repair completed",
		zap.String("namespace", namespaceName),
		zap.Int("added_nodes", addedCount),
		zap.Int("missing_nodes", missingCount),
	)

	return nil
}

// addNodeToCluster selects a new node and spawns all services (RQLite follower, Olric, Gateway)
// on it, joining the existing cluster. Returns the replacement node info and allocated port block.
func (cm *ClusterManager) addNodeToCluster(
	ctx context.Context,
	cluster *NamespaceCluster,
	excludeIDs []string,
	surviving []survivingNodePorts,
) (*NodeCapacity, *PortBlock, error) {

	// 1. Select replacement node
	replacement, err := cm.nodeSelector.SelectReplacementNode(ctx, excludeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to select replacement node: %w", err)
	}

	cm.logger.Info("Selected node for cluster repair",
		zap.String("namespace", cluster.NamespaceName),
		zap.String("new_node", replacement.NodeID),
		zap.String("new_ip", replacement.InternalIP),
	)

	// 2. Allocate ports on the new node
	portBlock, err := cm.portAllocator.AllocatePortBlock(ctx, replacement.NodeID, cluster.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to allocate ports on new node: %w", err)
	}

	// 3. Spawn RQLite follower
	var joinAddr string
	for _, s := range surviving {
		if s.RQLiteRaftPort > 0 {
			joinAddr = fmt.Sprintf("%s:%d", s.InternalIP, s.RQLiteRaftPort)
			break
		}
	}

	rqliteCfg := rqlite.InstanceConfig{
		Namespace:      cluster.NamespaceName,
		NodeID:         replacement.NodeID,
		HTTPPort:       portBlock.RQLiteHTTPPort,
		RaftPort:       portBlock.RQLiteRaftPort,
		HTTPAdvAddress: fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.RQLiteHTTPPort),
		RaftAdvAddress: fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.RQLiteRaftPort),
		JoinAddresses:  []string{joinAddr},
		IsLeader:       false,
	}

	var spawnErr error
	if replacement.NodeID == cm.localNodeID {
		spawnErr = cm.spawnRQLiteWithSystemd(ctx, rqliteCfg)
	} else {
		_, spawnErr = cm.spawnRQLiteRemote(ctx, replacement.InternalIP, rqliteCfg)
	}
	if spawnErr != nil {
		cm.portAllocator.DeallocatePortBlock(ctx, cluster.ID, replacement.NodeID)
		return nil, nil, fmt.Errorf("failed to spawn RQLite follower: %w", spawnErr)
	}
	cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleRQLiteFollower, portBlock)
	cm.logEvent(ctx, cluster.ID, EventRQLiteStarted, replacement.NodeID,
		"RQLite follower started on new node (repair)", nil)

	// 4. Spawn Olric
	var olricPeers []string
	for _, s := range surviving {
		if s.OlricMemberlistPort > 0 {
			olricPeers = append(olricPeers, fmt.Sprintf("%s:%d", s.InternalIP, s.OlricMemberlistPort))
		}
	}

	olricCfg := olric.InstanceConfig{
		Namespace:      cluster.NamespaceName,
		NodeID:         replacement.NodeID,
		HTTPPort:       portBlock.OlricHTTPPort,
		MemberlistPort: portBlock.OlricMemberlistPort,
		BindAddr:       replacement.InternalIP,
		AdvertiseAddr:  replacement.InternalIP,
		PeerAddresses:  olricPeers,
	}

	if replacement.NodeID == cm.localNodeID {
		spawnErr = cm.spawnOlricWithSystemd(ctx, olricCfg)
	} else {
		_, spawnErr = cm.spawnOlricRemote(ctx, replacement.InternalIP, olricCfg)
	}
	if spawnErr != nil {
		cm.logger.Error("Failed to spawn Olric on new node (repair continues)",
			zap.String("node", replacement.NodeID), zap.Error(spawnErr))
	} else {
		cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleOlric, portBlock)
		cm.logEvent(ctx, cluster.ID, EventOlricStarted, replacement.NodeID,
			"Olric started on new node (repair)", nil)
	}

	// 5. Spawn Gateway
	var olricServers []string
	for _, s := range surviving {
		if s.OlricHTTPPort > 0 {
			olricServers = append(olricServers, fmt.Sprintf("%s:%d", s.InternalIP, s.OlricHTTPPort))
		}
	}
	olricServers = append(olricServers, fmt.Sprintf("%s:%d", replacement.InternalIP, portBlock.OlricHTTPPort))

	gwCfg := gateway.InstanceConfig{
		Namespace:             cluster.NamespaceName,
		NodeID:                replacement.NodeID,
		HTTPPort:              portBlock.GatewayHTTPPort,
		BaseDomain:            cm.baseDomain,
		RQLiteDSN:             fmt.Sprintf("http://localhost:%d", portBlock.RQLiteHTTPPort),
		GlobalRQLiteDSN:       cm.globalRQLiteDSN,
		OlricServers:          olricServers,
		OlricTimeout:          30 * time.Second,
		IPFSClusterAPIURL:     cm.ipfsClusterAPIURL,
		IPFSAPIURL:            cm.ipfsAPIURL,
		IPFSTimeout:           cm.ipfsTimeout,
		IPFSReplicationFactor: cm.ipfsReplicationFactor,
	}

	if replacement.NodeID == cm.localNodeID {
		spawnErr = cm.spawnGatewayWithSystemd(ctx, gwCfg)
	} else {
		_, spawnErr = cm.spawnGatewayRemote(ctx, replacement.InternalIP, gwCfg)
	}
	if spawnErr != nil {
		cm.logger.Error("Failed to spawn Gateway on new node (repair continues)",
			zap.String("node", replacement.NodeID), zap.Error(spawnErr))
	} else {
		cm.insertClusterNode(ctx, cluster.ID, replacement.NodeID, NodeRoleGateway, portBlock)
		cm.logEvent(ctx, cluster.ID, EventGatewayStarted, replacement.NodeID,
			"Gateway started on new node (repair)", nil)
	}

	// 6. Add DNS records for the new node's public IP
	dnsManager := NewDNSRecordManager(cm.db, cm.baseDomain, cm.logger)
	if err := dnsManager.AddNamespaceRecord(ctx, cluster.NamespaceName, replacement.IPAddress); err != nil {
		cm.logger.Error("Failed to add DNS record for new node",
			zap.String("namespace", cluster.NamespaceName),
			zap.String("ip", replacement.IPAddress),
			zap.Error(err))
	} else {
		cm.logEvent(ctx, cluster.ID, EventDNSCreated, replacement.NodeID,
			fmt.Sprintf("DNS record added for new node %s", replacement.IPAddress), nil)
	}

	cm.logEvent(ctx, cluster.ID, EventNodeReplaced, replacement.NodeID,
		fmt.Sprintf("New node %s added to cluster (repair)", replacement.NodeID),
		map[string]interface{}{"new_node": replacement.NodeID})

	return replacement, portBlock, nil
}
