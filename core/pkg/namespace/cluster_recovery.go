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
// namespace clusters and deployment replicas. It marks all deployment replicas on
// the dead node as failed, updates deployment statuses, and replaces namespace
// cluster nodes.
func (cm *ClusterManager) HandleDeadNode(ctx context.Context, deadNodeID string) {
	cm.logger.Error("Handling dead node — starting recovery",
		zap.String("dead_node", deadNodeID),
	)

	// Mark node as offline in dns_nodes
	if err := cm.markNodeOffline(ctx, deadNodeID); err != nil {
		cm.logger.Warn("Failed to mark node offline", zap.Error(err))
	}

	// Mark all deployment replicas on the dead node as failed.
	// This must happen before namespace recovery so routing immediately
	// excludes the dead node — no relying on circuit breakers to discover it.
	cm.markDeadNodeReplicasFailed(ctx, deadNodeID)

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
	// Bugboard #282: compare in UTC — event timestamps are stored UTC.
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
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

// HandleSuspectNode disables DNS records for a suspect node to prevent traffic
// from being routed to it. Called early (T+30s) when the node first becomes suspect,
// before confirming it's actually dead. If the node recovers, HandleSuspectRecovery
// will re-enable the records.
//
// Safety: never disables the last active record for a namespace.
func (cm *ClusterManager) HandleSuspectNode(ctx context.Context, suspectNodeID string) {
	cm.logger.Warn("Handling suspect node — disabling DNS records",
		zap.String("suspect_node", suspectNodeID),
	)

	// Acquire per-node lock to prevent concurrent suspect handling
	suspectKey := "suspect:" + suspectNodeID
	cm.provisioningMu.Lock()
	if cm.provisioning[suspectKey] {
		cm.provisioningMu.Unlock()
		cm.logger.Info("Suspect handling already in progress for node, skipping",
			zap.String("node_id", suspectNodeID))
		return
	}
	cm.provisioning[suspectKey] = true
	cm.provisioningMu.Unlock()
	defer func() {
		cm.provisioningMu.Lock()
		delete(cm.provisioning, suspectKey)
		cm.provisioningMu.Unlock()
	}()

	// Find all clusters this node belongs to
	clusters, err := cm.getClustersByNodeID(ctx, suspectNodeID)
	if err != nil {
		cm.logger.Warn("Failed to find clusters for suspect node",
			zap.String("suspect_node", suspectNodeID), zap.Error(err))
		return
	}

	if len(clusters) == 0 {
		cm.logger.Info("Suspect node has no namespace cluster assignments",
			zap.String("suspect_node", suspectNodeID))
		return
	}

	// Get suspect node's public IP (DNS A records contain public IPs)
	ips, err := cm.getNodeIPs(ctx, suspectNodeID)
	if err != nil {
		cm.logger.Warn("Failed to get suspect node IPs",
			zap.String("suspect_node", suspectNodeID), zap.Error(err))
		return
	}

	dnsManager := NewDNSRecordManager(cm.db, cm.baseDomain, cm.logger)
	disabledCount := 0

	for _, cluster := range clusters {
		// Safety check: never disable the last active record
		activeCount, err := dnsManager.CountActiveNamespaceRecords(ctx, cluster.NamespaceName)
		if err != nil {
			cm.logger.Warn("Failed to count active DNS records, skipping namespace",
				zap.String("namespace", cluster.NamespaceName),
				zap.Error(err))
			continue
		}

		if activeCount <= 1 {
			cm.logger.Warn("Not disabling DNS — would leave namespace with no active records",
				zap.String("namespace", cluster.NamespaceName),
				zap.String("suspect_node", suspectNodeID),
				zap.Int("active_records", activeCount))
			continue
		}

		if err := dnsManager.DisableNamespaceRecord(ctx, cluster.NamespaceName, ips.IPAddress); err != nil {
			cm.logger.Warn("Failed to disable DNS record for suspect node",
				zap.String("namespace", cluster.NamespaceName),
				zap.String("ip", ips.IPAddress),
				zap.Error(err))
			continue
		}

		disabledCount++
		cm.logger.Info("Disabled DNS record for suspect node",
			zap.String("namespace", cluster.NamespaceName),
			zap.String("ip", ips.IPAddress))
	}

	cm.logger.Info("Suspect node DNS handling completed",
		zap.String("suspect_node", suspectNodeID),
		zap.Int("namespaces_affected", len(clusters)),
		zap.Int("records_disabled", disabledCount))
}

// HandleSuspectRecovery re-enables DNS records for a node that recovered from
// suspect state without going dead. Called when the health monitor detects
// that a previously suspect node is responding to probes again.
func (cm *ClusterManager) HandleSuspectRecovery(ctx context.Context, nodeID string) {
	cm.logger.Info("Handling suspect recovery — re-enabling DNS records",
		zap.String("node_id", nodeID),
	)

	// Find all clusters this node belongs to
	clusters, err := cm.getClustersByNodeID(ctx, nodeID)
	if err != nil {
		cm.logger.Warn("Failed to find clusters for recovered node",
			zap.String("node_id", nodeID), zap.Error(err))
		return
	}

	if len(clusters) == 0 {
		return
	}

	// Get node's public IP (DNS A records contain public IPs)
	ips, err := cm.getNodeIPs(ctx, nodeID)
	if err != nil {
		cm.logger.Warn("Failed to get recovered node IPs",
			zap.String("node_id", nodeID), zap.Error(err))
		return
	}

	dnsManager := NewDNSRecordManager(cm.db, cm.baseDomain, cm.logger)
	enabledCount := 0

	for _, cluster := range clusters {
		if err := dnsManager.EnableNamespaceRecord(ctx, cluster.NamespaceName, ips.IPAddress); err != nil {
			cm.logger.Warn("Failed to re-enable DNS record for recovered node",
				zap.String("namespace", cluster.NamespaceName),
				zap.String("ip", ips.IPAddress),
				zap.Error(err))
			continue
		}

		enabledCount++
		cm.logger.Info("Re-enabled DNS record for recovered node",
			zap.String("namespace", cluster.NamespaceName),
			zap.String("ip", ips.IPAddress))
	}

	cm.logger.Info("Suspect recovery DNS handling completed",
		zap.String("node_id", nodeID),
		zap.Int("records_enabled", enabledCount))
}

// nodeIsLive reports whether a node is currently alive: marked active in
// dns_nodes and heartbeating within the same freshness window the node selector
// uses. Bugboard #279 — recovery must be able to tell "gone" from "restarting".
func (cm *ClusterManager) nodeIsLive(ctx context.Context, nodeID string) bool {
	var rows []struct {
		ID string `db:"id"`
	}
	cutoff := time.Now().UTC().Add(-nodeLivenessWindow).Format("2006-01-02 15:04:05")
	q := `SELECT id FROM dns_nodes WHERE id = ? AND status = 'active' AND last_seen > ?`
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows, q, nodeID, cutoff); err != nil {
		// Unknown is not alive — fall through to the normal dead-node path rather
		// than silently skipping recovery on a transient query failure.
		cm.logger.Warn("Liveness check failed; treating node as not live",
			zap.String("node_id", nodeID), zap.Error(err))
		return false
	}
	return len(rows) > 0
}

// nodeLivenessWindow mirrors the node selector's 2-minute activity window.
const nodeLivenessWindow = 2 * time.Minute

// reinstateNode restores a cluster member whose services are expected to be
// running again, clearing the failed marks left by an earlier dead-node event.
func (cm *ClusterManager) reinstateNode(ctx context.Context, cluster *NamespaceCluster, nodeID string) error {
	if err := cm.updateClusterNodeStatus(ctx, cluster.ID, nodeID, NodeStatusRunning); err != nil {
		return fmt.Errorf("reinstate node %s: %w", nodeID, err)
	}
	cm.logger.Info("Reinstated cluster node that is alive again (bugboard #279)",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
		zap.String("node_id", nodeID))
	cm.logEvent(ctx, cluster.ID, EventRecoveryComplete, nodeID,
		fmt.Sprintf("Node %s is alive again — reinstated without replacement", nodeID), nil)
	return nil
}

// settleClusterStatus recomputes the cluster-level status from its members and
// writes it. Bugboard #279: RepairCluster used to return early when nothing was
// missing WITHOUT clearing a stale 'degraded', so a cluster that had fully
// recovered stayed marked degraded — and with bugboard #278 that kept the whole
// namespace 404ing at the edge. Ready when every expected member is running.
func (cm *ClusterManager) settleClusterStatus(ctx context.Context, cluster *NamespaceCluster) error {
	nodes, err := cm.getClusterNodes(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("settle cluster status: %w", err)
	}
	running := make(map[string]bool)
	for _, cn := range nodes {
		if cn.Status == NodeStatusRunning || cn.Status == NodeStatusStarting {
			running[cn.NodeID] = true
		}
	}
	if len(running) >= cluster.RQLiteNodeCount {
		return cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusReady, "")
	}
	return cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusDegraded,
		fmt.Sprintf("%d of %d nodes running", len(running), cluster.RQLiteNodeCount))
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

	// 0. Bugboard #279: the node may simply have restarted. Replacing a node that
	// is alive again is wrong twice over — it discards a healthy member, and on a
	// cluster that already spans the whole fleet there is no replacement to find,
	// so steps 1-2 below would commit failed/degraded and then bail out, wedging
	// the cluster there permanently. Reinstate instead.
	if cm.nodeIsLive(ctx, deadNodeID) {
		if err := cm.reinstateNode(ctx, cluster, deadNodeID); err != nil {
			return err
		}
		return cm.settleClusterStatus(ctx, cluster)
	}

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
	portBlock, err := cm.portAllocator.AllocatePortBlock(ctx, replacement.NodeID, cluster.ID, BlueprintTenant())
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
			SecretsEncryptionKey:  cm.secretsEncryptionKey,
			NtfyBaseURL:           cm.ntfyBaseURL,
		}

		// Add WebRTC config if enabled for this namespace.
		//
		// TURN and SFU are DECOUPLED (bugboard #25): the TURN secret is
		// namespace-wide, so this replacement gateway must be able to mint
		// credentials even though it holds no SFU allocation yet. Setting them
		// together used to leave a failover replacement with NO turn_secret, so it
		// answered /v1/webrtc/turn/credentials with 503 while serving its share of
		// the ns-<ns> round-robin (observed on devnet, 57.131.41.160).
		if webrtcCfg, err := cm.GetWebRTCConfig(ctx, cluster.NamespaceName); err == nil && webrtcCfg != nil {
			gwCfg.TURNDomain = fmt.Sprintf("turn.ns-%s.%s", cluster.NamespaceName, cm.baseDomain)
			gwCfg.TURNSecret = webrtcCfg.TURNSharedSecret
			gwCfg.TURNStealthDomain = cm.stealthDomainFor(cluster.NamespaceName, webrtcCfg)
			if sfuBlock, serr := cm.webrtcPortAllocator.GetSFUPorts(ctx, cluster.ID, replacement.NodeID); serr == nil && sfuBlock != nil {
				gwCfg.WebRTCEnabled = true
				gwCfg.SFUPort = sfuBlock.SFUSignalingPort
			}
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
	_, err := cm.db.Exec(ctx, query, status, time.Now().UTC().Format("2006-01-02 15:04:05"), clusterID, nodeID)
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

// regenerateClusterState rebuilds cluster-state.json on every node from the
// CURRENT port allocations (bugboard #280).
//
// cluster-state.json is what the namespace gateway config is generated from —
// olric_servers and the rqlite join list both come from its all_nodes. It was
// only ever written by the provisioning paths, so after a node was permanently
// removed the file kept naming it forever: RepairCluster never rewrote it, the
// startup restore only reads it, and the warm-reconcile path compares just the
// WebRTC fields. Regenerating it whenever membership changes is what stops a
// namespace from pointing at departed nodes.
func (cm *ClusterManager) regenerateClusterState(ctx context.Context, cluster *NamespaceCluster) error {
	nodes, blocks, err := cm.clusterStateInputs(ctx, cluster)
	if err != nil {
		return err
	}
	cm.saveClusterStateToAllNodes(ctx, cluster, nodes, blocks)
	cm.logger.Info("Regenerated namespace cluster state from live allocations (bugboard #280)",
		zap.String("namespace", cluster.NamespaceName),
		zap.Int("nodes", len(nodes)))
	return nil
}

// clusterStateInputs reads the cluster's CURRENT membership from the allocations
// table and shapes it for saveClusterStateToAllNodes. Split out from
// regenerateClusterState so the membership computation — the part that actually
// went wrong in bugboard #280 — is testable without touching disk or the network.
func (cm *ClusterManager) clusterStateInputs(ctx context.Context, cluster *NamespaceCluster) ([]NodeCapacity, []*PortBlock, error) {
	type allocRow struct {
		NodeID              string `db:"node_id"`
		InternalIP          string `db:"internal_ip"`
		IPAddress           string `db:"ip_address"`
		RQLiteHTTPPort      int    `db:"rqlite_http_port"`
		RQLiteRaftPort      int    `db:"rqlite_raft_port"`
		OlricHTTPPort       int    `db:"olric_http_port"`
		OlricMemberlistPort int    `db:"olric_memberlist_port"`
		GatewayHTTPPort     int    `db:"gateway_http_port"`
	}
	var rows []allocRow
	query := `
		SELECT pa.node_id, COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip, dn.ip_address,
			pa.rqlite_http_port, pa.rqlite_raft_port, pa.olric_http_port,
			pa.olric_memberlist_port, pa.gateway_http_port
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ?
		ORDER BY pa.node_id
	`
	if err := cm.db.Query(client.WithInternalAuth(ctx), &rows, query, cluster.ID); err != nil {
		return nil, nil, fmt.Errorf("regenerate cluster state: query allocations: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("regenerate cluster state: no port allocations for cluster %s", cluster.ID)
	}

	nodes := make([]NodeCapacity, 0, len(rows))
	blocks := make([]*PortBlock, 0, len(rows))
	for _, r := range rows {
		nodes = append(nodes, NodeCapacity{
			NodeID:     r.NodeID,
			IPAddress:  r.IPAddress,
			InternalIP: r.InternalIP,
		})
		blocks = append(blocks, &PortBlock{
			NodeID:              r.NodeID,
			NamespaceClusterID:  cluster.ID,
			RQLiteHTTPPort:      r.RQLiteHTTPPort,
			RQLiteRaftPort:      r.RQLiteRaftPort,
			OlricHTTPPort:       r.OlricHTTPPort,
			OlricMemberlistPort: r.OlricMemberlistPort,
			GatewayHTTPPort:     r.GatewayHTTPPort,
		})
	}

	return nodes, blocks, nil
}

// removeStalePortAllocation drops a departed node's namespace_port_allocations
// row (bugboard #280). The counterpart to removeClusterNodeAssignment: both rows
// describe the same membership, and leaving one behind is what let stale nodes
// survive in generated namespace gateway config.
func (cm *ClusterManager) removeStalePortAllocation(ctx context.Context, clusterID, nodeID string) {
	query := `DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`
	if _, err := cm.db.Exec(ctx, query, clusterID, nodeID); err != nil {
		cm.logger.Warn("Failed to remove stale port allocation",
			zap.String("cluster_id", clusterID),
			zap.String("node_id", nodeID),
			zap.Error(err))
	}
}

// clusterNodePurgeStaleAfter mirrors purgeStaleAfter in
// pkg/node/dns_registration.go:514 (15m) — the DNS layer already treats a
// node silent this long as genuinely gone and purges its records, so cluster
// membership should not keep carrying it any longer than DNS does. It cannot
// be imported directly: pkg/node imports pkg/namespace (pkg/node/gateway.go),
// so the reverse import would be a cycle. Keep the two values in sync by hand
// if either changes.
//
// Deliberately longer than webrtcMemberGracePeriod (10m): deleting the
// namespace_cluster_nodes ROW is a more consequential, harder-to-undo action
// than merely excluding a node from one WebRTC reconcile pass, so it gets
// extra margin on top of that grace before acting. A node down for less than
// this is still within a routine rolling-restart window, not gone.
const clusterNodePurgeStaleAfter = 15 * time.Minute

// staleClusterNodeSQL selects cluster members that are permanently gone:
// recorded in namespace_cluster_nodes for this cluster, but their dns_nodes
// row is non-active and has been silent since before the caller-supplied
// cutoff. Mirrors the shape of webrtcViableMemberSQL (cluster_manager_webrtc.go)
// but selects the STALE complement on the longer clusterNodePurgeStaleAfter
// horizon rather than the WebRTC-specific grace period. Args: clusterID, cutoff
// ("YYYY-MM-DD HH:MM:SS", UTC — see staleCutoff-style callers).
const staleClusterNodeSQL = `
	SELECT DISTINCT ncn.node_id
	FROM namespace_cluster_nodes ncn
	JOIN dns_nodes dn ON ncn.node_id = dn.id
	WHERE ncn.namespace_cluster_id = ?
	  AND dn.status != 'active'
	  AND dn.last_seen < ?
`

// pruneStaleClusterNodes removes namespace_cluster_nodes rows for members
// that are permanently gone (bugboard #173).
//
// Root cause: RepairCluster only ever ADDS members, never removes stale ones,
// and removeClusterNodeAssignment was otherwise only reachable through
// ReplaceClusterNode — which only runs when HandleDeadNode fires. On devnet
// that path never fired for two nodes that really were gone: the DNS
// heartbeat loop (startDNSHeartbeat, 30s tick) flips a silent node's
// dns_nodes.status to 'inactive' after just 120s
// (cleanupStaleNodeRecords, pkg/node/dns_registration.go), and the ring-based
// health monitor's getRingNeighbors (pkg/node/health/monitor.go) only
// considers status='active' nodes as probe targets. Once a node flips
// inactive it drops out of every node's neighbor set, pruneStaleState wipes
// its accumulated miss count, and it can never again reach the monitor's own
// 12-miss dead threshold — so onNodeDead, and therefore HandleDeadNode and
// this row's cleanup, permanently never fires for a node that dies this way.
// That leaves the namespace_cluster_nodes row an orphan forever, inflating
// both the WebRTC quorum denominator (bugboard #170/#171) and RepairCluster's
// "already have enough nodes" count. This sweep closes that gap
// independently of whether the ring monitor ever confirms death, using
// dns_nodes staleness directly rather than relying on that confirmation.
//
// Not exclusive to one coordinator: the DELETE is idempotent, so more than
// one node running this concurrently just performs the same no-op delete
// twice — no lock or election is needed the way role reallocation needs one.
func (cm *ClusterManager) pruneStaleClusterNodes(ctx context.Context, clusterID string) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	type row struct {
		NodeID string `db:"node_id"`
	}
	var rows []row
	cutoff := time.Now().UTC().Add(-clusterNodePurgeStaleAfter).Format("2006-01-02 15:04:05")
	if err := cm.db.Query(internalCtx, &rows, staleClusterNodeSQL, clusterID, cutoff); err != nil {
		return nil, fmt.Errorf("query stale cluster nodes: %w", err)
	}

	removed := make([]string, 0, len(rows))
	for _, r := range rows {
		cm.removeClusterNodeAssignment(ctx, clusterID, r.NodeID)
		// Bugboard #280: the port allocation must go too. Pruning only
		// namespace_cluster_nodes left the allocation row behind, and that row is
		// what cluster-state.json (and therefore the namespace gateway's
		// olric_servers / rqlite join list) is built from — so a namespace kept
		// pointing at a departed node forever. On devnet that left anchat-test
		// with Olric discovery aimed at two removed nodes: its cache was down on
		// every gateway, and each gateway restart stalled for MINUTES timing out
		// against them before it would bind.
		cm.removeStalePortAllocation(ctx, clusterID, r.NodeID)
		removed = append(removed, r.NodeID)
		cm.logger.Warn("Removed permanently-gone cluster node assignment and its port allocation (bugboard #173, #280)",
			zap.String("cluster_id", clusterID),
			zap.String("node_id", r.NodeID))
	}
	return removed, nil
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
	_, err := cm.db.Exec(ctx, query, time.Now().UTC().Format("2006-01-02 15:04:05"), nodeID)
	return err
}

// markNodeActive sets a node's status to 'active' in dns_nodes.
func (cm *ClusterManager) markNodeActive(ctx context.Context, nodeID string) {
	query := `UPDATE dns_nodes SET status = 'active', updated_at = ? WHERE id = ?`
	if _, err := cm.db.Exec(ctx, query, time.Now().UTC().Format("2006-01-02 15:04:05"), nodeID); err != nil {
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

	// 3. Prune permanently-gone members before counting what's missing
	// (bugboard #173). Without this, a member that died without
	// HandleDeadNode ever confirming it (see pruneStaleClusterNodes for why
	// that confirmation can permanently never arrive) keeps its
	// namespace_cluster_nodes row and its status forever — RepairCluster
	// would count it as active below and never trigger a replacement.
	if removed, perr := cm.pruneStaleClusterNodes(ctx, cluster.ID); perr != nil {
		cm.logger.Warn("Failed to prune stale cluster nodes during repair — proceeding with unpruned membership",
			zap.String("namespace", namespaceName), zap.Error(perr))
	} else if len(removed) > 0 {
		cm.logger.Warn("Repair pruned permanently-gone cluster node assignments (bugboard #173)",
			zap.String("namespace", namespaceName), zap.Strings("removed_nodes", removed))
		// Bugboard #280: membership changed, so the state file every namespace
		// gateway config is generated from is now stale. Rewrite it, otherwise the
		// pruned nodes stay in olric_servers and the rqlite join list forever.
		if rerr := cm.regenerateClusterState(ctx, cluster); rerr != nil {
			cm.logger.Warn("Failed to regenerate cluster state after pruning — namespace config may still name departed nodes",
				zap.String("namespace", namespaceName), zap.Error(rerr))
		}
	}

	// 4. Get current cluster nodes
	clusterNodes, err := cm.getClusterNodes(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	// Bugboard #279: reinstate members that are marked failed but whose node is
	// alive again. Without this a node that merely restarted stays failed forever:
	// pruneStaleClusterNodes above only removes nodes that are NOT active, and the
	// replacement path below excludes every current member, so on a fleet-wide
	// cluster there is nothing to replace it with and the cluster never recovers.
	reinstated := 0
	for _, cn := range clusterNodes {
		if cn.Status != NodeStatusFailed {
			continue
		}
		if !cm.nodeIsLive(ctx, cn.NodeID) {
			continue
		}
		if err := cm.reinstateNode(ctx, cluster, cn.NodeID); err != nil {
			cm.logger.Warn("Failed to reinstate live cluster node",
				zap.String("namespace", namespaceName),
				zap.String("node_id", cn.NodeID), zap.Error(err))
			continue
		}
		reinstated++
	}
	if reinstated > 0 {
		if clusterNodes, err = cm.getClusterNodes(ctx, cluster.ID); err != nil {
			return fmt.Errorf("failed to re-read cluster nodes after reinstate: %w", err)
		}
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
			zap.Int("reinstated", reinstated),
		)
		// Bugboard #279: clear a stale 'degraded' — returning early without doing
		// this is what left a fully-recovered cluster marked degraded, which the
		// edge router then refused to serve (bugboard #278).
		return cm.settleClusterStatus(ctx, cluster)
	}

	cm.logger.Info("Cluster needs repair — adding missing nodes",
		zap.String("namespace", namespaceName),
		zap.Int("active_nodes", activeCount),
		zap.Int("expected", expectedCount),
		zap.Int("missing", missingCount),
	)

	cm.logEvent(ctx, cluster.ID, EventRecoveryStarted, "",
		fmt.Sprintf("Cluster repair started: %d of %d nodes active, adding %d", activeCount, expectedCount, missingCount), nil)

	// 5. Build the current node exclude list (all physical node IDs in the cluster)
	excludeIDs := make([]string, 0)
	nodeIDSet := make(map[string]bool)
	for _, cn := range clusterNodes {
		if !nodeIDSet[cn.NodeID] {
			nodeIDSet[cn.NodeID] = true
			excludeIDs = append(excludeIDs, cn.NodeID)
		}
	}

	// 6. Get surviving nodes' port info for joining
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

	// 7. Add missing nodes one at a time
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

	// 8. Update cluster-state.json on all nodes
	cm.updateClusterStateAfterRecovery(ctx, cluster)

	// 9. Mark cluster ready
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
	portBlock, err := cm.portAllocator.AllocatePortBlock(ctx, replacement.NodeID, cluster.ID, BlueprintTenant())
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
		SecretsEncryptionKey:  cm.secretsEncryptionKey,
		NtfyBaseURL:           cm.ntfyBaseURL,
	}

	// Add WebRTC config if enabled for this namespace. TURN/SFU decoupled — see
	// the identical block in HandleDeadNode above (bugboard #25): the
	// namespace-wide TURN secret must be set even with no SFU allocation, or this
	// replacement gateway serves 503 on /v1/webrtc/turn/credentials.
	if webrtcCfg, err := cm.GetWebRTCConfig(ctx, cluster.NamespaceName); err == nil && webrtcCfg != nil {
		gwCfg.TURNDomain = fmt.Sprintf("turn.ns-%s.%s", cluster.NamespaceName, cm.baseDomain)
		gwCfg.TURNSecret = webrtcCfg.TURNSharedSecret
		gwCfg.TURNStealthDomain = cm.stealthDomainFor(cluster.NamespaceName, webrtcCfg)
		if sfuBlock, serr := cm.webrtcPortAllocator.GetSFUPorts(ctx, cluster.ID, replacement.NodeID); serr == nil && sfuBlock != nil {
			gwCfg.WebRTCEnabled = true
			gwCfg.SFUPort = sfuBlock.SFUSignalingPort
		}
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

// markDeadNodeReplicasFailed marks all deployment replicas on a dead node as
// 'failed' and recalculates each affected deployment's status. This ensures
// routing immediately excludes the dead node instead of discovering it's
// unreachable through timeouts.
func (cm *ClusterManager) markDeadNodeReplicasFailed(ctx context.Context, deadNodeID string) {
	// Find all active deployment replicas on the dead node.
	type affectedReplica struct {
		DeploymentID string `db:"deployment_id"`
	}
	var affected []affectedReplica
	findQuery := `SELECT DISTINCT deployment_id FROM deployment_replicas WHERE node_id = ? AND status = 'active'`
	if err := cm.db.Query(ctx, &affected, findQuery, deadNodeID); err != nil {
		cm.logger.Warn("Failed to query deployment replicas for dead node",
			zap.String("dead_node", deadNodeID), zap.Error(err))
		return
	}

	if len(affected) == 0 {
		return
	}

	cm.logger.Info("Marking deployment replicas on dead node as failed",
		zap.String("dead_node", deadNodeID),
		zap.Int("replica_count", len(affected)),
	)

	// Mark all replicas on the dead node as failed in a single UPDATE.
	markQuery := `UPDATE deployment_replicas SET status = 'failed' WHERE node_id = ? AND status = 'active'`
	if _, err := cm.db.Exec(ctx, markQuery, deadNodeID); err != nil {
		cm.logger.Error("Failed to mark deployment replicas as failed",
			zap.String("dead_node", deadNodeID), zap.Error(err))
		return
	}

	// Recalculate each affected deployment's status based on remaining active replicas.
	type replicaCount struct {
		Count int `db:"count"`
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	for _, a := range affected {
		var counts []replicaCount
		countQuery := `SELECT COUNT(*) as count FROM deployment_replicas WHERE deployment_id = ? AND status = 'active'`
		if err := cm.db.Query(ctx, &counts, countQuery, a.DeploymentID); err != nil {
			cm.logger.Warn("Failed to count active replicas for deployment",
				zap.String("deployment_id", a.DeploymentID), zap.Error(err))
			continue
		}

		activeCount := 0
		if len(counts) > 0 {
			activeCount = counts[0].Count
		}

		if activeCount > 0 {
			// Some replicas still alive — degraded, not dead.
			statusQuery := `UPDATE deployments SET status = 'degraded' WHERE id = ? AND status = 'active'`
			cm.db.Exec(ctx, statusQuery, a.DeploymentID)
			cm.logger.Warn("Deployment degraded — replica on dead node marked failed",
				zap.String("deployment_id", a.DeploymentID),
				zap.String("dead_node", deadNodeID),
				zap.Int("remaining_active", activeCount),
			)
		} else {
			// No replicas alive — deployment is failed.
			statusQuery := `UPDATE deployments SET status = 'failed' WHERE id = ? AND status IN ('active', 'degraded')`
			cm.db.Exec(ctx, statusQuery, a.DeploymentID)
			cm.logger.Error("Deployment failed — all replicas on dead node",
				zap.String("deployment_id", a.DeploymentID),
				zap.String("dead_node", deadNodeID),
			)
		}

		// Log event for audit trail.
		eventQuery := `INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, 'node_death_replica_failed', ?, ?)`
		msg := fmt.Sprintf("Replica on node %s marked failed (node confirmed dead), %d active replicas remaining", deadNodeID, activeCount)
		cm.db.Exec(ctx, eventQuery, a.DeploymentID, msg, now)
	}
}
