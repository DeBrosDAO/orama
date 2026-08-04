package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ClusterManagerConfig contains configuration for the cluster manager
type ClusterManagerConfig struct {
	BaseDomain      string // Base domain for namespace gateways (e.g., "orama-devnet.network")
	BaseDataDir     string // Base directory for namespace data (e.g., "~/.orama/data/namespaces")
	GlobalRQLiteDSN string // Global RQLite DSN for API key validation (e.g., "http://localhost:4001")
	// IPFS configuration for namespace gateways (defaults used if not set)
	IPFSClusterAPIURL     string        // IPFS Cluster API URL (default: "http://localhost:9094")
	IPFSAPIURL            string        // IPFS API URL (default: "http://localhost:4501")
	IPFSTimeout           time.Duration // Timeout for IPFS operations (default: 60s)
	IPFSReplicationFactor int           // IPFS replication factor (default: 3)

	// TurnEncryptionKey is a 32-byte AES-256 key for encrypting TURN shared secrets
	// in RQLite. Derived from cluster secret via HKDF(clusterSecret, "turn-encryption").
	// If nil, TURN secrets are stored in plaintext (backward compatibility).
	TurnEncryptionKey []byte

	// ClusterSecretPath is the host's cluster-secret file path. Forwarded
	// to spawned namespace gateways via YAML so they can derive the
	// cluster-wide JWT signing key (bug #215 fix). Empty string disables
	// cross-node JWT verification within namespace clusters.
	ClusterSecretPath string

	// SecretsEncryptionKey is the host's serverless secrets encryption key
	// (AES-256, hex-encoded), read once from secrets/secrets-encryption-key.
	// Forwarded to spawned namespace gateways so `function secrets ...`
	// works there (bugboard #837 follow-up). Empty leaves namespace-gateway
	// secrets management disabled (fail-loud).
	SecretsEncryptionKey string
}

// ClusterManager orchestrates namespace cluster provisioning and lifecycle
type ClusterManager struct {
	db                  rqlite.Client
	portAllocator       *NamespacePortAllocator
	webrtcPortAllocator *WebRTCPortAllocator

	// webrtcSpawnCooldown throttles WebRTC spawn retries per namespace so a
	// crash-looping unit cannot be restarted every tick (bugboard #161).
	webrtcSpawnMu       sync.Mutex
	webrtcSpawnCooldown map[string]time.Time
	nodeSelector        *ClusterNodeSelector
	systemdSpawner      *SystemdSpawner // NEW: Systemd-based spawner replaces old spawners
	dnsManager          *DNSRecordManager
	logger              *zap.Logger
	baseDomain          string
	baseDataDir         string
	globalRQLiteDSN     string // Global RQLite DSN for namespace gateway auth

	// IPFS configuration for namespace gateways
	ipfsClusterAPIURL     string
	ipfsAPIURL            string
	ipfsTimeout           time.Duration
	ipfsReplicationFactor int

	// Local node identity for distributed spawning
	localNodeID string

	// AES-256 key for encrypting TURN secrets in RQLite (nil = plaintext)
	turnEncryptionKey []byte

	// Host's serverless secrets encryption key, forwarded to spawned
	// namespace gateways (bugboard #837 follow-up). Empty = disabled.
	secretsEncryptionKey string

	// Track provisioning operations
	provisioningMu sync.RWMutex
	provisioning   map[string]bool // namespace -> in progress

	// Leadership-locality reconciler cooldown (bugboard #708): per-namespace
	// timestamp of the last leadership transfer, to bound churn. Lazy-init.
	leaderLocalityMu       sync.Mutex
	leaderLocalityCooldown map[string]time.Time

	// startedAt is when this ClusterManager was constructed (process start,
	// for all practical purposes). Bugboard #171: gates how soon this node
	// will act as WebRTC reconcile coordinator — see
	// webrtcReconcileStartupGrace. Left zero-valued when a ClusterManager is
	// constructed directly (as unit tests do) rather than via
	// NewClusterManager; time.Since of the zero value is enormous, so that
	// case is always treated as "well past the grace period" rather than
	// accidentally gating tests that don't care about startup timing.
	startedAt time.Time
}

// NewClusterManager creates a new cluster manager
func NewClusterManager(
	db rqlite.Client,
	cfg ClusterManagerConfig,
	logger *zap.Logger,
) *ClusterManager {
	// Create internal components
	portAllocator := NewNamespacePortAllocator(db, logger)
	webrtcPortAllocator := NewWebRTCPortAllocator(db, logger)
	nodeSelector := NewClusterNodeSelector(db, portAllocator, logger)
	systemdSpawner := NewSystemdSpawner(cfg.BaseDataDir, cfg.ClusterSecretPath, logger)
	dnsManager := NewDNSRecordManager(db, cfg.BaseDomain, logger)

	// Set IPFS defaults
	ipfsClusterAPIURL := cfg.IPFSClusterAPIURL
	if ipfsClusterAPIURL == "" {
		ipfsClusterAPIURL = "http://localhost:9094"
	}
	ipfsAPIURL := cfg.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:4501"
	}
	ipfsTimeout := cfg.IPFSTimeout
	if ipfsTimeout == 0 {
		ipfsTimeout = 60 * time.Second
	}
	ipfsReplicationFactor := cfg.IPFSReplicationFactor
	if ipfsReplicationFactor == 0 {
		ipfsReplicationFactor = 3
	}

	return &ClusterManager{
		db:                    db,
		portAllocator:         portAllocator,
		webrtcPortAllocator:   webrtcPortAllocator,
		nodeSelector:          nodeSelector,
		systemdSpawner:        systemdSpawner,
		dnsManager:            dnsManager,
		baseDomain:            cfg.BaseDomain,
		baseDataDir:           cfg.BaseDataDir,
		globalRQLiteDSN:       cfg.GlobalRQLiteDSN,
		ipfsClusterAPIURL:     ipfsClusterAPIURL,
		ipfsAPIURL:            ipfsAPIURL,
		ipfsTimeout:           ipfsTimeout,
		ipfsReplicationFactor: ipfsReplicationFactor,
		turnEncryptionKey:     cfg.TurnEncryptionKey,
		secretsEncryptionKey:  cfg.SecretsEncryptionKey,
		logger:                logger.With(zap.String("component", "cluster-manager")),
		provisioning:          make(map[string]bool),
		startedAt:             time.Now(),
	}
}

// NewClusterManagerWithComponents creates a cluster manager with custom components (useful for testing)
func NewClusterManagerWithComponents(
	db rqlite.Client,
	portAllocator *NamespacePortAllocator,
	nodeSelector *ClusterNodeSelector,
	systemdSpawner *SystemdSpawner,
	cfg ClusterManagerConfig,
	logger *zap.Logger,
) *ClusterManager {
	// Set IPFS defaults (same as NewClusterManager)
	ipfsClusterAPIURL := cfg.IPFSClusterAPIURL
	if ipfsClusterAPIURL == "" {
		ipfsClusterAPIURL = "http://localhost:9094"
	}
	ipfsAPIURL := cfg.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:4501"
	}
	ipfsTimeout := cfg.IPFSTimeout
	if ipfsTimeout == 0 {
		ipfsTimeout = 60 * time.Second
	}
	ipfsReplicationFactor := cfg.IPFSReplicationFactor
	if ipfsReplicationFactor == 0 {
		ipfsReplicationFactor = 3
	}

	return &ClusterManager{
		db:                    db,
		portAllocator:         portAllocator,
		webrtcPortAllocator:   NewWebRTCPortAllocator(db, logger),
		nodeSelector:          nodeSelector,
		systemdSpawner:        systemdSpawner,
		dnsManager:            NewDNSRecordManager(db, cfg.BaseDomain, logger),
		baseDomain:            cfg.BaseDomain,
		baseDataDir:           cfg.BaseDataDir,
		globalRQLiteDSN:       cfg.GlobalRQLiteDSN,
		ipfsClusterAPIURL:     ipfsClusterAPIURL,
		ipfsAPIURL:            ipfsAPIURL,
		ipfsTimeout:           ipfsTimeout,
		ipfsReplicationFactor: ipfsReplicationFactor,
		turnEncryptionKey:     cfg.TurnEncryptionKey,
		secretsEncryptionKey:  cfg.SecretsEncryptionKey,
		logger:                logger.With(zap.String("component", "cluster-manager")),
		provisioning:          make(map[string]bool),
		startedAt:             time.Now(),
	}
}

// SetLocalNodeID sets this node's peer ID for local/remote dispatch during provisioning
func (cm *ClusterManager) SetLocalNodeID(id string) {
	cm.localNodeID = id
	cm.logger.Info("Local node ID set for distributed provisioning", zap.String("local_node_id", id))
}

// spawnRQLiteWithSystemd generates config and spawns RQLite via systemd
func (cm *ClusterManager) spawnRQLiteWithSystemd(ctx context.Context, cfg rqlite.InstanceConfig) error {
	// RQLite uses command-line args, no config file needed
	// Just call systemd spawner which will generate env file and start service
	return cm.systemdSpawner.SpawnRQLite(ctx, cfg.Namespace, cfg.NodeID, cfg)
}

// spawnOlricWithSystemd spawns Olric via systemd (config creation now handled by spawner)
func (cm *ClusterManager) spawnOlricWithSystemd(ctx context.Context, cfg olric.InstanceConfig) error {
	// SystemdSpawner now handles config file creation
	return cm.systemdSpawner.SpawnOlric(ctx, cfg.Namespace, cfg.NodeID, cfg)
}

// writePeersJSON writes RQLite peers.json file for Raft cluster recovery
func (cm *ClusterManager) writePeersJSON(dataDir string, peers []rqlite.RaftPeer) error {
	raftDir := filepath.Join(dataDir, "raft")
	if err := os.MkdirAll(raftDir, 0755); err != nil {
		return fmt.Errorf("failed to create raft directory: %w", err)
	}

	peersFile := filepath.Join(raftDir, "peers.json")
	data, err := json.Marshal(peers)
	if err != nil {
		return fmt.Errorf("failed to marshal peers: %w", err)
	}

	return os.WriteFile(peersFile, data, 0644)
}

// spawnGatewayWithSystemd spawns Gateway via systemd (config creation now handled by spawner)
func (cm *ClusterManager) spawnGatewayWithSystemd(ctx context.Context, cfg gateway.InstanceConfig) error {
	// SystemdSpawner now handles config file creation
	return cm.systemdSpawner.SpawnGateway(ctx, cfg.Namespace, cfg.NodeID, cfg)
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

	// Save cluster-state.json on all nodes (local + remote) for disk-based restore on restart
	cm.saveClusterStateToAllNodes(ctx, cluster, nodes, portBlocks)

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

	var err error
	if nodes[0].NodeID == cm.localNodeID {
		cm.logger.Info("Spawning RQLite leader locally", zap.String("node", nodes[0].NodeID))
		err = cm.spawnRQLiteWithSystemd(ctx, leaderCfg)
		if err == nil {
			// Create Instance object for consistency with existing code
			instances[0] = &rqlite.Instance{
				Config: leaderCfg,
			}
		}
	} else {
		cm.logger.Info("Spawning RQLite leader remotely", zap.String("node", nodes[0].NodeID), zap.String("ip", nodes[0].InternalIP))
		instances[0], err = cm.spawnRQLiteRemote(ctx, nodes[0].InternalIP, leaderCfg)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start RQLite leader: %w", err)
	}

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
			err = cm.spawnRQLiteWithSystemd(ctx, followerCfg)
			if err == nil {
				followerInstance = &rqlite.Instance{
					Config: followerCfg,
				}
			}
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

// startOlricCluster starts Olric instances on all nodes concurrently.
// Olric uses memberlist for peer discovery — all peers must be reachable at roughly
// the same time. Sequential spawning fails because early instances exhaust their
// retry budget before later instances start. By spawning all concurrently, all
// memberlist ports open within seconds of each other, allowing discovery to succeed.
func (cm *ClusterManager) startOlricCluster(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) ([]*olric.OlricInstance, error) {
	instances := make([]*olric.OlricInstance, len(nodes))
	errs := make([]error, len(nodes))

	// Build configs for all nodes upfront
	configs := make([]olric.InstanceConfig, len(nodes))
	for i, node := range nodes {
		var peers []string
		for j, peerNode := range nodes {
			if j != i {
				peers = append(peers, fmt.Sprintf("%s:%d", peerNode.InternalIP, portBlocks[j].OlricMemberlistPort))
			}
		}
		configs[i] = olric.InstanceConfig{
			Namespace:      cluster.NamespaceName,
			NodeID:         node.NodeID,
			HTTPPort:       portBlocks[i].OlricHTTPPort,
			MemberlistPort: portBlocks[i].OlricMemberlistPort,
			BindAddr:       node.InternalIP, // Bind to WG IP directly (0.0.0.0 resolves to IPv6 on some hosts)
			AdvertiseAddr:  node.InternalIP, // Advertise WG IP to peers
			PeerAddresses:  peers,
		}
	}

	// Spawn all instances concurrently
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n NodeCapacity) {
			defer wg.Done()
			if n.NodeID == cm.localNodeID {
				cm.logger.Info("Spawning Olric locally", zap.String("node", n.NodeID))
				errs[idx] = cm.spawnOlricWithSystemd(ctx, configs[idx])
				if errs[idx] == nil {
					instances[idx] = &olric.OlricInstance{
						Namespace:      configs[idx].Namespace,
						NodeID:         configs[idx].NodeID,
						HTTPPort:       configs[idx].HTTPPort,
						MemberlistPort: configs[idx].MemberlistPort,
						BindAddr:       configs[idx].BindAddr,
						AdvertiseAddr:  configs[idx].AdvertiseAddr,
						PeerAddresses:  configs[idx].PeerAddresses,
						Status:         olric.InstanceStatusRunning,
						StartedAt:      time.Now(),
					}
				}
			} else {
				cm.logger.Info("Spawning Olric remotely", zap.String("node", n.NodeID), zap.String("ip", n.InternalIP))
				instances[idx], errs[idx] = cm.spawnOlricRemote(ctx, n.InternalIP, configs[idx])
			}
		}(i, node)
	}
	wg.Wait()

	// Check for errors — if any failed, stop all and return
	for i, err := range errs {
		if err != nil {
			cm.logger.Error("Olric spawn failed", zap.String("node", nodes[i].NodeID), zap.Error(err))
			// Stop any that succeeded
			for j := range nodes {
				if errs[j] == nil {
					cm.stopOlricOnNode(ctx, nodes[j].NodeID, nodes[j].InternalIP, cluster.NamespaceName)
				}
			}
			return nil, fmt.Errorf("failed to start Olric on node %s: %w", nodes[i].NodeID, err)
		}
	}

	// All instances started — give memberlist time to converge.
	// Olric's memberlist retries peer joins every ~1s for ~10 attempts.
	// Since all instances are now up, they should discover each other quickly.
	cm.logger.Info("All Olric instances started, waiting for memberlist convergence",
		zap.Int("node_count", len(nodes)),
	)
	time.Sleep(5 * time.Second)

	// Log events and record cluster nodes
	for i, node := range nodes {
		cm.logEvent(ctx, cluster.ID, EventOlricStarted, node.NodeID, "Olric instance started", nil)
		cm.logEvent(ctx, cluster.ID, EventOlricJoined, node.NodeID, "Olric instance joined memberlist", nil)

		if err := cm.insertClusterNode(ctx, cluster.ID, node.NodeID, NodeRoleOlric, portBlocks[i]); err != nil {
			cm.logger.Warn("Failed to record cluster node", zap.Error(err))
		}
	}

	// Verify at least the local instance is still healthy after convergence
	for i, node := range nodes {
		if node.NodeID == cm.localNodeID && instances[i] != nil {
			healthy, err := instances[i].IsHealthy(ctx)
			if !healthy {
				cm.logger.Warn("Local Olric instance unhealthy after convergence wait", zap.Error(err))
			} else {
				cm.logger.Info("Local Olric instance healthy after convergence")
			}
		}
	}

	return instances, nil
}

// startGatewayCluster starts Gateway instances on all nodes (locally or remotely)
func (cm *ClusterManager) startGatewayCluster(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock, rqliteInstances []*rqlite.Instance, olricInstances []*olric.OlricInstance) ([]*gateway.GatewayInstance, error) {
	instances := make([]*gateway.GatewayInstance, len(nodes))

	// Build Olric server addresses — always use WireGuard IPs (Olric binds to WireGuard interface)
	olricServers := make([]string, len(olricInstances))
	for i, inst := range olricInstances {
		olricServers[i] = inst.AdvertisedDSN() // Always use WireGuard IP
	}

	// Start all Gateway instances
	for i, node := range nodes {
		// Connect to local RQLite instance on each node
		rqliteDSN := fmt.Sprintf("http://localhost:%d", portBlocks[i].RQLiteHTTPPort)

		cfg := gateway.InstanceConfig{
			Namespace:             cluster.NamespaceName,
			NodeID:                node.NodeID,
			HTTPPort:              portBlocks[i].GatewayHTTPPort,
			BaseDomain:            cm.baseDomain,
			RQLiteDSN:             rqliteDSN,
			GlobalRQLiteDSN:       cm.globalRQLiteDSN,
			OlricServers:          olricServers,
			OlricTimeout:          30 * time.Second,
			IPFSClusterAPIURL:     cm.ipfsClusterAPIURL,
			IPFSAPIURL:            cm.ipfsAPIURL,
			IPFSTimeout:           cm.ipfsTimeout,
			IPFSReplicationFactor: cm.ipfsReplicationFactor,
			SecretsEncryptionKey:  cm.secretsEncryptionKey,
		}

		var instance *gateway.GatewayInstance
		var err error
		if node.NodeID == cm.localNodeID {
			cm.logger.Info("Spawning Gateway locally", zap.String("node", node.NodeID))
			err = cm.spawnGatewayWithSystemd(ctx, cfg)
			if err == nil {
				instance = &gateway.GatewayInstance{
					Namespace:    cfg.Namespace,
					NodeID:       cfg.NodeID,
					HTTPPort:     cfg.HTTPPort,
					BaseDomain:   cfg.BaseDomain,
					RQLiteDSN:    cfg.RQLiteDSN,
					OlricServers: cfg.OlricServers,
					Status:       gateway.InstanceStatusRunning,
					StartedAt:    time.Now(),
				}
			}
		} else {
			cm.logger.Info("Spawning Gateway remotely", zap.String("node", node.NodeID), zap.String("ip", node.InternalIP))
			instance, err = cm.spawnGatewayRemote(ctx, node.InternalIP, cfg)
		}
		if err != nil {
			// Stop previously started instances
			for j := 0; j < i; j++ {
				cm.stopGatewayOnNode(ctx, nodes[j].NodeID, nodes[j].InternalIP, cluster.NamespaceName)
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
		"action":               "spawn-rqlite",
		"namespace":            cfg.Namespace,
		"node_id":              cfg.NodeID,
		"rqlite_http_port":     cfg.HTTPPort,
		"rqlite_raft_port":     cfg.RaftPort,
		"rqlite_http_adv_addr": cfg.HTTPAdvAddress,
		"rqlite_raft_adv_addr": cfg.RaftAdvAddress,
		"rqlite_join_addrs":    cfg.JoinAddresses,
		"rqlite_is_leader":     cfg.IsLeader,
	})
	if err != nil {
		return nil, err
	}
	return &rqlite.Instance{PID: resp.PID}, nil
}

// spawnOlricRemote sends a spawn-olric request to a remote node
func (cm *ClusterManager) spawnOlricRemote(ctx context.Context, nodeIP string, cfg olric.InstanceConfig) (*olric.OlricInstance, error) {
	resp, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":                "spawn-olric",
		"namespace":             cfg.Namespace,
		"node_id":               cfg.NodeID,
		"olric_http_port":       cfg.HTTPPort,
		"olric_memberlist_port": cfg.MemberlistPort,
		"olric_bind_addr":       cfg.BindAddr,
		"olric_advertise_addr":  cfg.AdvertiseAddr,
		"olric_peer_addresses":  cfg.PeerAddresses,
	})
	if err != nil {
		return nil, err
	}
	return &olric.OlricInstance{
		PID:            resp.PID,
		HTTPPort:       cfg.HTTPPort,
		MemberlistPort: cfg.MemberlistPort,
		BindAddr:       cfg.BindAddr,
		AdvertiseAddr:  cfg.AdvertiseAddr,
	}, nil
}

// spawnGatewayRemote sends a spawn-gateway request to a remote node
func (cm *ClusterManager) spawnGatewayRemote(ctx context.Context, nodeIP string, cfg gateway.InstanceConfig) (*gateway.GatewayInstance, error) {
	ipfsTimeout := ""
	if cfg.IPFSTimeout > 0 {
		ipfsTimeout = cfg.IPFSTimeout.String()
	}

	olricTimeout := ""
	if cfg.OlricTimeout > 0 {
		olricTimeout = cfg.OlricTimeout.String()
	}

	resp, err := cm.sendSpawnRequest(ctx, nodeIP, map[string]interface{}{
		"action":                      "spawn-gateway",
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
		"gateway_turn_secret":         cfg.TURNSecret,
		"gateway_turn_stealth_domain": cfg.TURNStealthDomain,
		// Bugboard #837 follow-up: carry the host secrets encryption key to
		// the remote node so its spawned namespace gateway can manage secrets.
		"gateway_secrets_encryption_key": cfg.SecretsEncryptionKey,
	})
	if err != nil {
		return nil, err
	}
	return &gateway.GatewayInstance{
		Namespace:    cfg.Namespace,
		NodeID:       cfg.NodeID,
		HTTPPort:     cfg.HTTPPort,
		BaseDomain:   cfg.BaseDomain,
		RQLiteDSN:    cfg.RQLiteDSN,
		OlricServers: cfg.OlricServers,
		PID:          resp.PID,
	}, nil
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
		cm.systemdSpawner.StopRQLite(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-rqlite", namespace, nodeID)
	}
}

// stopOlricOnNode stops an Olric instance on a node (local or remote)
func (cm *ClusterManager) stopOlricOnNode(ctx context.Context, nodeID, nodeIP, namespace string) {
	if nodeID == cm.localNodeID {
		cm.systemdSpawner.StopOlric(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-olric", namespace, nodeID)
	}
}

// stopGatewayOnNode stops a Gateway instance on a node (local or remote)
func (cm *ClusterManager) stopGatewayOnNode(ctx context.Context, nodeID, nodeIP, namespace string) {
	if nodeID == cm.localNodeID {
		cm.systemdSpawner.StopGateway(ctx, namespace, nodeID)
	} else {
		cm.sendStopRequest(ctx, nodeIP, "stop-gateway", namespace, nodeID)
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

// createDNSRecords creates DNS records for the namespace gateway.
// Creates A records (+ wildcards) pointing to the public IPs of nodes running the namespace gateway cluster.
func (cm *ClusterManager) createDNSRecords(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) error {
	// Collect public IPs from the selected cluster nodes
	var gatewayIPs []string
	for _, node := range nodes {
		if node.IPAddress != "" {
			gatewayIPs = append(gatewayIPs, node.IPAddress)
		}
	}

	if len(gatewayIPs) == 0 {
		cm.logger.Error("No valid node IPs found for DNS records",
			zap.String("namespace", cluster.NamespaceName),
			zap.Int("node_count", len(nodes)),
		)
		return fmt.Errorf("no valid node IPs found for DNS records")
	}

	if err := cm.dnsManager.CreateNamespaceRecords(ctx, cluster.NamespaceName, gatewayIPs); err != nil {
		return err
	}

	fqdn := fmt.Sprintf("ns-%s.%s.", cluster.NamespaceName, cm.baseDomain)
	cm.logEvent(ctx, cluster.ID, EventDNSCreated, "", fmt.Sprintf("DNS records created for %s (%d gateway node records)", fqdn, len(gatewayIPs)*2), nil)
	return nil
}

// rollbackProvisioning cleans up a failed provisioning attempt
func (cm *ClusterManager) rollbackProvisioning(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock, rqliteInstances []*rqlite.Instance, olricInstances []*olric.OlricInstance) {
	cm.logger.Info("Rolling back failed provisioning", zap.String("cluster_id", cluster.ID))

	// Stop all namespace services (Gateway, Olric, RQLite) using systemd
	cm.systemdSpawner.StopAll(ctx, cluster.NamespaceName)

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

// DeprovisionCluster tears down a namespace cluster on all nodes.
// Stops namespace infrastructure (Gateway, Olric, RQLite) on every cluster node,
// deletes cluster-state.json, deallocates ports, removes DNS records, and cleans up DB.
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

	// 1. Get cluster nodes WITH IPs (must happen before any DB deletion)
	type deprovisionNodeInfo struct {
		NodeID     string `db:"node_id"`
		InternalIP string `db:"internal_ip"`
	}
	var clusterNodes []deprovisionNodeInfo
	nodeQuery := `
		SELECT ncn.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip
		FROM namespace_cluster_nodes ncn
		JOIN dns_nodes dn ON ncn.node_id = dn.id
		WHERE ncn.namespace_cluster_id = ?
	`
	if err := cm.db.Query(ctx, &clusterNodes, nodeQuery, cluster.ID); err != nil {
		cm.logger.Warn("Failed to query cluster nodes for deprovisioning, falling back to local-only stop", zap.Error(err))
		// Fall back to local-only stop (individual methods, NOT StopAll which uses dangerous glob)
		// Stop WebRTC services first (SFU → TURN), then core services (Gateway → Olric → RQLite)
		cm.systemdSpawner.StopSFU(ctx, cluster.NamespaceName, cm.localNodeID)
		cm.systemdSpawner.StopTURN(ctx, cluster.NamespaceName, cm.localNodeID)
		cm.systemdSpawner.StopGateway(ctx, cluster.NamespaceName, cm.localNodeID)
		cm.systemdSpawner.StopOlric(ctx, cluster.NamespaceName, cm.localNodeID)
		cm.systemdSpawner.StopRQLite(ctx, cluster.NamespaceName, cm.localNodeID)
		cm.systemdSpawner.DeleteClusterState(cluster.NamespaceName)
	} else {
		// 2. Stop WebRTC services first (SFU → TURN), then core infra (Gateway → Olric → RQLite)
		for _, node := range clusterNodes {
			cm.stopSFUOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName)
		}
		for _, node := range clusterNodes {
			cm.stopTURNOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName)
		}
		for _, node := range clusterNodes {
			cm.stopGatewayOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName)
		}
		for _, node := range clusterNodes {
			cm.stopOlricOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName)
		}
		for _, node := range clusterNodes {
			cm.stopRQLiteOnNode(ctx, node.NodeID, node.InternalIP, cluster.NamespaceName, nil)
		}

		// 3. Delete cluster-state.json on all nodes
		for _, node := range clusterNodes {
			if node.NodeID == cm.localNodeID {
				cm.systemdSpawner.DeleteClusterState(cluster.NamespaceName)
			} else {
				cm.sendStopRequest(ctx, node.InternalIP, "delete-cluster-state", cluster.NamespaceName, node.NodeID)
			}
		}
	}

	// 4. Deallocate all ports (core + WebRTC)
	cm.portAllocator.DeallocateAllPortBlocks(ctx, cluster.ID)
	cm.webrtcPortAllocator.DeallocateAll(ctx, cluster.ID)

	// 5. Delete namespace DNS records (gateway + TURN)
	cm.dnsManager.DeleteNamespaceRecords(ctx, cluster.NamespaceName)
	cm.dnsManager.DeleteTURNRecords(ctx, cluster.NamespaceName)

	// 6. Explicitly delete child tables (FK cascades disabled in rqlite)
	cm.db.Exec(ctx, `DELETE FROM namespace_cluster_events WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(ctx, `DELETE FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(ctx, `DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(ctx, `DELETE FROM webrtc_port_allocations WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(ctx, `DELETE FROM webrtc_rooms WHERE namespace_cluster_id = ?`, cluster.ID)
	cm.db.Exec(ctx, `DELETE FROM namespace_webrtc_config WHERE namespace_cluster_id = ?`, cluster.ID)

	// 7. Delete cluster record
	cm.db.Exec(ctx, `DELETE FROM namespace_clusters WHERE id = ?`, cluster.ID)

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

	// Check individual service status by inspecting cluster nodes
	nodes, err := cm.getClusterNodes(ctx, clusterID)
	if err == nil {
		runningCount := 0
		hasRQLite := false
		hasOlric := false
		hasGateway := false

		for _, node := range nodes {
			status.Nodes = append(status.Nodes, node.NodeID)
			if node.Status == NodeStatusRunning {
				runningCount++
			}
			if node.RQLiteHTTPPort > 0 {
				hasRQLite = true
			}
			if node.OlricHTTPPort > 0 {
				hasOlric = true
			}
			if node.GatewayHTTPPort > 0 {
				hasGateway = true
			}
		}

		allRunning := len(nodes) > 0 && runningCount == len(nodes)
		status.RQLiteReady = allRunning && hasRQLite
		status.OlricReady = allRunning && hasOlric
		status.GatewayReady = allRunning && hasGateway
		status.DNSReady = allRunning
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
		// Recover from panics (e.g., gorqlite index-out-of-range) so the
		// goroutine doesn't die silently leaving status stuck at "provisioning".
		if r := recover(); r != nil {
			cm.logger.Error("Provisioning panicked",
				zap.String("namespace", namespaceName),
				zap.Any("panic", r),
			)
			bgCtx := context.Background()
			cm.updateClusterStatus(bgCtx, cluster.ID, ClusterStatusFailed,
				fmt.Sprintf("provisioning panicked: %v", r))
		}
		cm.provisioningMu.Lock()
		delete(cm.provisioning, namespaceName)
		cm.provisioningMu.Unlock()
	}()

	// Overall timeout — prevents the goroutine from hanging indefinitely
	// if a remote spawn request or RQLite write blocks.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

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

	// Save cluster-state.json on all nodes (local + remote) for disk-based restore
	// on restart. The synchronous ProvisionCluster path does this; the async path
	// previously did not, so any namespace created through the normal (async) flow
	// had no state file and was silently dropped on the next node reboot (disk
	// restore found nothing and the DB fallback was skipped when other namespaces
	// had state files). Mirror the sync path so async-provisioned clusters survive.
	cm.saveClusterStateToAllNodes(ctx, cluster, nodes, portBlocks)

	cm.logger.Info("Cluster provisioning completed",
		zap.String("cluster_id", cluster.ID),
		zap.String("namespace", namespaceName),
	)
}

// RestoreLocalClusters restores namespace cluster processes that should be running on this node.
// Called on node startup to re-spawn RQLite, Olric, and Gateway processes for clusters
// that were previously provisioned and assigned to this node.
func (cm *ClusterManager) RestoreLocalClusters(ctx context.Context) error {
	if cm.localNodeID == "" {
		return fmt.Errorf("local node ID not set")
	}

	cm.logger.Info("Checking for namespace clusters to restore", zap.String("local_node_id", cm.localNodeID))

	// Find all ready clusters that have this node assigned
	type clusterNodeInfo struct {
		ClusterID     string `db:"namespace_cluster_id"`
		NamespaceName string `db:"namespace_name"`
		NodeID        string `db:"node_id"`
		Role          string `db:"role"`
	}
	var assignments []clusterNodeInfo
	query := `
		SELECT DISTINCT cn.namespace_cluster_id, c.namespace_name, cn.node_id, cn.role
		FROM namespace_cluster_nodes cn
		JOIN namespace_clusters c ON cn.namespace_cluster_id = c.id
		WHERE cn.node_id = ? AND c.status = 'ready'
	`
	if err := cm.db.Query(ctx, &assignments, query, cm.localNodeID); err != nil {
		return fmt.Errorf("failed to query local cluster assignments: %w", err)
	}

	if len(assignments) == 0 {
		cm.logger.Info("No namespace clusters to restore on this node")
		return nil
	}

	// Group by cluster
	clusterNamespaces := make(map[string]string) // clusterID -> namespaceName
	for _, a := range assignments {
		clusterNamespaces[a.ClusterID] = a.NamespaceName
	}

	cm.logger.Info("Found namespace clusters to restore",
		zap.Int("count", len(clusterNamespaces)),
		zap.String("local_node_id", cm.localNodeID),
	)

	// Get local node's WireGuard IP
	type nodeIPInfo struct {
		InternalIP string `db:"internal_ip"`
	}
	var localNodeInfo []nodeIPInfo
	ipQuery := `SELECT COALESCE(internal_ip, ip_address) as internal_ip FROM dns_nodes WHERE id = ? LIMIT 1`
	if err := cm.db.Query(ctx, &localNodeInfo, ipQuery, cm.localNodeID); err != nil || len(localNodeInfo) == 0 {
		cm.logger.Warn("Could not determine local node IP, skipping restore", zap.Error(err))
		return fmt.Errorf("failed to get local node IP: %w", err)
	}
	localIP := localNodeInfo[0].InternalIP

	for clusterID, namespaceName := range clusterNamespaces {
		if err := cm.restoreClusterOnNode(ctx, clusterID, namespaceName, localIP); err != nil {
			cm.logger.Error("Failed to restore namespace cluster",
				zap.String("namespace", namespaceName),
				zap.String("cluster_id", clusterID),
				zap.Error(err),
			)
			// Continue restoring other clusters
		}
	}

	return nil
}

// restoreClusterOnNode restores all processes for a single cluster on this node
func (cm *ClusterManager) restoreClusterOnNode(ctx context.Context, clusterID, namespaceName, localIP string) error {
	cm.logger.Info("Restoring namespace cluster processes",
		zap.String("namespace", namespaceName),
		zap.String("cluster_id", clusterID),
	)

	// Get port allocation for this node
	var portBlocks []PortBlock
	portQuery := `SELECT * FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`
	if err := cm.db.Query(ctx, &portBlocks, portQuery, clusterID, cm.localNodeID); err != nil || len(portBlocks) == 0 {
		return fmt.Errorf("no port allocation found for cluster %s on node %s", clusterID, cm.localNodeID)
	}
	pb := &portBlocks[0]

	// Get all nodes in this cluster (for join addresses and peer addresses)
	allNodes, err := cm.getClusterNodes(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	// Get all nodes' IPs and port allocations
	type nodePortInfo struct {
		NodeID              string `db:"node_id"`
		InternalIP          string `db:"internal_ip"`
		RQLiteHTTPPort      int    `db:"rqlite_http_port"`
		RQLiteRaftPort      int    `db:"rqlite_raft_port"`
		OlricHTTPPort       int    `db:"olric_http_port"`
		OlricMemberlistPort int    `db:"olric_memberlist_port"`
	}
	var allNodePorts []nodePortInfo
	allPortsQuery := `
		SELECT pa.node_id, COALESCE(dn.internal_ip, dn.ip_address) as internal_ip,
			pa.rqlite_http_port, pa.rqlite_raft_port, pa.olric_http_port, pa.olric_memberlist_port
		FROM namespace_port_allocations pa
		JOIN dns_nodes dn ON pa.node_id = dn.id
		WHERE pa.namespace_cluster_id = ?
	`
	if err := cm.db.Query(ctx, &allNodePorts, allPortsQuery, clusterID); err != nil {
		return fmt.Errorf("failed to get all node ports: %w", err)
	}

	// 1. Restore RQLite
	// Check if RQLite systemd service is already running
	rqliteRunning, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(namespaceName, systemd.ServiceTypeRQLite)
	if !rqliteRunning {
		// Check if RQLite data directory exists (has existing data)
		dataDir := filepath.Join(cm.baseDataDir, namespaceName, "rqlite", cm.localNodeID)
		hasExistingData := false
		if _, err := os.Stat(filepath.Join(dataDir, "raft")); err == nil {
			hasExistingData = true
		}

		if hasExistingData {
			// Write peers.json for Raft cluster recovery (official RQLite mechanism).
			// When all nodes restart simultaneously, Raft can't form quorum from stale state.
			// peers.json tells rqlited the correct voter list so it can hold a fresh election.
			var peers []rqlite.RaftPeer
			for _, np := range allNodePorts {
				raftAddr := fmt.Sprintf("%s:%d", np.InternalIP, np.RQLiteRaftPort)
				peers = append(peers, rqlite.RaftPeer{
					ID:       raftAddr,
					Address:  raftAddr,
					NonVoter: false,
				})
			}
			if err := cm.writePeersJSON(dataDir, peers); err != nil {
				cm.logger.Error("Failed to write peers.json", zap.String("namespace", namespaceName), zap.Error(err))
			}
		}

		// Build join addresses for first-time joins (no existing data)
		var joinAddrs []string
		isLeader := false
		if !hasExistingData {
			// Deterministic leader selection: sort all node IDs and pick the first one.
			// Every node independently computes the same result — no coordination needed.
			// The elected leader bootstraps the cluster; followers use -join with retries
			// to wait for the leader to become ready (up to 5 minutes).
			sortedNodeIDs := make([]string, 0, len(allNodePorts))
			for _, np := range allNodePorts {
				sortedNodeIDs = append(sortedNodeIDs, np.NodeID)
			}
			sort.Strings(sortedNodeIDs)
			electedLeaderID := sortedNodeIDs[0]

			if cm.localNodeID == electedLeaderID {
				isLeader = true
				cm.logger.Info("Deterministic leader election: this node is the leader",
					zap.String("namespace", namespaceName),
					zap.String("node_id", cm.localNodeID))
			} else {
				// Follower: join the elected leader's raft address
				for _, np := range allNodePorts {
					if np.NodeID == electedLeaderID {
						joinAddrs = append(joinAddrs, fmt.Sprintf("%s:%d", np.InternalIP, np.RQLiteRaftPort))
						break
					}
				}
				cm.logger.Info("Deterministic leader election: this node is a follower",
					zap.String("namespace", namespaceName),
					zap.String("node_id", cm.localNodeID),
					zap.String("leader_id", electedLeaderID),
					zap.Strings("join_addrs", joinAddrs))
			}
		}

		rqliteCfg := rqlite.InstanceConfig{
			Namespace:      namespaceName,
			NodeID:         cm.localNodeID,
			HTTPPort:       pb.RQLiteHTTPPort,
			RaftPort:       pb.RQLiteRaftPort,
			HTTPAdvAddress: fmt.Sprintf("%s:%d", localIP, pb.RQLiteHTTPPort),
			RaftAdvAddress: fmt.Sprintf("%s:%d", localIP, pb.RQLiteRaftPort),
			JoinAddresses:  joinAddrs,
			IsLeader:       isLeader,
		}

		if err := cm.spawnRQLiteWithSystemd(ctx, rqliteCfg); err != nil {
			cm.logger.Error("Failed to restore RQLite", zap.String("namespace", namespaceName), zap.Error(err))
		} else {
			cm.logger.Info("Restored RQLite instance", zap.String("namespace", namespaceName), zap.Int("port", pb.RQLiteHTTPPort))
		}
	} else {
		cm.logger.Info("RQLite already running", zap.String("namespace", namespaceName), zap.Int("port", pb.RQLiteHTTPPort))
	}

	// 2. Restore Olric
	olricRunning := false
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", pb.OlricMemberlistPort), 2*time.Second)
	if err == nil {
		conn.Close()
		olricRunning = true
	}

	if !olricRunning {
		var peers []string
		for _, np := range allNodePorts {
			if np.NodeID != cm.localNodeID {
				peers = append(peers, fmt.Sprintf("%s:%d", np.InternalIP, np.OlricMemberlistPort))
			}
		}

		olricCfg := olric.InstanceConfig{
			Namespace:      namespaceName,
			NodeID:         cm.localNodeID,
			HTTPPort:       pb.OlricHTTPPort,
			MemberlistPort: pb.OlricMemberlistPort,
			BindAddr:       localIP,
			AdvertiseAddr:  localIP,
			PeerAddresses:  peers,
		}

		if err := cm.spawnOlricWithSystemd(ctx, olricCfg); err != nil {
			cm.logger.Error("Failed to restore Olric", zap.String("namespace", namespaceName), zap.Error(err))
		} else {
			cm.logger.Info("Restored Olric instance", zap.String("namespace", namespaceName), zap.Int("port", pb.OlricHTTPPort))
		}
	} else {
		cm.logger.Info("Olric already running", zap.String("namespace", namespaceName), zap.Int("port", pb.OlricMemberlistPort))
	}

	// 3. Restore Gateway
	// Check if any cluster node has the gateway role (gateway may have been skipped during provisioning)
	hasGateway := false
	for _, node := range allNodes {
		if node.Role == NodeRoleGateway {
			hasGateway = true
			break
		}
	}

	if hasGateway {
		gwRunning := false
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/v1/health", pb.GatewayHTTPPort))
		if err == nil {
			resp.Body.Close()
			gwRunning = true
		}

		if !gwRunning {
			// Build olric server addresses — always use WireGuard IPs (Olric binds to WireGuard interface)
			var olricServers []string
			for _, np := range allNodePorts {
				olricServers = append(olricServers, fmt.Sprintf("%s:%d", np.InternalIP, np.OlricHTTPPort))
			}

			gwCfg := gateway.InstanceConfig{
				Namespace:             namespaceName,
				NodeID:                cm.localNodeID,
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
				SecretsEncryptionKey:  cm.secretsEncryptionKey,
			}

			// Add WebRTC config if enabled for this namespace.
			//
			// TURN and SFU are DECOUPLED (bugboard #25): the TURN shared secret is
			// namespace-wide, so ANY gateway for the namespace can mint TURN
			// credentials — the TURN servers are remote and a credential is just an
			// HMAC of that secret. The SFU port, by contrast, is per-node and only
			// exists on nodes holding an SFU allocation.
			//
			// These used to be set together inside `if sfuBlock != nil`, so a gateway
			// on a node with no SFU allocation got NO turn_secret at all and answered
			// /v1/webrtc/turn/credentials with 503 "TURN not configured". That is
			// reachable in normal operation: dead-node failover can make a node a
			// gateway for a namespace it holds no WebRTC allocation in (observed on
			// devnet — 57.131.41.160 served ~50% of anchat-test credential requests
			// and failed every one of them).
			if webrtcCfg, err := cm.GetWebRTCConfig(ctx, namespaceName); err == nil && webrtcCfg != nil {
				gwCfg.TURNDomain = fmt.Sprintf("turn.ns-%s.%s", namespaceName, cm.baseDomain)
				gwCfg.TURNSecret = webrtcCfg.TURNSharedSecret
				gwCfg.TURNStealthDomain = cm.stealthDomainFor(namespaceName, webrtcCfg)
				// WebRTCEnabled is the legacy SFU-presence flag; the route gate no
				// longer keys on it (#25/#411). Only an SFU node reports a port.
				if sfuBlock, serr := cm.webrtcPortAllocator.GetSFUPorts(ctx, clusterID, cm.localNodeID); serr == nil && sfuBlock != nil {
					gwCfg.WebRTCEnabled = true
					gwCfg.SFUPort = sfuBlock.SFUSignalingPort
				}
			}

			if err := cm.spawnGatewayWithSystemd(ctx, gwCfg); err != nil {
				cm.logger.Error("Failed to restore Gateway", zap.String("namespace", namespaceName), zap.Error(err))
			} else {
				cm.logger.Info("Restored Gateway instance", zap.String("namespace", namespaceName), zap.Int("port", pb.GatewayHTTPPort))
			}
		} else {
			cm.logger.Info("Gateway already running", zap.String("namespace", namespaceName), zap.Int("port", pb.GatewayHTTPPort))
		}
	}

	// Save local state to disk for future restarts without DB dependency
	var stateNodes []ClusterLocalStateNode
	for _, np := range allNodePorts {
		stateNodes = append(stateNodes, ClusterLocalStateNode{
			NodeID:              np.NodeID,
			InternalIP:          np.InternalIP,
			RQLiteHTTPPort:      np.RQLiteHTTPPort,
			RQLiteRaftPort:      np.RQLiteRaftPort,
			OlricHTTPPort:       np.OlricHTTPPort,
			OlricMemberlistPort: np.OlricMemberlistPort,
		})
	}
	localState := &ClusterLocalState{
		ClusterID:     clusterID,
		NamespaceName: namespaceName,
		LocalNodeID:   cm.localNodeID,
		LocalIP:       localIP,
		LocalPorts: ClusterLocalStatePorts{
			RQLiteHTTPPort:      pb.RQLiteHTTPPort,
			RQLiteRaftPort:      pb.RQLiteRaftPort,
			OlricHTTPPort:       pb.OlricHTTPPort,
			OlricMemberlistPort: pb.OlricMemberlistPort,
			GatewayHTTPPort:     pb.GatewayHTTPPort,
		},
		AllNodes:   stateNodes,
		HasGateway: hasGateway,
		BaseDomain: cm.baseDomain,
		SavedAt:    time.Now(),
	}
	if err := cm.saveLocalState(localState); err != nil {
		cm.logger.Warn("Failed to save cluster local state", zap.String("namespace", namespaceName), zap.Error(err))
	}

	return nil
}

// ClusterLocalState is persisted to disk so namespace processes can be restored
// without querying the main RQLite cluster (which may not have a leader yet on cold start).
type ClusterLocalState struct {
	ClusterID     string                  `json:"cluster_id"`
	NamespaceName string                  `json:"namespace_name"`
	LocalNodeID   string                  `json:"local_node_id"`
	LocalIP       string                  `json:"local_ip"`
	LocalPorts    ClusterLocalStatePorts  `json:"local_ports"`
	AllNodes      []ClusterLocalStateNode `json:"all_nodes"`
	HasGateway    bool                    `json:"has_gateway"`
	BaseDomain    string                  `json:"base_domain"`
	SavedAt       time.Time               `json:"saved_at"`

	// WebRTC fields (zero values when WebRTC not enabled — backward compatible)
	HasSFU             bool   `json:"has_sfu,omitempty"`
	HasTURN            bool   `json:"has_turn,omitempty"`
	TURNSharedSecret   string `json:"turn_shared_secret,omitempty"`  // Needed for gateway to generate TURN credentials on cold start
	TURNDomain         string `json:"turn_domain,omitempty"`         // TURN server domain for gateway config
	TURNStealthDomain  string `json:"turn_stealth_domain,omitempty"` // Stealth TURNS:443 host (feat-124); empty when stealth disabled
	TURNCredentialTTL  int    `json:"turn_credential_ttl,omitempty"`
	SFUSignalingPort   int    `json:"sfu_signaling_port,omitempty"`
	SFUMediaPortStart  int    `json:"sfu_media_port_start,omitempty"`
	SFUMediaPortEnd    int    `json:"sfu_media_port_end,omitempty"`
	TURNListenPort     int    `json:"turn_listen_port,omitempty"`
	TURNTLSPort        int    `json:"turn_tls_port,omitempty"`
	TURNRelayPortStart int    `json:"turn_relay_port_start,omitempty"`
	TURNRelayPortEnd   int    `json:"turn_relay_port_end,omitempty"`
}

type ClusterLocalStatePorts struct {
	RQLiteHTTPPort      int `json:"rqlite_http_port"`
	RQLiteRaftPort      int `json:"rqlite_raft_port"`
	OlricHTTPPort       int `json:"olric_http_port"`
	OlricMemberlistPort int `json:"olric_memberlist_port"`
	GatewayHTTPPort     int `json:"gateway_http_port"`
}

type ClusterLocalStateNode struct {
	NodeID              string `json:"node_id"`
	InternalIP          string `json:"internal_ip"`
	RQLiteHTTPPort      int    `json:"rqlite_http_port"`
	RQLiteRaftPort      int    `json:"rqlite_raft_port"`
	OlricHTTPPort       int    `json:"olric_http_port"`
	OlricMemberlistPort int    `json:"olric_memberlist_port"`
}

// saveClusterStateToAllNodes builds and saves cluster-state.json on every node in the cluster.
// Each node gets its own state file with node-specific LocalNodeID, LocalIP, and LocalPorts.
func (cm *ClusterManager) saveClusterStateToAllNodes(ctx context.Context, cluster *NamespaceCluster, nodes []NodeCapacity, portBlocks []*PortBlock) {
	// Build the shared AllNodes list
	var allNodes []ClusterLocalStateNode
	for i, node := range nodes {
		allNodes = append(allNodes, ClusterLocalStateNode{
			NodeID:              node.NodeID,
			InternalIP:          node.InternalIP,
			RQLiteHTTPPort:      portBlocks[i].RQLiteHTTPPort,
			RQLiteRaftPort:      portBlocks[i].RQLiteRaftPort,
			OlricHTTPPort:       portBlocks[i].OlricHTTPPort,
			OlricMemberlistPort: portBlocks[i].OlricMemberlistPort,
		})
	}

	for i, node := range nodes {
		state := &ClusterLocalState{
			ClusterID:     cluster.ID,
			NamespaceName: cluster.NamespaceName,
			LocalNodeID:   node.NodeID,
			LocalIP:       node.InternalIP,
			LocalPorts: ClusterLocalStatePorts{
				RQLiteHTTPPort:      portBlocks[i].RQLiteHTTPPort,
				RQLiteRaftPort:      portBlocks[i].RQLiteRaftPort,
				OlricHTTPPort:       portBlocks[i].OlricHTTPPort,
				OlricMemberlistPort: portBlocks[i].OlricMemberlistPort,
				GatewayHTTPPort:     portBlocks[i].GatewayHTTPPort,
			},
			AllNodes:   allNodes,
			HasGateway: true,
			BaseDomain: cm.baseDomain,
			SavedAt:    time.Now(),
		}

		if node.NodeID == cm.localNodeID {
			// Save locally
			if err := cm.saveLocalState(state); err != nil {
				cm.logger.Warn("Failed to save local cluster state", zap.String("namespace", cluster.NamespaceName), zap.Error(err))
			}
		} else {
			// Send to remote node
			data, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				cm.logger.Warn("Failed to marshal cluster state for remote node", zap.String("node", node.NodeID), zap.Error(err))
				continue
			}
			_, err = cm.sendSpawnRequest(ctx, node.InternalIP, map[string]interface{}{
				"action":        "save-cluster-state",
				"namespace":     cluster.NamespaceName,
				"node_id":       node.NodeID,
				"cluster_state": json.RawMessage(data),
			})
			if err != nil {
				cm.logger.Warn("Failed to send cluster state to remote node",
					zap.String("node", node.NodeID),
					zap.String("ip", node.InternalIP),
					zap.Error(err))
			}
		}
	}
}

// saveLocalState writes cluster state to disk for fast recovery without DB queries.
func (cm *ClusterManager) saveLocalState(state *ClusterLocalState) error {
	dir := filepath.Join(cm.baseDataDir, state.NamespaceName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	path := filepath.Join(dir, "cluster-state.json")
	// Atomic write: this file now carries the namespace TURN shared secret
	// (bugboard #130) and is rewritten from multiple converge paths. Write a
	// temp file then rename over the target so a reader (or a concurrent
	// writer) never observes a half-written secret — rename is atomic on the
	// same filesystem. 0600 + chmod on the temp file keeps the secret out of
	// world/group read; the rename then makes the live file 0600 too, which
	// also tightens a file an older release left at 0644.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to set temp state file permissions: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to rename state file into place: %w", err)
	}
	cm.logger.Info("Saved cluster local state", zap.String("namespace", state.NamespaceName), zap.String("path", path))
	return nil
}

// loadLocalState reads cluster state from disk.
func loadLocalState(path string) (*ClusterLocalState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state ClusterLocalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}
	return &state, nil
}

// RestoreLocalClustersFromDisk restores namespace processes using local state files,
// avoiding any dependency on the main RQLite cluster being available.
// Returns the number of namespaces restored, or -1 if no state files were found.
func (cm *ClusterManager) RestoreLocalClustersFromDisk(ctx context.Context) (int, error) {
	pattern := filepath.Join(cm.baseDataDir, "*", "cluster-state.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return -1, fmt.Errorf("failed to glob state files: %w", err)
	}
	if len(matches) == 0 {
		return -1, nil
	}

	cm.logger.Info("Found local cluster state files", zap.Int("count", len(matches)))

	restored := 0
	for _, path := range matches {
		state, err := loadLocalState(path)
		if err != nil {
			cm.logger.Error("Failed to load cluster state file", zap.String("path", path), zap.Error(err))
			continue
		}
		if err := cm.restoreClusterFromState(ctx, state); err != nil {
			cm.logger.Error("Failed to restore cluster from state", zap.String("namespace", state.NamespaceName), zap.Error(err))
			continue
		}
		restored++
	}
	return restored, nil
}

// restoreWebRTC is the resolved WebRTC gateway config for a restored
// namespace gateway.
const (
	// webrtcResolveRetries / webrtcResolveRetryDelay bound how long the converge
	// waits for a slow/just-restarted node's namespace rqlite to become readable
	// before giving up on the WebRTC secret. A distant node (high WG RTT) can
	// take a few seconds to sync; without this it reads empty once and comes up
	// with TURN disabled (bugboard #130). 5 × 2s = 10s ceiling on the cold path.
	webrtcResolveRetries    = 5
	webrtcResolveRetryDelay = 2 * time.Second
)

// resolveWebRTCConfigWithRetry calls fetch up to `retries` times, sleeping
// `delay` between attempts, and returns the first result whose error is nil. A
// distant/just-restarted node's namespace rqlite can take a few seconds to
// become readable; without the retry the read fails once and the gateway comes
// up with TURN disabled (bugboard #130). A genuine decrypt failure (stale
// cluster-secret) also errors and exhausts the retries, returning the final
// error so the caller can mark the result unresolved. `sleep` is injected so
// unit tests exercise the loop without real delay.
func resolveWebRTCConfigWithRetry(retries int, delay time.Duration, sleep func(time.Duration), fetch func() (*WebRTCConfig, error)) (*WebRTCConfig, error) {
	var cfg *WebRTCConfig
	var err error
	for attempt := 0; attempt < retries; attempt++ {
		cfg, err = fetch()
		if err == nil {
			return cfg, nil
		}
		if attempt < retries-1 {
			sleep(delay)
		}
	}
	return cfg, err
}

// applyResolvedWebRTCToState copies a freshly-resolved WebRTC config into the
// local cluster state so a future cold start can read the TURN secret from disk
// instead of the (possibly-slow) namespace rqlite (bugboard #130). Returns true
// iff the state changed, so the caller only rewrites the on-disk file when
// there's something to persist. Pure — unit-testable without a live cluster.
func applyResolvedWebRTCToState(state *ClusterLocalState, wr restoreWebRTC) bool {
	hasTURN := wr.turnSecret != ""
	hasSFU := wr.sfuPort > 0
	if state.TURNSharedSecret == wr.turnSecret &&
		state.TURNDomain == wr.turnDomain &&
		state.TURNStealthDomain == wr.stealthDomain &&
		state.SFUSignalingPort == wr.sfuPort &&
		state.HasTURN == hasTURN &&
		state.HasSFU == hasSFU {
		return false
	}
	state.HasTURN = hasTURN
	state.HasSFU = hasSFU
	state.TURNSharedSecret = wr.turnSecret
	state.TURNDomain = wr.turnDomain
	state.TURNStealthDomain = wr.stealthDomain
	state.SFUSignalingPort = wr.sfuPort
	return true
}

type restoreWebRTC struct {
	enabled       bool
	sfuPort       int
	turnDomain    string
	turnSecret    string
	stealthDomain string // feat-124: empty when webrtc stealth is disabled
	// unresolved is true when the DB lookup ERRORED (vs. resolved-but-not-
	// enabled) AND the local cache had no secret to fall back to. The caller
	// must NOT write a WebRTC-disabled gateway config off an unresolved
	// lookup — that silently kills turn.credentials on a node that should
	// serve TURN (bugboard #130: a decrypt failure after cluster-secret
	// rotation was swallowed into "disabled"). enabled is always false when
	// unresolved.
	unresolved bool
}

// chooseRestoreWebRTC resolves a restored gateway's WebRTC config. TWO
// independent aspects (bugboard #25 decouple):
//
//   - TURN (turnSecret + turnDomain) is NAMESPACE-WIDE. Any gateway with
//     the namespace TURN secret can mint /v1/webrtc/turn/credentials (the
//     credentials are an HMAC; the actual TURN servers are remote). So a
//     gateway node that runs NO local SFU still gets the TURN secret.
//   - SFU (sfuPort) is PER-NODE — non-zero only when this node runs a
//     local SFU (for /v1/webrtc/signal + /rooms proxying).
//
// Precedence: DB-FIRST. The namespace_webrtc_config row is the source of
// truth for the CURRENT TURN secret, so we always consult it. The local
// cluster-state.json cache (dbFetch's counterpart) is a FALLBACK ONLY —
// used when the DB read fails (a slow/just-restarted node whose namespace
// rqlite has not synced yet). This is the bugboard #130 FOLLOW-UP fix: the
// earlier state-FIRST read short-circuited the DB whenever the cache held a
// secret and so NEVER re-validated a present-but-stale cached secret. If a
// secret was rotated (disable→enable) while a node was offline, that node
// kept serving the OLD secret indefinitely. DB-first means a stale cache
// can survive at most until the DB becomes readable on the next converge —
// never indefinitely — while still letting a genuinely DB-down node come up
// on TURN via the cache (the #130 resilience the cache was added for).
//
// `enabled` is true when EITHER a TURN secret OR an SFU port is present,
// so the caller knows to write a webrtc block. A non-SFU gateway gets
// {sfuPort:0, turnSecret:set} — credentials route registers, signal/rooms
// don't.
//
// Extracted as a pure function so the precedence is unit-testable without
// standing up the full restore path (systemd spawner + DB + port store).
func chooseRestoreWebRTC(
	stateHasSFU bool, stateSFUPort int, stateTURNDomain, stateTURNSecret, stateStealthDomain string,
	dbFetch func() (turnSecret, turnDomain, stealthDomain string, sfuPort int, resolved bool),
) restoreWebRTC {
	// DB-first: consult the source of truth before trusting the local cache.
	dbSecret, dbDomain, dbStealth, dbSFU, resolved := dbFetch()
	if resolved {
		// The DB read landed and is authoritative. dbSecret == "" means the
		// namespace genuinely has no WebRTC enabled — honor that (disable),
		// do NOT fall back to a possibly-stale cached secret. A present
		// secret is the CURRENT one and wins over any cached value.
		if dbSecret == "" {
			return restoreWebRTC{}
		}
		return restoreWebRTC{
			enabled:       true,
			sfuPort:       dbSFU,
			turnDomain:    dbDomain,
			turnSecret:    dbSecret,
			stealthDomain: dbStealth,
		}
	}

	// The DB/decrypt lookup ERRORED (slow node whose namespace rqlite is not
	// readable yet, or a decrypt failure after a cluster-secret rotation).
	// Fall back to the locally-cached secret so TURN still comes up — possibly
	// stale, but functional, and self-correcting on the next converge once the
	// DB is readable (NOT indefinite). If the cache is empty too, signal
	// unresolved so the caller preserves the running gateway config instead of
	// blanking TURN (bugboard #130).
	sfuPort := 0
	if stateHasSFU && stateSFUPort > 0 {
		sfuPort = stateSFUPort
	}
	if stateTURNSecret == "" && sfuPort == 0 {
		return restoreWebRTC{unresolved: true}
	}
	return restoreWebRTC{
		enabled:       stateTURNSecret != "" || sfuPort > 0,
		sfuPort:       sfuPort,
		turnDomain:    stateTURNDomain,
		turnSecret:    stateTURNSecret,
		stealthDomain: stateStealthDomain,
	}
}

// sfuPortBlockSpawnable reports whether a DB SFU port allocation is complete
// enough to spawn a pion SFU. A nil block (no allocation for this node) or a
// zero signaling/media-start port would produce a crash-looping unit that fails
// to bind, so the restore path must skip the spawn rather than write a broken
// config. Pure function so the guard is unit-testable without a live cluster.
func sfuPortBlockSpawnable(block *WebRTCPortBlock) bool {
	return block != nil && block.SFUSignalingPort > 0 && block.SFUMediaPortStart > 0 && block.SFUMediaPortEnd > 0
}

// turnPortBlockSpawnable reports whether a DB TURN allocation is complete enough
// to spawn a TURN server. The listen/TLS ports matter as much as the relay range:
// a config with listen port 0 does NOT fail — pion binds "0.0.0.0:0" to a random
// ephemeral port, systemd reports the unit active, and the node stays in TURN DNS
// while relaying nothing. A silent dead relay is far worse than a refused spawn,
// so every port is checked here rather than trusted. Pure, so it is testable.
func turnPortBlockSpawnable(block *WebRTCPortBlock) bool {
	return block != nil &&
		block.TURNListenPort > 0 && block.TURNTLSPort > 0 &&
		block.TURNRelayPortStart > 0 && block.TURNRelayPortEnd > 0
}

// restoreClusterFromState restores all processes for a cluster using local state (no DB queries).
func (cm *ClusterManager) restoreClusterFromState(ctx context.Context, state *ClusterLocalState) error {
	cm.logger.Info("Restoring namespace cluster from local state",
		zap.String("namespace", state.NamespaceName),
		zap.String("cluster_id", state.ClusterID),
	)

	// Self-check: verify this node is still assigned to this cluster in the DB.
	// If we were replaced during downtime, do NOT restore — stop services instead.
	if cm.db != nil {
		type countResult struct {
			Count int `db:"count"`
		}
		var results []countResult
		verifyQuery := `SELECT COUNT(*) as count FROM namespace_cluster_nodes WHERE namespace_cluster_id = ? AND node_id = ?`
		if err := cm.db.Query(ctx, &results, verifyQuery, state.ClusterID, cm.localNodeID); err == nil && len(results) > 0 {
			if results[0].Count == 0 {
				cm.logger.Warn("Node was replaced during downtime, stopping orphaned services instead of restoring",
					zap.String("namespace", state.NamespaceName),
					zap.String("cluster_id", state.ClusterID))
				cm.systemdSpawner.StopAll(ctx, state.NamespaceName)
				// Delete the stale cluster-state.json
				stateFilePath := filepath.Join(cm.baseDataDir, state.NamespaceName, "cluster-state.json")
				os.Remove(stateFilePath)
				return nil
			}
		}
	}

	pb := &state.LocalPorts
	localIP := state.LocalIP

	// 1. Restore RQLite
	// Check if RQLite systemd service is already running
	rqliteRunning, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(state.NamespaceName, systemd.ServiceTypeRQLite)
	if !rqliteRunning {
		// Check if RQLite data directory exists (has existing data)
		dataDir := filepath.Join(cm.baseDataDir, state.NamespaceName, "rqlite", cm.localNodeID)
		hasExistingData := false
		if _, err := os.Stat(filepath.Join(dataDir, "raft")); err == nil {
			hasExistingData = true
		}

		if hasExistingData {
			var peers []rqlite.RaftPeer
			for _, np := range state.AllNodes {
				raftAddr := fmt.Sprintf("%s:%d", np.InternalIP, np.RQLiteRaftPort)
				peers = append(peers, rqlite.RaftPeer{ID: raftAddr, Address: raftAddr, NonVoter: false})
			}
			if err := cm.writePeersJSON(dataDir, peers); err != nil {
				cm.logger.Error("Failed to write peers.json", zap.String("namespace", state.NamespaceName), zap.Error(err))
			}
		}

		var joinAddrs []string
		isLeader := false
		if !hasExistingData {
			sortedNodeIDs := make([]string, 0, len(state.AllNodes))
			for _, np := range state.AllNodes {
				sortedNodeIDs = append(sortedNodeIDs, np.NodeID)
			}
			sort.Strings(sortedNodeIDs)
			electedLeaderID := sortedNodeIDs[0]

			if cm.localNodeID == electedLeaderID {
				isLeader = true
			} else {
				for _, np := range state.AllNodes {
					if np.NodeID == electedLeaderID {
						joinAddrs = append(joinAddrs, fmt.Sprintf("%s:%d", np.InternalIP, np.RQLiteRaftPort))
						break
					}
				}
			}
		}

		rqliteCfg := rqlite.InstanceConfig{
			Namespace:      state.NamespaceName,
			NodeID:         cm.localNodeID,
			HTTPPort:       pb.RQLiteHTTPPort,
			RaftPort:       pb.RQLiteRaftPort,
			HTTPAdvAddress: fmt.Sprintf("%s:%d", localIP, pb.RQLiteHTTPPort),
			RaftAdvAddress: fmt.Sprintf("%s:%d", localIP, pb.RQLiteRaftPort),
			JoinAddresses:  joinAddrs,
			IsLeader:       isLeader,
		}
		if err := cm.spawnRQLiteWithSystemd(ctx, rqliteCfg); err != nil {
			cm.logger.Error("Failed to restore RQLite from state", zap.String("namespace", state.NamespaceName), zap.Error(err))
		} else {
			cm.logger.Info("Restored RQLite instance from state", zap.String("namespace", state.NamespaceName))
		}
	}

	// 2. Restore Olric
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", pb.OlricMemberlistPort), 2*time.Second)
	if err == nil {
		conn.Close()
	} else {
		var peers []string
		for _, np := range state.AllNodes {
			if np.NodeID != cm.localNodeID {
				peers = append(peers, fmt.Sprintf("%s:%d", np.InternalIP, np.OlricMemberlistPort))
			}
		}
		olricCfg := olric.InstanceConfig{
			Namespace:      state.NamespaceName,
			NodeID:         cm.localNodeID,
			HTTPPort:       pb.OlricHTTPPort,
			MemberlistPort: pb.OlricMemberlistPort,
			BindAddr:       localIP,
			AdvertiseAddr:  localIP,
			PeerAddresses:  peers,
		}
		if err := cm.spawnOlricWithSystemd(ctx, olricCfg); err != nil {
			cm.logger.Error("Failed to restore Olric from state", zap.String("namespace", state.NamespaceName), zap.Error(err))
		} else {
			cm.logger.Info("Restored Olric instance from state", zap.String("namespace", state.NamespaceName))
		}
	}

	// 3. Restore Gateway
	if state.HasGateway {
		// Re-advertise THIS node in the namespace gateway round-robin (`ns-<ns>`
		// and its `*.ns-<ns>` wildcard). Counterpart to EnsureTURNRecordForNode:
		// CreateNamespaceRecords only runs at provision time, so without this a
		// node whose record was purged while it was briefly non-active would never
		// re-advertise. Additive per-node upsert — never touches another node's
		// record, and never re-enables a record recovery deliberately disabled.
		// The public-IP lookup hits the MAIN rqlite, which this restore path
		// deliberately avoids depending on — so it can fail this early in boot.
		// That failure must be logged, not swallowed: it is correlated with the
		// very condition the purge reacts to (a node cut off from rqlite), and it
		// is the only signal that this node did not re-enter the round-robin.
		pip, perr := cm.getLocalNodePublicIP(ctx)
		switch {
		case perr != nil:
			cm.logger.Warn("Cannot re-advertise namespace host DNS record: public IP unavailable from dns_nodes (main rqlite not reachable this early in boot) — this node stays out of the ns-<ns> round-robin until the next restore",
				zap.String("namespace", state.NamespaceName), zap.Error(perr))
		case pip == "":
			cm.logger.Warn("Cannot re-advertise namespace host DNS record: this node has no public IP recorded in dns_nodes",
				zap.String("namespace", state.NamespaceName))
		default:
			if derr := cm.dnsManager.EnsureNamespaceHostRecordForNode(ctx, state.NamespaceName, pip); derr != nil {
				cm.logger.Warn("Ensure namespace host DNS record for node failed",
					zap.String("namespace", state.NamespaceName), zap.Error(derr))
			}
		}

		// Build the desired gateway config up front (incl. WebRTC resolved
		// from state→DB) so it drives BOTH the cold-spawn (gateway down)
		// and the warm-reconcile (gateway up but config drifted) paths.
		var olricServers []string // WireGuard IPs (Olric binds to the WG interface)
		for _, np := range state.AllNodes {
			olricServers = append(olricServers, fmt.Sprintf("%s:%d", np.InternalIP, np.OlricHTTPPort))
		}
		gwCfg := gateway.InstanceConfig{
			Namespace:             state.NamespaceName,
			NodeID:                cm.localNodeID,
			HTTPPort:              pb.GatewayHTTPPort,
			BaseDomain:            state.BaseDomain,
			RQLiteDSN:             fmt.Sprintf("http://localhost:%d", pb.RQLiteHTTPPort),
			GlobalRQLiteDSN:       cm.globalRQLiteDSN,
			OlricServers:          olricServers,
			OlricTimeout:          30 * time.Second,
			IPFSClusterAPIURL:     cm.ipfsClusterAPIURL,
			IPFSAPIURL:            cm.ipfsAPIURL,
			IPFSTimeout:           cm.ipfsTimeout,
			IPFSReplicationFactor: cm.ipfsReplicationFactor,
			SecretsEncryptionKey:  cm.secretsEncryptionKey,
		}

		// Resolve WebRTC config. DB-FIRST (source of truth for the CURRENT
		// secret); the local state cache is consulted only when the DB read
		// fails (bugboard #130 follow-up — see chooseRestoreWebRTC). Bugboard
		// #25 — the state file is NOT updated by EnableWebRTC, so a namespace
		// enabled AFTER its state file was written carries no SFU/TURN fields
		// here; reading the DB re-materializes them.
		wr := chooseRestoreWebRTC(
			state.HasSFU, state.SFUSignalingPort, state.TURNDomain, state.TURNSharedSecret, state.TURNStealthDomain,
			func() (turnSecret, turnDomain, stealthDomain string, sfuPort int, resolved bool) {
				// Retry the read on a transient error. A distant/slow node's
				// namespace rqlite may not be synced/readable yet at cold-start
				// converge time — without the retry the read fails once and the
				// gateway is written with TURN disabled (bugboard #130). The
				// secret IS in the DB; we just need the read to land once the
				// follower catches up (typically a few seconds). A genuine
				// decrypt failure (stale key) also errors here and will exhaust
				// the retries → unresolved → the caller preserves the running
				// config rather than blanking it.
				webrtcCfg, err := resolveWebRTCConfigWithRetry(
					webrtcResolveRetries, webrtcResolveRetryDelay, time.Sleep,
					func() (*WebRTCConfig, error) {
						return cm.GetWebRTCConfig(ctx, state.NamespaceName)
					})
				if err != nil {
					// Persistent error after retries (slow read that never
					// landed, or a decrypt failure). Do NOT swallow into
					// "disabled" — surface loudly and signal unresolved so the
					// caller preserves the running config (bugboard #130).
					cm.logger.Error("WebRTC TURN secret unresolvable on this node after retries — refusing to silently disable TURN; preserving existing gateway config. If this is a cluster-secret rotation, regenerate with `orama namespace disable webrtc` then `orama namespace enable webrtc`.",
						zap.String("namespace", state.NamespaceName),
						zap.String("node_id", cm.localNodeID),
						zap.Int("attempts", webrtcResolveRetries),
						zap.Error(err))
					return "", "", "", 0, false
				}
				if webrtcCfg == nil {
					// Resolved cleanly: the namespace genuinely has no WebRTC.
					return "", "", "", 0, true
				}
				// TURN is namespace-wide; SFU port is per-node and may be
				// absent on a gateway-only (non-SFU) node — that's fine,
				// the gateway still serves TURN credentials.
				sfu := 0
				if sfuBlock, serr := cm.webrtcPortAllocator.GetSFUPorts(ctx, state.ClusterID, cm.localNodeID); serr == nil && sfuBlock != nil {
					sfu = sfuBlock.SFUSignalingPort
				}
				return webrtcCfg.TURNSharedSecret,
					fmt.Sprintf("turn.ns-%s.%s", state.NamespaceName, cm.baseDomain),
					cm.stealthDomainFor(state.NamespaceName, webrtcCfg),
					sfu, true
			},
		)
		if wr.enabled {
			// WebRTCEnabled is the legacy flag (ignored by the route gate
			// now — bugboard #25/#411); set it to SFU presence for
			// config-shape consistency with how EnableWebRTC writes nodes.
			gwCfg.WebRTCEnabled = wr.sfuPort > 0
			gwCfg.SFUPort = wr.sfuPort
			gwCfg.TURNDomain = wr.turnDomain
			gwCfg.TURNSecret = wr.turnSecret
			gwCfg.TURNStealthDomain = wr.stealthDomain

			// Cache the resolved secret into THIS node's local state so that if
			// the NEXT cold start can't read the namespace rqlite (a distant/
			// slow node whose follower hasn't synced), chooseRestoreWebRTC can
			// fall back to this on-disk secret instead of coming up with TURN
			// disabled (bugboard #130). The cache is a FALLBACK — DB-first
			// resolution still prefers the live DB secret whenever it's
			// readable, so this cached value can never pin the node to a stale
			// secret. Each node self-heals its own cache on a successful
			// resolve; nothing is sent cross-node.
			if applyResolvedWebRTCToState(state, wr) {
				if err := cm.saveLocalState(state); err != nil {
					cm.logger.Warn("Failed to cache resolved WebRTC config to local state (cold start may fall back to the DB read next boot)",
						zap.String("namespace", state.NamespaceName), zap.Error(err))
				} else {
					cm.logger.Info("Cached resolved WebRTC config to local state for cold-start resilience (bugboard #130)",
						zap.String("namespace", state.NamespaceName))
				}
			}
		} else if !wr.unresolved {
			// The DB read RESOLVED that this namespace has NO WebRTC (disabled).
			// Clear any stale cached secret from local state so a future cold
			// start that hits a transient DB error can't fall back to it and
			// resurrect TURN for a disabled namespace — the hole being: a node
			// that was offline during DisableWebRTC never received the cleared
			// state push and would otherwise keep serving the old secret. Only
			// do this on a RESOLVED-disabled read, NEVER on an unresolved
			// (DB-error) one — there the cache IS the fallback and must survive.
			if applyResolvedWebRTCToState(state, restoreWebRTC{}) {
				if err := cm.saveLocalState(state); err != nil {
					cm.logger.Warn("Failed to clear stale cached WebRTC secret from local state after DB reported the namespace disabled",
						zap.String("namespace", state.NamespaceName), zap.Error(err))
				} else {
					cm.logger.Info("Cleared stale cached WebRTC secret from local state (namespace disabled in DB)",
						zap.String("namespace", state.NamespaceName))
				}
			}
		}

		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/v1/health", pb.GatewayHTTPPort))
		if err == nil {
			resp.Body.Close()
			switch {
			case wr.unresolved:
				// Bugboard #130 guard: the WebRTC secret could not be resolved
				// (DB/decrypt error, logged above). The gateway is already up
				// and may be serving TURN from a valid on-disk secret — do NOT
				// reconcile it to the empty/disabled block we'd otherwise
				// build, which would kill turn.credentials on this node. Leave
				// the running config untouched; the operator regenerates the
				// secret.
				//
				// Note: this also defers ReconcileGateway's #837
				// secrets-encryption-key reconcile for this one converge pass.
				// That is acceptable — the operator action that fixes the
				// unresolved TURN secret (regenerate + restart) re-runs the
				// full reconcile, and pre-fix this path would have corrupted
				// the WebRTC block anyway.
				cm.logger.Error("Gateway up but WebRTC secret unresolved — skipping reconcile to avoid disabling TURN on the running config (bugboard #130)",
					zap.String("namespace", state.NamespaceName))
			default:
				// Gateway is already up. Reconcile config drift (bugboard #25 —
				// the WARM case): if the running gateway's on-disk config has a
				// WebRTC block that differs from the desired (e.g. it lost the
				// block on a prior restart where it stayed healthy and the
				// cold-spawn path below never ran), rewrite the config +
				// restart. ReconcileGateway is a no-op when the on-disk block
				// already matches, so this does NOT cause a restart loop.
				if rerr := cm.systemdSpawner.ReconcileGateway(ctx, state.NamespaceName, cm.localNodeID, gwCfg); rerr != nil {
					cm.logger.Warn("Gateway WebRTC reconcile failed (leaving running config as-is)",
						zap.String("namespace", state.NamespaceName), zap.Error(rerr))
				}
			}
		} else {
			// Gateway is down → cold spawn. We must bring a gateway up
			// regardless (the namespace needs one); but if the WebRTC secret
			// was unresolved we can't write a working TURN block, so warn
			// loudly that TURN is degraded on this node until the secret is
			// regenerated (bugboard #130).
			switch {
			case wr.unresolved:
				cm.logger.Error("Cold-spawning gateway with TURN UNAVAILABLE — WebRTC secret unresolved on this node; turn.credentials will return namespace_not_configured until it is regenerated (`orama namespace disable webrtc` then `orama namespace enable webrtc`)",
					zap.String("namespace", state.NamespaceName))
			case wr.enabled && !state.HasSFU:
				cm.logger.Info("Re-materialized WebRTC gateway config from DB (state file was stale)",
					zap.String("namespace", state.NamespaceName),
					zap.Int("sfu_port", wr.sfuPort))
			}
			if err := cm.spawnGatewayWithSystemd(ctx, gwCfg); err != nil {
				cm.logger.Error("Failed to restore Gateway from state", zap.String("namespace", state.NamespaceName), zap.Error(err))
			} else {
				cm.logger.Info("Restored Gateway instance from state", zap.String("namespace", state.NamespaceName))
			}
		}
	}

	// bugboard #158: keep the TURN server's auth_secret in sync with the current
	// namespace DB secret, independently of the state file. Some namespaces'
	// cluster-state.json does not persist TURN fields (only has_gateway), so the
	// state-gated TURN restore below never fires for them — yet the gateway still
	// re-syncs its turn_secret to the DB secret on every boot (ReconcileGateway).
	// If TURN keeps a stale secret the gateway mints credentials TURN rejects and
	// every call fails auth (Allocate 400 → zero relay candidates). ReconcileTURN
	// also switches the TURNS cert from the self-signed fallback to the wildcard
	// once it's available (browsers reject self-signed). Reads the on-disk turn
	// config and patches only drifted fields; a no-op when there is no TURN on
	// this node or it is already in sync.
	if turnWebRTCCfg, werr := cm.GetWebRTCConfig(ctx, state.NamespaceName); werr == nil && turnWebRTCCfg != nil {
		// Before reconciling THIS node's services, bring the cluster-wide role
		// assignments back in line with live membership (bugboard #161): node
		// replacement migrates gateway/olric/rqlite but leaves TURN/SFU
		// allocations on the departed node, so a namespace silently ends up with
		// one live relay instead of two and the replacement node holds no role at
		// all. Self-elects a single coordinator, and is a no-op once healthy.
		if aerr := cm.ReconcileWebRTCAllocations(ctx, state.ClusterID, state.NamespaceName, turnWebRTCCfg.TURNNodeCount); aerr != nil {
			cm.logger.Warn("WebRTC allocation reconcile failed (leaving assignments as-is)",
				zap.String("namespace", state.NamespaceName), zap.Error(aerr))
		}

		if rerr := cm.systemdSpawner.ReconcileTURN(ctx, state.NamespaceName, cm.localNodeID, turnWebRTCCfg.TURNSharedSecret); rerr != nil {
			cm.logger.Warn("TURN reconcile failed (leaving running config as-is)",
				zap.String("namespace", state.NamespaceName), zap.Error(rerr))
		}
		// If this node runs TURN, make sure its TURN/stealth A record exists.
		// The #158 sweep only DELETES records for inactive nodes; a node whose
		// record was purged while briefly down (e.g. mid-deploy) would otherwise
		// never re-advertise, leaving the turn domain empty. Additive per-node
		// upsert (never touches other nodes' records).
		// Gate on the ALLOCATION and on the unit actually serving — NOT on the
		// config file. StopService leaves the config behind, so a file-existence
		// gate re-advertises a node that lost the TURN role on its next boot,
		// pointing clients at a relay that will never start (the #161 symptom).
		bootTurnBlk, bootTurnErr := cm.webrtcPortAllocator.GetTURNPorts(ctx, state.ClusterID, cm.localNodeID)
		bootTurnUp, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(state.NamespaceName, systemd.ServiceTypeTURN)
		if bootTurnErr == nil && bootTurnBlk != nil && bootTurnUp {
			pip, perr := cm.getLocalNodePublicIP(ctx)
			switch {
			case perr != nil:
				cm.logger.Warn("Cannot re-advertise TURN DNS record: public IP unavailable from dns_nodes (main rqlite not reachable this early in boot) — this TURN node stays unadvertised until the next restore",
					zap.String("namespace", state.NamespaceName), zap.Error(perr))
			case pip == "":
				cm.logger.Warn("Cannot re-advertise TURN DNS record: this node has no public IP recorded in dns_nodes",
					zap.String("namespace", state.NamespaceName))
			default:
				if derr := cm.dnsManager.EnsureTURNRecordForNode(ctx, state.NamespaceName, pip, cm.stealthDomainFor(state.NamespaceName, turnWebRTCCfg)); derr != nil {
					cm.logger.Warn("Ensure TURN DNS record for node failed",
						zap.String("namespace", state.NamespaceName), zap.Error(derr))
				}
			}
		}
	}

	// 4. Restore TURN — gated on this node's DB ALLOCATION, not the state file.
	// Same reasoning as the SFU gate below (bugboard #161): after a node
	// replacement the TURN role moves, and cluster-state.json cannot know. The
	// relay port range is read from the allocation too, so a node that gains the
	// role spawns with real ports instead of the state file's zeros.
	turnAlloc, turnAllocErr := cm.webrtcPortAllocator.GetTURNPorts(ctx, state.ClusterID, cm.localNodeID)
	turnAllocated := turnAllocErr == nil && turnPortBlockSpawnable(turnAlloc)
	if turnAllocated {
		state.TURNRelayPortStart = turnAlloc.TURNRelayPortStart
		state.TURNRelayPortEnd = turnAlloc.TURNRelayPortEnd
	}
	if turnAllocated {
		// NOTE (bugboard #846): the TURN relay firewall rules are (re)applied by
		// the root-level Phase 6b firewall setup, which is TURN-aware. orama-node
		// runs as a NON-root user and cannot modify ufw, so the firewall reconcile
		// deliberately does NOT live here — it would just spawn a doomed `ufw`
		// call. The restore below only re-spawns the TURN process itself.
		turnRunning, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(state.NamespaceName, systemd.ServiceTypeTURN)
		if !turnRunning {
			// TURN config needs the shared secret from DB — we can't persist it to disk state.
			// If DB is available, fetch it; otherwise skip TURN restore (it will come back when DB is ready).
			webrtcCfg, err := cm.GetWebRTCConfig(ctx, state.NamespaceName)
			if err == nil && webrtcCfg != nil {
				// Resolve this node's public IP. Passing an empty PublicIP here
				// (the old behavior) produced a TURN config that crash-loops on
				// "public_ip must not be empty" — killing TURN on the node and
				// preventing AddWebRTCRules from ever running (bugboard #846).
				publicIP, ipErr := cm.getLocalNodePublicIP(ctx)
				if ipErr != nil {
					cm.logger.Error("Skipping TURN restore: cannot resolve local node public IP (an empty public_ip crash-loops the TURN server)",
						zap.String("namespace", state.NamespaceName), zap.Error(ipErr))
				} else {
					turnCfg := TURNInstanceConfig{
						Namespace:       state.NamespaceName,
						NodeID:          cm.localNodeID,
						ListenAddr:      fmt.Sprintf("0.0.0.0:%d", turnAlloc.TURNListenPort),
						TURNSListenAddr: fmt.Sprintf("0.0.0.0:%d", turnAlloc.TURNTLSPort),
						PublicIP:        publicIP,
						Realm:           cm.baseDomain,
						AuthSecret:      webrtcCfg.TURNSharedSecret,
						RelayPortStart:  state.TURNRelayPortStart,
						RelayPortEnd:    state.TURNRelayPortEnd,
						TURNDomain:      fmt.Sprintf("turn.ns-%s.%s", state.NamespaceName, cm.baseDomain),
						StealthDomain:   cm.stealthDomainFor(state.NamespaceName, webrtcCfg),
					}
					if err := cm.systemdSpawner.SpawnTURN(ctx, state.NamespaceName, cm.localNodeID, turnCfg); err != nil {
						cm.logger.Error("Failed to restore TURN from state", zap.String("namespace", state.NamespaceName), zap.Error(err))
					} else {
						cm.logger.Info("Restored TURN instance from state", zap.String("namespace", state.NamespaceName), zap.String("public_ip", publicIP))
					}
				}
			} else {
				cm.logger.Warn("Skipping TURN restore: WebRTC config not available from DB",
					zap.String("namespace", state.NamespaceName))
			}
		}
	}

	// 5. Restore SFU — gated on this node's DB ALLOCATION, not the state file.
	//
	// state.HasSFU comes from cluster-state.json, which restoreClusterOnNode
	// rewrites as gateway-only, so it is routinely absent even on a node that
	// genuinely holds an SFU role. Worse, after a node replacement the roles move
	// (bugboard #161) and the state file cannot know: the replacement node has no
	// has_sfu flag, so it would never spawn the role it was just assigned, while
	// the departed node's file still claims one. webrtc_port_allocations is the
	// authority for who runs what.
	sfuAllocated := false
	if blk, aerr := cm.webrtcPortAllocator.GetSFUPorts(ctx, state.ClusterID, cm.localNodeID); aerr == nil {
		sfuAllocated = sfuPortBlockSpawnable(blk)
	}
	if sfuAllocated {
		sfuRunning, _ := cm.systemdSpawner.systemdMgr.IsServiceActive(state.NamespaceName, systemd.ServiceTypeSFU)
		if !sfuRunning {
			webrtcCfg, err := cm.GetWebRTCConfig(ctx, state.NamespaceName)
			if err == nil && webrtcCfg != nil {
				// Source SFU ports from the DB port allocator, NOT the local
				// state file. restoreClusterOnNode persists a gateway-only
				// ClusterLocalState (no SFU media ports), so state.SFUMediaPort*
				// are 0 here — spawning pion with a 0 media range makes it fail
				// to bind and the systemd unit crash-loops. The DB is
				// authoritative and matches how EnableWebRTC first spawned the
				// SFU (same root class as the TURN #158 config-from-stale-state
				// bug: config must come from the DB, not the incomplete state).
				sfuBlock, serr := cm.webrtcPortAllocator.GetSFUPorts(ctx, state.ClusterID, cm.localNodeID)
				if !sfuPortBlockSpawnable(sfuBlock) {
					cm.logger.Error("Skipping SFU restore: SFU port allocation missing or invalid in DB — refusing to spawn with a zero media range",
						zap.String("namespace", state.NamespaceName),
						zap.String("node_id", cm.localNodeID),
						zap.Error(serr))
				} else {
					turnDomain := fmt.Sprintf("turn.ns-%s.%s", state.NamespaceName, cm.baseDomain)
					sfuCfg := SFUInstanceConfig{
						Namespace:      state.NamespaceName,
						NodeID:         cm.localNodeID,
						ListenAddr:     fmt.Sprintf("%s:%d", localIP, sfuBlock.SFUSignalingPort),
						MediaPortStart: sfuBlock.SFUMediaPortStart,
						MediaPortEnd:   sfuBlock.SFUMediaPortEnd,
						TURNServers: []sfu.TURNServerConfig{
							{Host: turnDomain, Port: TURNDefaultPort, Secure: false},
							{Host: turnDomain, Port: TURNSPort, Secure: true},
						},
						TURNSecret:  webrtcCfg.TURNSharedSecret,
						TURNCredTTL: webrtcCfg.TURNCredentialTTL,
						RQLiteDSN:   fmt.Sprintf("http://localhost:%d", pb.RQLiteHTTPPort),
					}
					if err := cm.systemdSpawner.SpawnSFU(ctx, state.NamespaceName, cm.localNodeID, sfuCfg); err != nil {
						cm.logger.Error("Failed to restore SFU", zap.String("namespace", state.NamespaceName), zap.Error(err))
					} else {
						cm.logger.Info("Restored SFU instance from DB port allocation",
							zap.String("namespace", state.NamespaceName),
							zap.Int("signaling_port", sfuBlock.SFUSignalingPort),
							zap.Int("media_start", sfuBlock.SFUMediaPortStart),
							zap.Int("media_end", sfuBlock.SFUMediaPortEnd))
					}
				}
			} else {
				cm.logger.Warn("Skipping SFU restore: WebRTC config not available from DB",
					zap.String("namespace", state.NamespaceName))
			}
		}
	}

	return nil
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
