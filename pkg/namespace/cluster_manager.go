package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ClusterManagerConfig contains configuration for the cluster manager
type ClusterManagerConfig struct {
	BaseDomain  string // Base domain for namespace gateways (e.g., "devnet-orama.network")
	BaseDataDir string // Base directory for namespace data (e.g., "~/.orama/data/namespaces")
}

// ClusterManager orchestrates namespace cluster provisioning and lifecycle
type ClusterManager struct {
	db             rqlite.Client
	portAllocator  *NamespacePortAllocator
	nodeSelector   *ClusterNodeSelector
	rqliteSpawner  *rqlite.InstanceSpawner
	olricSpawner   *olric.InstanceSpawner
	gatewaySpawner *gateway.InstanceSpawner
	logger         *zap.Logger
	baseDomain     string
	baseDataDir    string

	// Local node identity for distributed spawning
	localNodeID string

	// Track provisioning operations
	provisioningMu sync.RWMutex
	provisioning   map[string]bool // namespace -> in progress
}

// NewClusterManager creates a new cluster manager
func NewClusterManager(
	db rqlite.Client,
	cfg ClusterManagerConfig,
	logger *zap.Logger,
) *ClusterManager {
	// Create internal components
	portAllocator := NewNamespacePortAllocator(db, logger)
	nodeSelector := NewClusterNodeSelector(db, portAllocator, logger)
	rqliteSpawner := rqlite.NewInstanceSpawner(cfg.BaseDataDir, logger)
	olricSpawner := olric.NewInstanceSpawner(cfg.BaseDataDir, logger)
	gatewaySpawner := gateway.NewInstanceSpawner(cfg.BaseDataDir, logger)

	return &ClusterManager{
		db:             db,
		portAllocator:  portAllocator,
		nodeSelector:   nodeSelector,
		rqliteSpawner:  rqliteSpawner,
		olricSpawner:   olricSpawner,
		gatewaySpawner: gatewaySpawner,
		baseDomain:     cfg.BaseDomain,
		baseDataDir:    cfg.BaseDataDir,
		logger:         logger.With(zap.String("component", "cluster-manager")),
		provisioning:   make(map[string]bool),
	}
}

// NewClusterManagerWithComponents creates a cluster manager with custom components (useful for testing)
func NewClusterManagerWithComponents(
	db rqlite.Client,
	portAllocator *NamespacePortAllocator,
	nodeSelector *ClusterNodeSelector,
	rqliteSpawner *rqlite.InstanceSpawner,
	olricSpawner *olric.InstanceSpawner,
	gatewaySpawner *gateway.InstanceSpawner,
	cfg ClusterManagerConfig,
	logger *zap.Logger,
) *ClusterManager {
	return &ClusterManager{
		db:             db,
		portAllocator:  portAllocator,
		nodeSelector:   nodeSelector,
		rqliteSpawner:  rqliteSpawner,
		olricSpawner:   olricSpawner,
		gatewaySpawner: gatewaySpawner,
		baseDomain:     cfg.BaseDomain,
		baseDataDir:    cfg.BaseDataDir,
		logger:         logger.With(zap.String("component", "cluster-manager")),
		provisioning:   make(map[string]bool),
	}
}

// SetLocalNodeID sets this node's peer ID for local/remote dispatch during provisioning
func (cm *ClusterManager) SetLocalNodeID(id string) {
	cm.localNodeID = id
	cm.logger.Info("Local node ID set for distributed provisioning", zap.String("local_node_id", id))
}

// ProvisionCluster provisions a new 3-node cluster for a namespace
// This is an async operation - returns immediately with cluster ID for polling
func (cm *ClusterManager) ProvisionCluster(ctx context.Context, namespaceID int, namespaceName, provisionedBy string) (*NamespaceCluster, error) {
	// Check if already provisioning
	cm.provisioningMu.Lock()
	if cm.provisioning[namespaceName] {
		cm.provisioningMu.Unlock()
		return nil, fmt.Errorf("namespace %s is already being provisioned", namespaceName)
	}
	cm.provisioning[namespaceName] = true
	cm.provisioningMu.Unlock()

	defer func() {
		cm.provisioningMu.Lock()
		delete(cm.provisioning, namespaceName)
		cm.provisioningMu.Unlock()
	}()

	cm.logger.Info("Starting cluster provisioning",
		zap.String("namespace", namespaceName),
		zap.Int("namespace_id", namespaceID),
		zap.String("provisioned_by", provisionedBy),
	)

	// Create cluster record
	cluster := &NamespaceCluster{
		ID:               uuid.New().String(),
		NamespaceID:      namespaceID,
		NamespaceName:    namespaceName,
		Status:           ClusterStatusProvisioning,
		RQLiteNodeCount:  3,
		OlricNodeCount:   3,
		GatewayNodeCount: 3,
		ProvisionedBy:    provisionedBy,
		ProvisionedAt:    time.Now(),
	}

	// Insert cluster record
	if err := cm.insertCluster(ctx, cluster); err != nil {
		return nil, fmt.Errorf("failed to insert cluster record: %w", err)
	}

	// Log event
	cm.logEvent(ctx, cluster.ID, EventProvisioningStarted, "", "Cluster provisioning started", nil)

	// Select 3 nodes for the cluster
	nodes, err := cm.nodeSelector.SelectNodesForCluster(ctx, 3)
	if err != nil {
		cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusFailed, err.Error())
		return nil, fmt.Errorf("failed to select nodes: %w", err)
	}

	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.NodeID
	}
	cm.logEvent(ctx, cluster.ID, EventNodesSelected, "", "Selected nodes for cluster", map[string]interface{}{"nodes": nodeIDs})

	// Allocate ports on each node
	portBlocks := make([]*PortBlock, len(nodes))
	for i, node := range nodes {
		block, err := cm.portAllocator.AllocatePortBlock(ctx, node.NodeID, cluster.ID)
		if err != nil {
			// Rollback previous allocations
			for j := 0; j < i; j++ {
				cm.portAllocator.DeallocatePortBlock(ctx, cluster.ID, nodes[j].NodeID)
			}
			cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusFailed, err.Error())
			return nil, fmt.Errorf("failed to allocate ports on node %s: %w", node.NodeID, err)
		}
		portBlocks[i] = block
		cm.logEvent(ctx, cluster.ID, EventPortsAllocated, node.NodeID, 
			fmt.Sprintf("Allocated ports %d-%d", block.PortStart, block.PortEnd), nil)
	}

	// Start RQLite instances (leader first, then followers)
	rqliteInstances, err := cm.startRQLiteCluster(ctx, cluster, nodes, portBlocks)
	if err != nil {
		cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, nil, nil)
		return nil, fmt.Errorf("failed to start RQLite cluster: %w", err)
	}

	// Start Olric instances
	olricInstances, err := cm.startOlricCluster(ctx, cluster, nodes, portBlocks)
	if err != nil {
		cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, rqliteInstances, nil)
		return nil, fmt.Errorf("failed to start Olric cluster: %w", err)
	}

	// Start Gateway instances (optional - may not be available in dev mode)
	_, err = cm.startGatewayCluster(ctx, cluster, nodes, portBlocks, rqliteInstances, olricInstances)
	if err != nil {
		// Check if this is a "binary not found" error - if so, continue without gateways
		if strings.Contains(err.Error(), "gateway binary not found") {
			cm.logger.Warn("Skipping namespace gateway spawning (binary not available)",
				zap.String("namespace", cluster.NamespaceName),
				zap.Error(err),
			)
			cm.logEvent(ctx, cluster.ID, "gateway_skipped", "", "Gateway binary not available, cluster will use main gateway", nil)
		} else {
			cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, rqliteInstances, olricInstances)
			return nil, fmt.Errorf("failed to start Gateway cluster: %w", err)
		}
	}

	// Create DNS records for namespace gateway
	if err := cm.createDNSRecords(ctx, cluster, nodes, portBlocks); err != nil {
		cm.logger.Warn("Failed to create DNS records", zap.Error(err))
		// Don't fail provisioning for DNS errors
	}

	// Update cluster status to ready
	now := time.Now()
	cluster.Status = ClusterStatusReady
	cluster.ReadyAt = &now
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusReady, "")
	cm.logEvent(ctx, cluster.ID, EventClusterReady, "", "Cluster is ready", nil)

	cm.logger.Info("Cluster provisioning completed",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", namespaceName),
	)

	return cluster, nil
}

// startRQLiteCluster starts RQLite instances on all nodes (locally or remotely)
func (cm *ClusterManager) startRQLiteCluster(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) ([]*rqlite.Instance, error) {
	instances := make([]*rqlite.Instance, len(nodes))

	// Start leader first (node 0)
	leaderCfg := rqlite.InstanceConfig{
		Namespace:      cluster.NamespaceName,
		NodeID:         nodes[0].NodeID,
		HTTPPort:       portBlocks[0].RQLiteHTTPPort,
		RaftPort:       portBlocks[0].RQLiteRaftPort,
		HTTPAdvAddress: fmt.Sprintf("%s:%d", nodes[0].InternalIP, portBlocks[0].RQLiteHTTPPort),
		RaftAdvAddress: fmt.Sprintf("%s:%d", nodes[0].InternalIP, portBlocks[0].RQLiteRaftPort),
		IsLeader:       true,
	}

	var leaderInstance *rqlite.Instance
	var err error
	if nodes[0].NodeID == cm.localNodeID {
		cm.logger.Info("Spawning RQLite leader locally", zap.String("node", nodes[0].NodeID))
		leaderInstance, err = cm.rqliteSpawner.SpawnInstance(ctx, leaderCfg)
	} else {
		cm.logger.Info("Spawning RQLite leader remotely", zap.String("node", nodes[0].NodeID), zap.String("ip", nodes[0].InternalIP))
		leaderInstance, err = cm.spawnRQLiteRemote(ctx, nodes[0].InternalIP, leaderCfg)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start RQLite leader: %w", err)
	}
	instances[0] = leaderInstance

	cm.logEvent(ctx, cluster.ID, EventRQLiteStarted, nodes[0].NodeID, "RQLite leader started", nil)
	cm.logEvent(ctx, cluster.ID, EventRQLiteLeaderElected, nodes[0].NodeID, "RQLite leader elected", nil)

	if err := cm.insertClusterNode(ctx, cluster.ID, nodes[0].NodeID, NodeRoleRQLiteLeader, portBlocks[0]); err != nil {
		cm.logger.Warn("Failed to record cluster node", zap.Error(err))
	}

	// Start followers
	leaderRaftAddr := leaderCfg.RaftAdvAddress
	for i := 1; i < len(nodes); i++ {
		followerCfg := rqlite.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         nodes[i].NodeID,
			HTTPPort:       portBlocks[i].RQLiteHTTPPort,
			RaftPort:       portBlocks[i].RQLiteRaftPort,
			HTTPAdvAddress: fmt.Sprintf("%s:%d", nodes[i].InternalIP, portBlocks[i].RQLiteHTTPPort),
			RaftAdvAddress: fmt.Sprintf("%s:%d", nodes[i].InternalIP, portBlocks[i].RQLiteRaftPort),
			JoinAddresses:  []string{leaderRaftAddr},
			IsLeader:       false,
		}

		var followerInstance *rqlite.Instance
		if nodes[i].NodeID == cm.localNodeID {
			cm.logger.Info("Spawning RQLite follower locally", zap.String("node", nodes[i].NodeID))
			followerInstance, err = cm.rqliteSpawner.SpawnInstance(ctx, followerCfg)
		} else {
			cm.logger.Info("Spawning RQLite follower remotely", zap.String("node", nodes[i].NodeID), zap.String("ip", nodes[i].InternalIP))
			followerInstance, err = cm.spawnRQLiteRemote(ctx, nodes[i].InternalIP, followerCfg)
		}
		if err != nil {
			// Stop previously started instances
			for j := 0; j < i; j++ {
				cm.stopRQLiteOnNode(ctx, nodes[j].NodeID, nodes[j].InternalIP, cluster.NamespaceName, instances[j])
			}
			return nil, fmt.Errorf("failed to start RQLite follower on node %s: %w", nodes[i].NodeID, err)
		}
		instances[i] = followerInstance

		cm.logEvent(ctx, cluster.ID, EventRQLiteStarted, nodes[i].NodeID, "RQLite follower started", nil)
		cm.logEvent(ctx, cluster.ID, EventRQLiteJoined, nodes[i].NodeID, "RQLite follower joined cluster", nil)

		if err := cm.insertClusterNode(ctx, cluster.ID, nodes[i].NodeID, NodeRoleRQLiteFollower, portBlocks[i]); err != nil {
			cm.logger.Warn("Failed to record cluster node", zap.Error(err))
		}
	}

	return instances, nil
}

// startOlricCluster starts Olric instances on all nodes (locally or remotely)
func (cm *ClusterManager) startOlricCluster(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) ([]*olric.OlricInstance, error) {
	instances := make([]*olric.OlricInstance, len(nodes))

	// Build peer addresses (all nodes)
	peerAddresses := make([]string, len(nodes))
	for i, node := range nodes {
		peerAddresses[i] = fmt.Sprintf("%s:%d", node.InternalIP, portBlocks[i].OlricMemberlistPort)
	}

	// Start all Olric instances
	for i, node := range nodes {
		cfg := olric.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         node.NodeID,
			HTTPPort:       portBlocks[i].OlricHTTPPort,
			MemberlistPort: portBlocks[i].OlricMemberlistPort,
			BindAddr:       node.InternalIP,   // Bind to node's WG IP (0.0.0.0 resolves to IPv6 on some hosts)
			AdvertiseAddr:  node.InternalIP,   // Advertise WG IP to peers
			PeerAddresses:  peerAddresses,
		}

		var instance *olric.OlricInstance
		var err error
		if node.NodeID == cm.localNodeID {
			cm.logger.Info("Spawning Olric locally", zap.String("node", node.NodeID))
			instance, err = cm.olricSpawner.SpawnInstance(ctx, cfg)
		} else {
			cm.logger.Info("Spawning Olric remotely", zap.String("node", node.NodeID), zap.String("ip", node.InternalIP))
			instance, err = cm.spawnOlricRemote(ctx, node.InternalIP, cfg)
		}
		if err != nil {
			// Stop previously started instances
			for j := 0; j < i; j++ {
				cm.stopOlricOnNode(ctx, nodes[j].NodeID, nodes[j].InternalIP, cluster.NamespaceName)
			}
			return nil, fmt.Errorf("failed to start Olric on node %s: %w", node.NodeID, err)
		}
		instances[i] = instance

		cm.logEvent(ctx, cluster.ID, EventOlricStarted, node.NodeID, "Olric instance started", nil)
		cm.logEvent(ctx, cluster.ID, EventOlricJoined, node.NodeID, "Olric instance joined memberlist", nil)

		if err := cm.insertClusterNode(ctx, cluster.ID, node.NodeID, NodeRoleOlric, portBlocks[i]); err != nil {
			cm.logger.Warn("Failed to record cluster node", zap.Error(err))
		}
	}

	return instances, nil
}

// startGatewayCluster starts Gateway instances on all nodes
func (cm *ClusterManager) startGatewayCluster(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock, rqliteInstances []*rqlite.Instance, olricInstances []*olric.OlricInstance) ([]*gateway.GatewayInstance, error) {
	instances := make([]*gateway.GatewayInstance, len(nodes))

	// Build Olric server addresses
	olricServers := make([]string, len(olricInstances))
	for i, inst := range olricInstances {
		olricServers[i] = inst.DSN()
	}

	// Start all Gateway instances
	for i, node := range nodes {
		// Connect to local RQLite instance
		rqliteDSN := fmt.Sprintf("http://localhost:%d", portBlocks[i].RQLiteHTTPPort)

		cfg := gateway.InstanceConfig{
			Namespace:    cluster.NamespaceName,
			NodeID:       node.NodeID,
			HTTPPort:     portBlocks[i].GatewayHTTPPort,
			BaseDomain:   cm.baseDomain,
			RQLiteDSN:    rqliteDSN,
			OlricServers: olricServers,
		}

		instance, err := cm.gatewaySpawner.SpawnInstance(ctx, cfg)
		if err != nil {
			// Stop previously started instances
			for j := 0; j < i; j++ {
				cm.gatewaySpawner.StopInstance(ctx, cluster.NamespaceName, nodes[j].NodeID)
			}
			return nil, fmt.Errorf("failed to start Gateway on node %s: %w", node.NodeID, err)
		}
		instances[i] = instance

		cm.logEvent(ctx, cluster.ID, EventGatewayStarted, node.NodeID, "Gateway instance started", nil)

		if err := cm.insertClusterNode(ctx, cluster.ID, node.NodeID, NodeRoleGateway, portBlocks[i]); err != nil {
			cm.logger.Warn("Failed to record cluster node", zap.Error(err))
		}
	}

	return instances, nil
}

// spawnRQLiteRemote sends a spawn-rqlite request to a remote node
func (cm *ClusterManager) spawnRQLiteRemote(ctx context.Context, nodeIP string, cfg rqlite.InstanceConfig) (*rqlite.Instance, error) {
	resp, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":              "spawn-rqlite",
		"namespace":           cfg.Namespace,
		"node_id":             cfg.NodeID,
		"rqlite_http_port":    cfg.HTTPPort,
		"rqlite_raft_port":    cfg.RaftPort,
		"rqlite_http_adv_addr": cfg.HTTPAdvAddress,
		"rqlite_raft_adv_addr": cfg.RaftAdvAddress,
		"rqlite_join_addrs":   cfg.JoinAddresses,
		"rqlite_is_leader":    cfg.IsLeader,
	})
	if err != nil {
		return nil, err
	}
	return &rqlite.Instance{PID: resp.PID}, nil
}

// spawnOlricRemote sends a spawn-olric request to a remote node
func (cm *ClusterManager) spawnOlricRemote(ctx context.Context, nodeIP string, cfg olric.InstanceConfig) (*olric.OlricInstance, error) {
	resp, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":               "spawn-olric",
		"namespace":            cfg.Namespace,
		"node_id":              cfg.NodeID,
		"olric_http_port":      cfg.HTTPPort,
		"olric_memberlist_port": cfg.MemberlistPort,
		"olric_bind_addr":      cfg.BindAddr,
		"olric_advertise_addr": cfg.AdvertiseAddr,
		"olric_peer_addresses": cfg.PeerAddresses,
	})
	if err != nil {
		return nil, err
	}
	return &olric.OlricInstance{PID: resp.PID}, nil
}

// spawnResponse represents the JSON response from a spawn request
type spawnResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// sendSpawnRequest sends a spawn/stop request to a remote node's spawn endpoint
func (cm *ClusterManager) sendSpawnRequest(ctx context.Context, nodeIP string, req map[string]interface{}) (*spawnResponse, error) {
	url := fmt.Sprintf("http://%s:6001/v1/internal/namespace/spawn", nodeIP)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spawn request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Orama-Internal-Auth", "namespace-coordination")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send spawn request to %s: %w", nodeIP, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response from %s: %w", nodeIP, err)
	}

	var spawnResp spawnResponse
	if err := json.Unmarshal(respBody, &spawnResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from %s: %w", nodeIP, err)
	}

	if !spawnResp.Success {
		return nil, fmt.Errorf("spawn request failed on %s: %s", nodeIP, spawnResp.Error)
	}

	return &spawnResp, nil
}

// stopRQLiteOnNode stops a RQLite instance on a node (local or remote)
func (cm *ClusterManager) stopRQLiteOnNode(ctx context.Context, nodeID, nodeIP, namespace string, inst *rqlite.Instance) {
	if nodeID == cm.localNodeID {
		if inst != nil {
			cm.rqliteSpawner.StopInstance(ctx, inst)
		}
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-rqlite", namespace, nodeID)
	}
}

// stopOlricOnNode stops an Olric instance on a node (local or remote)
func (cm *ClusterManager) stopOlricOnNode(ctx context.Context, nodeID, nodeIP, namespace string) {
	if nodeID == cm.localNodeID {
		cm.olricSpawner.StopInstance(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-olric", namespace, nodeID)
	}
}

// sendStopRequest sends a stop request to a remote node
func (cm *ClusterManager) sendStopRequest(ctx context.Context, nodeIP, action, namespace, nodeID string) {
	_, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":    action,
		"namespace": namespace,
		"node_id":   nodeID,
	})
	if err != nil {
		cm.logger.Warn("Failed to send stop request to remote node",
			zap.String("node_ip", nodeIP),
			zap.String("action", action),
			zap.Error(err),
		)
	}
}

// createDNSRecords creates DNS records for the namespace gateway
func (cm *ClusterManager) createDNSRecords(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) error {
	// Create A records for ns-{namespace}.{baseDomain} pointing to all 3 nodes
	fqdn := fmt.Sprintf("ns-%s.%s", cluster.NamespaceName, cm.baseDomain)

	for i, node := range nodes {
		query := `
			INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by)
			VALUES (?, 'A', ?, 300, ?, 'system')
		`
		_, err := cm.db.Exec(ctx, query, fqdn, node.IPAddress, cluster.NamespaceName)
		if err != nil {
			cm.logger.Warn("Failed to create DNS record",
				zap.String("fqdn", fqdn),
				zap.String("ip", node.IPAddress),
				zap.Error(err),
			)
		} else {
			cm.logger.Info("Created DNS A record",
				zap.String("fqdn", fqdn),
				zap.String("ip", node.IPAddress),
				zap.Int("gateway_port", portBlocks[i].GatewayHTTPPort),
			)
		}
	}

	cm.logEvent(ctx, cluster.ID, EventDNSCreated, "", fmt.Sprintf("DNS records created for %s", fqdn), nil)
	return nil
}

// rollbackProvisioning cleans up a failed provisioning attempt
func (cm *ClusterManager) rollbackProvisioning(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock, rqliteInstances []*rqlite.Instance, olricInstances []*olric.OlricInstance) {
	cm.logger.Info("Rolling back failed provisioning", zap.String("cluster_id", cluster.ID))

	// Stop Gateway instances (local only for now)
	cm.gatewaySpawner.StopAllInstances(ctx, cluster.NamespaceName)

	// Stop Olric instances on each node
	if olricInstances != nil && nodes != nil {
		for _, node := range nodes {
			cm.stopOlricOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName)
		}
	}

	// Stop RQLite instances on each node
	if rqliteInstances != nil && nodes != nil {
		for i, inst := range rqliteInstances {
			if inst != nil && i < len(nodes) {
				cm.stopRQLiteOnNode(ctx, nodes[i].NodeID, nodes[i].InternalIP, cluster.NamespaceName, inst)
			}
		}
	}

	// Deallocate ports
	cm.portAllocator.DeallocateAllPortBlocks(ctx, cluster.ID)

	// Update cluster status
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusFailed, "Provisioning failed and rolled back")
}

// DeprovisionCluster tears down a namespace cluster
func (cm *ClusterManager) DeprovisionCluster(ctx context.Context, namespaceID int64) error {
	cluster, err := cm.GetClusterByNamespaceID(ctx, namespaceID)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster == nil {
		return nil // No cluster to deprovision
	}

	cm.logger.Info("Starting cluster deprovisioning",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", cluster.NamespaceName),
	)

	cm.logEvent(ctx, cluster.ID, EventDeprovisionStarted, "", "Cluster deprovisioning started", nil)
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusDeprovisioning, "")

	// Stop all services
	cm.gatewaySpawner.StopAllInstances(ctx, cluster.NamespaceName)
	cm.olricSpawner.StopAllInstances(ctx, cluster.NamespaceName)
	// Note: RQLite instances need to be stopped individually based on stored PIDs

	// Deallocate all ports
	cm.portAllocator.DeallocateAllPortBlocks(ctx, cluster.ID)

	// Delete DNS records
	query := `DELETE FROM dns_records WHERE namespace = ?`
	cm.db.Exec(ctx, query, cluster.NamespaceName)

	// Delete cluster record
	query = `DELETE FROM namespace_clusters WHERE id = ?`
	cm.db.Exec(ctx, query, cluster.ID)

	cm.logEvent(ctx, cluster.ID, EventDeprovisioned, "", "Cluster deprovisioned", nil)

	cm.logger.Info("Cluster deprovisioning completed", zap.String("cluster_id", cluster.ID))

	return nil
}

// GetClusterStatus returns the current status of a namespace cluster
func (cm *ClusterManager) GetClusterStatus(ctx context.Context, clusterID string) (*ClusterProvisioningStatus, error) {
	cluster, err := cm.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if cluster == nil {
		return nil, fmt.Errorf("cluster not found")
	}

	status := &ClusterProvisioningStatus{
		Status:    cluster.Status,
		ClusterID: cluster.ID,
	}

	// Check individual service status
	// TODO: Actually check each service's health
	if cluster.Status == ClusterStatusReady {
		status.RQLiteReady = true
		status.OlricReady = true
		status.GatewayReady = true
		status.DNSReady = true
	}

	// Get node list
	nodes, err := cm.getClusterNodes(ctx, clusterID)
	if err == nil {
		for _, node := range nodes {
			status.Nodes = append(status.Nodes, node.NodeID)
		}
	}

	if cluster.ErrorMessage != "" {
		status.Error = cluster.ErrorMessage
	}

	return status, nil
}

// GetCluster retrieves a cluster by ID
func (cm *ClusterManager) GetCluster(ctx context.Context, clusterID string) (*NamespaceCluster, error) {
	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE id = ?`
	if err := cm.db.Query(ctx, &clusters, query, clusterID); err != nil {
		return nil, err
	}
	if len(clusters) == 0 {
		return nil, nil
	}
	return &clusters[0], nil
}

// GetClusterByNamespaceID retrieves a cluster by namespace ID
func (cm *ClusterManager) GetClusterByNamespaceID(ctx context.Context, namespaceID int64) (*NamespaceCluster, error) {
	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE namespace_id = ?`
	if err := cm.db.Query(ctx, &clusters, query, namespaceID); err != nil {
		return nil, err
	}
	if len(clusters) == 0 {
		return nil, nil
	}
	return &clusters[0], nil
}

// GetClusterByNamespace retrieves a cluster by namespace name
func (cm *ClusterManager) GetClusterByNamespace(ctx context.Context, namespaceName string) (*NamespaceCluster, error) {
	var clusters []NamespaceCluster
	query := `SELECT * FROM namespace_clusters WHERE namespace_name = ?`
	if err := cm.db.Query(ctx, &clusters, query, namespaceName); err != nil {
		return nil, err
	}
	if len(clusters) == 0 {
		return nil, nil
	}
	return &clusters[0], nil
}

// Database helper methods

func (cm *ClusterManager) insertCluster(ctx context.Context, cluster *NamespaceCluster) error {
	query := `
		INSERT INTO namespace_clusters (
			id, namespace_id, namespace_name, status,
			rqlite_node_count, olric_node_count, gateway_node_count,
			provisioned_by, provisioned_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := cm.db.Exec(ctx, query,
		cluster.ID, cluster.NamespaceID, cluster.NamespaceName, cluster.Status,
		cluster.RQLiteNodeCount, cluster.OlricNodeCount, cluster.GatewayNodeCount,
		cluster.ProvisionedBy, cluster.ProvisionedAt,
	)
	return err
}

func (cm *ClusterManager) updateClusterStatus(ctx context.Context, clusterID string, status ClusterStatus, errorMsg string) error {
	var query string
	var args []interface{}

	if status == ClusterStatusReady {
		query = `UPDATE namespace_clusters SET status = ?, ready_at = ?, error_message = '' WHERE id = ?`
		args = []interface{}{status, time.Now(), clusterID}
	} else {
		query = `UPDATE namespace_clusters SET status = ?, error_message = ? WHERE id = ?`
		args = []interface{}{status, errorMsg, clusterID}
	}

	_, err := cm.db.Exec(ctx, query, args...)
	return err
}

func (cm *ClusterManager) insertClusterNode(ctx context.Context, clusterID, nodeID string, role NodeRole, portBlock *PortBlock) error {
	query := `
		INSERT INTO namespace_cluster_nodes (
			id, namespace_cluster_id, node_id, role, status,
			rqlite_http_port, rqlite_raft_port,
			olric_http_port, olric_memberlist_port,
			gateway_http_port, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'running', ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	_, err := cm.db.Exec(ctx, query,
		uuid.New().String(), clusterID, nodeID, role,
		portBlock.RQLiteHTTPPort, portBlock.RQLiteRaftPort,
		portBlock.OlricHTTPPort, portBlock.OlricMemberlistPort,
		portBlock.GatewayHTTPPort, now, now,
	)
	return err
}

func (cm *ClusterManager) getClusterNodes(ctx context.Context, clusterID string) ([]ClusterNode, error) {
	var nodes []ClusterNode
	query := `SELECT * FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`
	if err := cm.db.Query(ctx, &nodes, query, clusterID); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (cm *ClusterManager) logEvent(ctx context.Context, clusterID string, eventType EventType, nodeID, message string, metadata map[string]interface{}) {
	metadataJSON := ""
	if metadata != nil {
		if data, err := json.Marshal(metadata); err == nil {
			metadataJSON = string(data)
		}
	}

	query := `
		INSERT INTO namespace_cluster_events (id, namespace_cluster_id, event_type, node_id, message, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := cm.db.Exec(ctx, query, uuid.New().String(), clusterID, eventType, nodeID, message, metadataJSON, time.Now())
	if err != nil {
		cm.logger.Warn("Failed to log cluster event", zap.Error(err))
	}
}

// ClusterProvisioner interface implementation

// CheckNamespaceCluster checks if a namespace has a cluster and returns its status.
// Returns: (clusterID, status, needsProvisioning, error)
// - If the namespace is "default", returns ("", "default", false, nil) as it uses the global cluster
// - If a cluster exists and is ready/provisioning, returns (clusterID, status, false, nil)
// - If no cluster exists or cluster failed, returns ("", "", true, nil) to indicate provisioning is needed
func (cm *ClusterManager) CheckNamespaceCluster(ctx context.Context, namespaceName string) (string, string, bool, error) {
	// Default namespace uses the global cluster, no per-namespace cluster needed
	if namespaceName == "default" || namespaceName == "" {
		return "", "default", false, nil
	}

	cluster, err := cm.GetClusterByNamespace(ctx, namespaceName)
	if err != nil {
		return "", "", false, err
	}

	if cluster == nil {
		// No cluster exists, provisioning is needed
		return "", "", true, nil
	}

	// If the cluster failed, delete the old record and trigger re-provisioning
	if cluster.Status == ClusterStatusFailed {
		cm.logger.Info("Found failed cluster, will re-provision",
			zap.String("namespace", namespaceName),
			zap.String("cluster_id", cluster.ID),
		)
		// Delete the failed cluster record
		query := `DELETE FROM namespace_clusters WHERE id = ?`
		cm.db.Exec(ctx, query, cluster.ID)
		// Also clean up any port allocations
		cm.portAllocator.DeallocateAllPortBlocks(ctx, cluster.ID)
		return "", "", true, nil
	}

	// Return current status
	return cluster.ID, string(cluster.Status), false, nil
}

// ProvisionNamespaceCluster triggers provisioning for a new namespace cluster.
// Returns: (clusterID, pollURL, error)
// This starts an async provisioning process and returns immediately with the cluster ID
// and a URL to poll for status updates.
func (cm *ClusterManager) ProvisionNamespaceCluster(ctx context.Context, namespaceID int, namespaceName, wallet string) (string, string, error) {
	// Check if already provisioning
	cm.provisioningMu.Lock()
	if cm.provisioning[namespaceName] {
		cm.provisioningMu.Unlock()
		// Return existing cluster ID if found
		cluster, _ := cm.GetClusterByNamespace(ctx, namespaceName)
		if cluster != nil {
			return cluster.ID, "/v1/namespace/status?id=" + cluster.ID, nil
		}
		return "", "", fmt.Errorf("namespace %s is already being provisioned", namespaceName)
	}
	cm.provisioning[namespaceName] = true
	cm.provisioningMu.Unlock()

	// Create cluster record synchronously to get the ID
	cluster := &NamespaceCluster{
		ID:               uuid.New().String(),
		NamespaceID:      namespaceID,
		NamespaceName:    namespaceName,
		Status:           ClusterStatusProvisioning,
		RQLiteNodeCount:  3,
		OlricNodeCount:   3,
		GatewayNodeCount: 3,
		ProvisionedBy:    wallet,
		ProvisionedAt:    time.Now(),
	}

	// Insert cluster record
	if err := cm.insertCluster(ctx, cluster); err != nil {
		cm.provisioningMu.Lock()
		delete(cm.provisioning, namespaceName)
		cm.provisioningMu.Unlock()
		return "", "", fmt.Errorf("failed to insert cluster record: %w", err)
	}

	cm.logEvent(ctx, cluster.ID, EventProvisioningStarted, "", "Cluster provisioning started", nil)

	// Start actual provisioning in background goroutine
	go cm.provisionClusterAsync(cluster, namespaceID, namespaceName, wallet)

	pollURL := "/v1/namespace/status?id=" + cluster.ID
	return cluster.ID, pollURL, nil
}

// provisionClusterAsync performs the actual cluster provisioning in the background
func (cm *ClusterManager) provisionClusterAsync(cluster *NamespaceCluster, namespaceID int, namespaceName, provisionedBy string) {
	defer func() {
		cm.provisioningMu.Lock()
		delete(cm.provisioning, namespaceName)
		cm.provisioningMu.Unlock()
	}()

	ctx := context.Background()

	cm.logger.Info("Starting async cluster provisioning",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", namespaceName),
		zap.Int("namespace_id", namespaceID),
		zap.String("provisioned_by", provisionedBy),
	)

	// Select 3 nodes for the cluster
	nodes, err := cm.nodeSelector.SelectNodesForCluster(ctx, 3)
	if err != nil {
		cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusFailed, err.Error())
		cm.logger.Error("Failed to select nodes for cluster", zap.Error(err))
		return
	}

	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.NodeID
	}
	cm.logEvent(ctx, cluster.ID, EventNodesSelected, "", "Selected nodes for cluster", map[string]interface{}{"nodes": nodeIDs})

	// Allocate ports on each node
	portBlocks := make([]*PortBlock, len(nodes))
	for i, node := range nodes {
		block, err := cm.portAllocator.AllocatePortBlock(ctx, node.NodeID, cluster.ID)
		if err != nil {
			// Rollback previous allocations
			for j := 0; j < i; j++ {
				cm.portAllocator.DeallocatePortBlock(ctx, cluster.ID, nodes[j].NodeID)
			}
			cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusFailed, err.Error())
			cm.logger.Error("Failed to allocate ports", zap.Error(err))
			return
		}
		portBlocks[i] = block
		cm.logEvent(ctx, cluster.ID, EventPortsAllocated, node.NodeID,
			fmt.Sprintf("Allocated ports %d-%d", block.PortStart, block.PortEnd), nil)
	}

	// Start RQLite instances (leader first, then followers)
	rqliteInstances, err := cm.startRQLiteCluster(ctx, cluster, nodes, portBlocks)
	if err != nil {
		cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, nil, nil)
		cm.logger.Error("Failed to start RQLite cluster", zap.Error(err))
		return
	}

	// Start Olric instances
	olricInstances, err := cm.startOlricCluster(ctx, cluster, nodes, portBlocks)
	if err != nil {
		cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, rqliteInstances, nil)
		cm.logger.Error("Failed to start Olric cluster", zap.Error(err))
		return
	}

	// Start Gateway instances (optional - may not be available in dev mode)
	_, err = cm.startGatewayCluster(ctx, cluster, nodes, portBlocks, rqliteInstances, olricInstances)
	if err != nil {
		// Check if this is a "binary not found" error - if so, continue without gateways
		if strings.Contains(err.Error(), "gateway binary not found") {
			cm.logger.Warn("Skipping namespace gateway spawning (binary not available)",
				zap.String("namespace", cluster.NamespaceName),
				zap.Error(err),
			)
			cm.logEvent(ctx, cluster.ID, "gateway_skipped", "", "Gateway binary not available, cluster will use main gateway", nil)
		} else {
			cm.rollbackProvisioning(ctx, cluster, nodes, portBlocks, rqliteInstances, olricInstances)
			cm.logger.Error("Failed to start Gateway cluster", zap.Error(err))
			return
		}
	}

	// Create DNS records for namespace gateway
	if err := cm.createDNSRecords(ctx, cluster, nodes, portBlocks); err != nil {
		cm.logger.Warn("Failed to create DNS records", zap.Error(err))
		// Don't fail provisioning for DNS errors
	}

	// Update cluster status to ready
	now := time.Now()
	cluster.Status = ClusterStatusReady
	cluster.ReadyAt = &now
	cm.updateClusterStatus(ctx, cluster.ID, ClusterStatusReady, "")
	cm.logEvent(ctx, cluster.ID, EventClusterReady, "", "Cluster is ready", nil)

	cm.logger.Info("Cluster provisioning completed",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", namespaceName),
	)
}

// GetClusterStatusByID returns the full status of a cluster by ID.
// This method is part of the ClusterProvisioner interface used by the gateway.
// It returns a generic struct that matches the interface definition in auth/handlers.go.
func (cm *ClusterManager) GetClusterStatusByID(ctx context.Context, clusterID string) (interface{}, error) {
	status, err := cm.GetClusterStatus(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	// Return as a map to avoid import cycles with the interface type
	return map[string]interface{}{
		"cluster_id":    status.ClusterID,
		"namespace":     status.Namespace,
		"status":        string(status.Status),
		"nodes":         status.Nodes,
		"rqlite_ready":  status.RQLiteReady,
		"olric_ready":   status.OlricReady,
		"gateway_ready": status.GatewayReady,
		"dns_ready":     status.DNSReady,
		"error":         status.Error,
	}, nil
}
