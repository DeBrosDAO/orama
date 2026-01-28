package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	rqliteClient "github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ClusterManager orchestrates namespace cluster provisioning and lifecycle management.
// It coordinates the creation and teardown of RQLite, Olric, and Gateway instances
// for each namespace's dedicated cluster.
type ClusterManager struct {
	db              rqliteClient.Client
	portAllocator   *NamespacePortAllocator
	nodeSelector    *ClusterNodeSelector
	rqliteSpawner   *rqlite.InstanceSpawner
	olricSpawner    *olric.InstanceSpawner
	gatewaySpawner  *gateway.InstanceSpawner
	dnsManager      *DNSRecordManager
	baseDomain      string
	baseDataDir     string // Base directory for namespace data (e.g., ~/.orama/data/namespaces)
	logger          *zap.Logger
}

// ClusterManagerConfig holds configuration for the ClusterManager
type ClusterManagerConfig struct {
	BaseDomain  string // e.g., "devnet-orama.network"
	BaseDataDir string // e.g., "~/.orama/data/namespaces"
}

// NewClusterManager creates a new cluster manager
func NewClusterManager(
	db rqliteClient.Client,
	cfg ClusterManagerConfig,
	logger *zap.Logger,
) *ClusterManager {
	portAllocator := NewNamespacePortAllocator(db, logger)

	return &ClusterManager{
		db:             db,
		portAllocator:  portAllocator,
		nodeSelector:   NewClusterNodeSelector(db, portAllocator, logger),
		rqliteSpawner:  rqlite.NewInstanceSpawner(cfg.BaseDataDir, logger),
		olricSpawner:   olric.NewInstanceSpawner(cfg.BaseDataDir, logger),
		gatewaySpawner: gateway.NewInstanceSpawner(cfg.BaseDataDir, logger),
		dnsManager:     NewDNSRecordManager(db, cfg.BaseDomain, logger),
		baseDomain:     cfg.BaseDomain,
		baseDataDir:    cfg.BaseDataDir,
		logger:         logger.With(zap.String("component", "cluster-manager")),
	}
}

// ProvisionCluster provisions a complete namespace cluster (RQLite + Olric + Gateway).
// This is an asynchronous operation that returns immediately with a cluster ID.
// Use GetClusterStatus to poll for completion.
func (cm *ClusterManager) ProvisionCluster(ctx context.Context, namespaceID int, namespaceName, provisionedBy string) (*NamespaceCluster, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Check if cluster already exists
	existing, err := cm.GetClusterByNamespaceID(ctx, namespaceID)
	if err == nil && existing != nil {
		if existing.Status == ClusterStatusReady {
			return existing, nil
		}
		if existing.Status == ClusterStatusProvisioning {
			return existing, nil // Already provisioning
		}
		// If failed or deprovisioning, allow re-provisioning
	}

	// Create cluster record
	clusterID := uuid.New().String()
	cluster := &NamespaceCluster{
		ID:               clusterID,
		NamespaceID:      namespaceID,
		NamespaceName:    namespaceName,
		Status:           ClusterStatusProvisioning,
		RQLiteNodeCount:  DefaultRQLiteNodeCount,
		OlricNodeCount:   DefaultOlricNodeCount,
		GatewayNodeCount: DefaultGatewayNodeCount,
		ProvisionedBy:    provisionedBy,
		ProvisionedAt:    time.Now(),
	}

	// Insert cluster record
	insertQuery := `
		INSERT INTO namespace_clusters (
			id, namespace_id, namespace_name, status,
			rqlite_node_count, olric_node_count, gateway_node_count,
			provisioned_by, provisioned_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = cm.db.Exec(internalCtx, insertQuery,
		cluster.ID,
		cluster.NamespaceID,
		cluster.NamespaceName,
		string(cluster.Status),
		cluster.RQLiteNodeCount,
		cluster.OlricNodeCount,
		cluster.GatewayNodeCount,
		cluster.ProvisionedBy,
		cluster.ProvisionedAt,
	)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to create cluster record",
			Cause:   err,
		}
	}

	// Log provisioning started event
	cm.logEvent(internalCtx, clusterID, EventProvisioningStarted, "", "Cluster provisioning started", nil)

	// Start async provisioning
	go cm.doProvisioning(context.Background(), cluster)

	return cluster, nil
}

// doProvisioning performs the actual cluster provisioning asynchronously
func (cm *ClusterManager) doProvisioning(ctx context.Context, cluster *NamespaceCluster) {
	internalCtx := client.WithInternalAuth(ctx)

	cm.logger.Info("Starting cluster provisioning",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
	)

	// Step 1: Select nodes for the cluster
	selectedNodes, err := cm.nodeSelector.SelectNodesForCluster(internalCtx, DefaultRQLiteNodeCount)
	if err != nil {
		cm.failCluster(internalCtx, cluster.ID, "Failed to select nodes: "+err.Error())
		return
	}

	nodeIDs := make([]string, len(selectedNodes))
	for i, n := range selectedNodes {
		nodeIDs[i] = n.NodeID
	}
	cm.logEvent(internalCtx, cluster.ID, EventNodesSelected, "", "Selected nodes for cluster", map[string]interface{}{
		"node_ids": nodeIDs,
	})

	// Step 2: Allocate port blocks on each node
	portBlocks := make([]*PortBlock, len(selectedNodes))
	for i, node := range selectedNodes {
		block, err := cm.portAllocator.AllocatePortBlock(internalCtx, node.NodeID, cluster.ID)
		if err != nil {
			cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to allocate ports on node %s: %v", node.NodeID, err))
			// Cleanup already allocated ports
			for j := 0; j < i; j++ {
				_ = cm.portAllocator.DeallocatePortBlock(internalCtx, cluster.ID, selectedNodes[j].NodeID)
			}
			return
		}
		portBlocks[i] = block
		cm.logEvent(internalCtx, cluster.ID, EventPortsAllocated, node.NodeID,
			fmt.Sprintf("Allocated ports %d-%d", block.PortStart, block.PortEnd), nil)
	}

	// Step 3: Start RQLite instances
	// First node is the leader, others join it
	rqliteInstances := make([]*rqlite.RQLiteInstance, len(selectedNodes))

	// Start leader first
	leaderNode := selectedNodes[0]
	leaderPorts := portBlocks[0]
	leaderConfig := rqlite.InstanceConfig{
		Namespace:      cluster.NamespaceName,
		NodeID:         leaderNode.NodeID,
		HTTPPort:       leaderPorts.RQLiteHTTPPort,
		RaftPort:       leaderPorts.RQLiteRaftPort,
		HTTPAdvAddress: fmt.Sprintf("%s:%d", leaderNode.IPAddress, leaderPorts.RQLiteHTTPPort),
		RaftAdvAddress: fmt.Sprintf("%s:%d", leaderNode.IPAddress, leaderPorts.RQLiteRaftPort),
		IsLeader:       true,
	}

	leaderInstance, err := cm.rqliteSpawner.SpawnInstance(internalCtx, leaderConfig)
	if err != nil {
		cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to start RQLite leader: %v", err))
		cm.cleanupOnFailure(internalCtx, cluster.ID, selectedNodes, portBlocks)
		return
	}
	rqliteInstances[0] = leaderInstance
	cm.logEvent(internalCtx, cluster.ID, EventRQLiteStarted, leaderNode.NodeID, "RQLite leader started", nil)

	// Create cluster node record for leader
	cm.createClusterNodeRecord(internalCtx, cluster.ID, leaderNode.NodeID, NodeRoleRQLiteLeader, leaderPorts, leaderInstance.PID)

	// Start followers and join them to leader
	leaderJoinAddr := leaderInstance.AdvertisedDSN()
	for i := 1; i < len(selectedNodes); i++ {
		node := selectedNodes[i]
		ports := portBlocks[i]
		followerConfig := rqlite.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         node.NodeID,
			HTTPPort:       ports.RQLiteHTTPPort,
			RaftPort:       ports.RQLiteRaftPort,
			HTTPAdvAddress: fmt.Sprintf("%s:%d", node.IPAddress, ports.RQLiteHTTPPort),
			RaftAdvAddress: fmt.Sprintf("%s:%d", node.IPAddress, ports.RQLiteRaftPort),
			JoinAddresses:  []string{leaderJoinAddr},
			IsLeader:       false,
		}

		followerInstance, err := cm.rqliteSpawner.SpawnInstance(internalCtx, followerConfig)
		if err != nil {
			cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to start RQLite follower on node %s: %v", node.NodeID, err))
			cm.cleanupOnFailure(internalCtx, cluster.ID, selectedNodes, portBlocks)
			return
		}
		rqliteInstances[i] = followerInstance
		cm.logEvent(internalCtx, cluster.ID, EventRQLiteJoined, node.NodeID, "RQLite follower joined cluster", nil)
		cm.createClusterNodeRecord(internalCtx, cluster.ID, node.NodeID, NodeRoleRQLiteFollower, ports, followerInstance.PID)
	}

	cm.logEvent(internalCtx, cluster.ID, EventRQLiteLeaderElected, leaderNode.NodeID, "RQLite cluster formed", nil)

	// Step 4: Start Olric instances
	// Collect all memberlist addresses for peer discovery
	olricPeers := make([]string, len(selectedNodes))
	for i, node := range selectedNodes {
		olricPeers[i] = fmt.Sprintf("%s:%d", node.IPAddress, portBlocks[i].OlricMemberlistPort)
	}

	for i, node := range selectedNodes {
		ports := portBlocks[i]
		olricConfig := olric.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         node.NodeID,
			HTTPPort:       ports.OlricHTTPPort,
			MemberlistPort: ports.OlricMemberlistPort,
			BindAddr:       "0.0.0.0",
			AdvertiseAddr:  node.IPAddress,
			PeerAddresses:  olricPeers,
		}

		_, err := cm.olricSpawner.SpawnInstance(internalCtx, olricConfig)
		if err != nil {
			cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to start Olric on node %s: %v", node.NodeID, err))
			cm.cleanupOnFailure(internalCtx, cluster.ID, selectedNodes, portBlocks)
			return
		}
		cm.logEvent(internalCtx, cluster.ID, EventOlricStarted, node.NodeID, "Olric instance started", nil)

		// Update cluster node record with Olric role
		cm.updateClusterNodeOlricStatus(internalCtx, cluster.ID, node.NodeID)
	}

	cm.logEvent(internalCtx, cluster.ID, EventOlricJoined, "", "Olric cluster formed", nil)

	// Step 5: Start Gateway instances
	// Build Olric server list for gateway config
	olricServers := make([]string, len(selectedNodes))
	for i, node := range selectedNodes {
		olricServers[i] = fmt.Sprintf("%s:%d", node.IPAddress, portBlocks[i].OlricHTTPPort)
	}

	for i, node := range selectedNodes {
		ports := portBlocks[i]
		gatewayConfig := gateway.InstanceConfig{
			Namespace:    cluster.NamespaceName,
			NodeID:       node.NodeID,
			HTTPPort:     ports.GatewayHTTPPort,
			BaseDomain:   cm.baseDomain,
			RQLiteDSN:    fmt.Sprintf("http://%s:%d", node.IPAddress, ports.RQLiteHTTPPort),
			OlricServers: olricServers,
			NodePeerID:   node.NodeID, // Use node ID as peer ID
			DataDir:      cm.baseDataDir,
		}

		_, err := cm.gatewaySpawner.SpawnInstance(internalCtx, gatewayConfig)
		if err != nil {
			cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to start Gateway on node %s: %v", node.NodeID, err))
			cm.cleanupOnFailure(internalCtx, cluster.ID, selectedNodes, portBlocks)
			return
		}
		cm.logEvent(internalCtx, cluster.ID, EventGatewayStarted, node.NodeID, "Gateway instance started", nil)

		// Update cluster node record with Gateway role
		cm.updateClusterNodeGatewayStatus(internalCtx, cluster.ID, node.NodeID)
	}

	// Step 6: Create DNS records for namespace gateway
	nodeIPs := make([]string, len(selectedNodes))
	for i, node := range selectedNodes {
		nodeIPs[i] = node.IPAddress
	}

	if err := cm.dnsManager.CreateNamespaceRecords(internalCtx, cluster.NamespaceName, nodeIPs); err != nil {
		cm.failCluster(internalCtx, cluster.ID, fmt.Sprintf("Failed to create DNS records: %v", err))
		cm.cleanupOnFailure(internalCtx, cluster.ID, selectedNodes, portBlocks)
		return
	}
	cm.logEvent(internalCtx, cluster.ID, EventDNSCreated, "", "DNS records created", map[string]interface{}{
		"domain":   fmt.Sprintf("ns-%s.%s", cluster.NamespaceName, cm.baseDomain),
		"node_ips": nodeIPs,
	})

	// Mark cluster as ready
	now := time.Now()
	updateQuery := `UPDATE namespace_clusters SET status = ?, ready_at = ? WHERE id = ?`
	_, err = cm.db.Exec(internalCtx, updateQuery, string(ClusterStatusReady), now, cluster.ID)
	if err != nil {
		cm.logger.Error("Failed to update cluster status to ready",
			zap.String("cluster_id", cluster.ID),
			zap.Error(err),
		)
	}

	cm.logEvent(internalCtx, cluster.ID, EventClusterReady, "", "Cluster is ready", nil)

	cm.logger.Info("Cluster provisioning completed",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
	)
}

// DeprovisionCluster tears down all services for a namespace cluster
func (cm *ClusterManager) DeprovisionCluster(ctx context.Context, clusterID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	// Get cluster info
	cluster, err := cm.GetCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	cm.logger.Info("Starting cluster deprovisioning",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", cluster.NamespaceName),
	)

	// Update status to deprovisioning
	updateQuery := `UPDATE namespace_clusters SET status = ? WHERE id = ?`
	_, _ = cm.db.Exec(internalCtx, updateQuery, string(ClusterStatusDeprovisioning), clusterID)
	cm.logEvent(internalCtx, clusterID, EventDeprovisionStarted, "", "Cluster deprovisioning started", nil)

	// Stop all gateway instances
	if err := cm.gatewaySpawner.StopAllInstances(ctx, cluster.NamespaceName); err != nil {
		cm.logger.Warn("Error stopping gateway instances", zap.Error(err))
	}

	// Stop all olric instances
	if err := cm.olricSpawner.StopAllInstances(ctx, cluster.NamespaceName); err != nil {
		cm.logger.Warn("Error stopping olric instances", zap.Error(err))
	}

	// Stop all rqlite instances
	if err := cm.rqliteSpawner.StopAllInstances(ctx, cluster.NamespaceName); err != nil {
		cm.logger.Warn("Error stopping rqlite instances", zap.Error(err))
	}

	// Delete DNS records
	if err := cm.dnsManager.DeleteNamespaceRecords(ctx, cluster.NamespaceName); err != nil {
		cm.logger.Warn("Error deleting DNS records", zap.Error(err))
	}

	// Deallocate all ports
	if err := cm.portAllocator.DeallocateAllPortBlocks(ctx, clusterID); err != nil {
		cm.logger.Warn("Error deallocating ports", zap.Error(err))
	}

	// Delete cluster node records
	deleteNodesQuery := `DELETE FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`
	_, _ = cm.db.Exec(internalCtx, deleteNodesQuery, clusterID)

	// Delete cluster record
	deleteClusterQuery := `DELETE FROM namespace_clusters WHERE id = ?`
	_, err = cm.db.Exec(internalCtx, deleteClusterQuery, clusterID)
	if err != nil {
		return &ClusterError{
			Message: "failed to delete cluster record",
			Cause:   err,
		}
	}

	cm.logEvent(internalCtx, clusterID, EventDeprovisioned, "", "Cluster deprovisioned", nil)

	cm.logger.Info("Cluster deprovisioning completed",
		zap.String("cluster_id", clusterID),
		zap.String("namespace", cluster.NamespaceName),
	)

	return nil
}

// GetCluster retrieves a cluster by ID
func (cm *ClusterManager) GetCluster(ctx context.Context, clusterID string) (*NamespaceCluster, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE id = ? LIMIT 1`
	err := cm.db.Query(internalCtx, &clusters, query, clusterID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query cluster",
			Cause:   err,
		}
	}

	if len(clusters) == 0 {
		return nil, ErrClusterNotFound
	}

	return &clusters[0], nil
}

// GetClusterByNamespaceID retrieves a cluster by namespace ID
func (cm *ClusterManager) GetClusterByNamespaceID(ctx context.Context, namespaceID int) (*NamespaceCluster, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE namespace_id = ? LIMIT 1`
	err := cm.db.Query(internalCtx, &clusters, query, namespaceID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query cluster",
			Cause:   err,
		}
	}

	if len(clusters) == 0 {
		return nil, ErrClusterNotFound
	}

	return &clusters[0], nil
}

// GetClusterByNamespaceName retrieves a cluster by namespace name
func (cm *ClusterManager) GetClusterByNamespaceName(ctx context.Context, namespaceName string) (*NamespaceCluster, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE namespace_name = ? LIMIT 1`
	err := cm.db.Query(internalCtx, &clusters, query, namespaceName)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query cluster",
			Cause:   err,
		}
	}

	if len(clusters) == 0 {
		return nil, ErrClusterNotFound
	}

	return &clusters[0], nil
}

// GetClusterStatus returns the detailed provisioning status of a cluster
func (cm *ClusterManager) GetClusterStatus(ctx context.Context, clusterID string) (*ClusterProvisioningStatus, error) {
	cluster, err := cm.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	// Get cluster nodes
	internalCtx := client.WithInternalAuth(ctx)
	var nodes []ClusterNode
	nodesQuery := `SELECT * FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`
	_ = cm.db.Query(internalCtx, &nodes, nodesQuery, clusterID)

	// Determine component readiness
	rqliteReady := false
	olricReady := false
	gatewayReady := false

	rqliteCount := 0
	olricCount := 0
	gatewayCount := 0

	for _, node := range nodes {
		if node.Status == NodeStatusRunning {
			switch node.Role {
			case NodeRoleRQLiteLeader, NodeRoleRQLiteFollower:
				rqliteCount++
			case NodeRoleOlric:
				olricCount++
			case NodeRoleGateway:
				gatewayCount++
			}
		}
	}

	// Consider ready if we have the expected number of each type
	rqliteReady = rqliteCount >= cluster.RQLiteNodeCount
	olricReady = olricCount >= cluster.OlricNodeCount
	gatewayReady = gatewayCount >= cluster.GatewayNodeCount

	// DNS is ready if cluster status is ready
	dnsReady := cluster.Status == ClusterStatusReady

	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.NodeID
	}

	status := &ClusterProvisioningStatus{
		ClusterID:    cluster.ID,
		Namespace:    cluster.NamespaceName,
		Status:       cluster.Status,
		Nodes:        nodeIDs,
		RQLiteReady:  rqliteReady,
		OlricReady:   olricReady,
		GatewayReady: gatewayReady,
		DNSReady:     dnsReady,
		Error:        cluster.ErrorMessage,
		CreatedAt:    cluster.ProvisionedAt,
		ReadyAt:      cluster.ReadyAt,
	}

	return status, nil
}

// failCluster marks a cluster as failed with an error message
func (cm *ClusterManager) failCluster(ctx context.Context, clusterID, errorMsg string) {
	cm.logger.Error("Cluster provisioning failed",
		zap.String("cluster_id", clusterID),
		zap.String("error", errorMsg),
	)

	updateQuery := `UPDATE namespace_clusters SET status = ?, error_message = ?, retry_count = retry_count + 1 WHERE id = ?`
	_, _ = cm.db.Exec(ctx, updateQuery, string(ClusterStatusFailed), errorMsg, clusterID)

	cm.logEvent(ctx, clusterID, EventClusterFailed, "", errorMsg, nil)
}

// cleanupOnFailure cleans up partial resources after a provisioning failure
func (cm *ClusterManager) cleanupOnFailure(ctx context.Context, clusterID string, nodes []NodeCapacity, portBlocks []*PortBlock) {
	// Get namespace name from first port block
	var namespaceName string
	if len(portBlocks) > 0 {
		// Query to get namespace name from cluster
		var clusters []NamespaceCluster
		query := `SELECT namespace_name FROM namespace_clusters WHERE id = ? LIMIT 1`
		if err := cm.db.Query(ctx, &clusters, query, clusterID); err == nil && len(clusters) > 0 {
			namespaceName = clusters[0].NamespaceName
		}
	}

	if namespaceName != "" {
		// Stop any started instances
		_ = cm.gatewaySpawner.StopAllInstances(ctx, namespaceName)
		_ = cm.olricSpawner.StopAllInstances(ctx, namespaceName)
		_ = cm.rqliteSpawner.StopAllInstances(ctx, namespaceName)
	}

	// Deallocate ports
	for _, node := range nodes {
		_ = cm.portAllocator.DeallocatePortBlock(ctx, clusterID, node.NodeID)
	}

	// Delete cluster node records
	deleteQuery := `DELETE FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`
	_, _ = cm.db.Exec(ctx, deleteQuery, clusterID)
}

// logEvent logs a cluster lifecycle event
func (cm *ClusterManager) logEvent(ctx context.Context, clusterID string, eventType EventType, nodeID, message string, metadata map[string]interface{}) {
	eventID := uuid.New().String()

	var metadataJSON string
	if metadata != nil {
		data, _ := json.Marshal(metadata)
		metadataJSON = string(data)
	}

	insertQuery := `
		INSERT INTO namespace_cluster_events (id, namespace_cluster_id, event_type, node_id, message, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, _ = cm.db.Exec(ctx, insertQuery, eventID, clusterID, string(eventType), nodeID, message, metadataJSON, time.Now())

	cm.logger.Debug("Cluster event logged",
		zap.String("cluster_id", clusterID),
		zap.String("event_type", string(eventType)),
		zap.String("node_id", nodeID),
		zap.String("message", message),
	)
}

// createClusterNodeRecord creates a record for a node in the cluster
func (cm *ClusterManager) createClusterNodeRecord(ctx context.Context, clusterID, nodeID string, role NodeRole, ports *PortBlock, pid int) {
	recordID := uuid.New().String()
	now := time.Now()

	insertQuery := `
		INSERT INTO namespace_cluster_nodes (
			id, namespace_cluster_id, node_id, role,
			rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port,
			status, process_pid, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, _ = cm.db.Exec(ctx, insertQuery,
		recordID,
		clusterID,
		nodeID,
		string(role),
		ports.RQLiteHTTPPort,
		ports.RQLiteRaftPort,
		ports.OlricHTTPPort,
		ports.OlricMemberlistPort,
		ports.GatewayHTTPPort,
		string(NodeStatusRunning),
		pid,
		now,
		now,
	)
}

// updateClusterNodeOlricStatus updates a node record to indicate Olric is running
func (cm *ClusterManager) updateClusterNodeOlricStatus(ctx context.Context, clusterID, nodeID string) {
	// Check if Olric role record exists
	var existing []ClusterNode
	checkQuery := `SELECT id FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ? AND role = ?`
	_ = cm.db.Query(ctx, &existing, checkQuery, clusterID, nodeID, string(NodeRoleOlric))

	if len(existing) == 0 {
		// Create new record for Olric role
		recordID := uuid.New().String()
		now := time.Now()
		insertQuery := `
			INSERT INTO namespace_cluster_nodes (
				id, namespace_cluster_id, node_id, role, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		_, _ = cm.db.Exec(ctx, insertQuery, recordID, clusterID, nodeID, string(NodeRoleOlric), string(NodeStatusRunning), now, now)
	}
}

// updateClusterNodeGatewayStatus updates a node record to indicate Gateway is running
func (cm *ClusterManager) updateClusterNodeGatewayStatus(ctx context.Context, clusterID, nodeID string) {
	// Check if Gateway role record exists
	var existing []ClusterNode
	checkQuery := `SELECT id FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ? AND role = ?`
	_ = cm.db.Query(ctx, &existing, checkQuery, clusterID, nodeID, string(NodeRoleGateway))

	if len(existing) == 0 {
		// Create new record for Gateway role
		recordID := uuid.New().String()
		now := time.Now()
		insertQuery := `
			INSERT INTO namespace_cluster_nodes (
				id, namespace_cluster_id, node_id, role, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		_, _ = cm.db.Exec(ctx, insertQuery, recordID, clusterID, nodeID, string(NodeRoleGateway), string(NodeStatusRunning), now, now)
	}
}
