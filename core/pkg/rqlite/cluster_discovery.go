package rqlite

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
	"github.com/libp2p/go-libp2p/core/host"
	"go.uber.org/zap"
)

// ClusterDiscoveryService bridges LibP2P discovery with RQLite cluster management
type ClusterDiscoveryService struct {
	host           host.Host
	discoveryMgr   *discovery.Manager
	rqliteManager  *RQLiteManager
	nodeID         string
	nodeType       string
	raftAddress    string
	httpAddress    string
	wireGuardIP    string // extracted from raftAddress (IP component)
	dataDir        string
	minClusterSize int // Minimum cluster size required

	// Lifecycle manager for this node's state machine
	lifecycle *lifecycle.Manager

	knownPeers      map[string]*discovery.RQLiteNodeMetadata // NodeID -> Metadata
	peerHealth      map[string]*PeerHealth                   // NodeID -> Health
	lastUpdate      time.Time
	updateInterval  time.Duration // 30 seconds
	inactivityLimit time.Duration // 24 hours

	logger  *zap.Logger
	mu      sync.RWMutex
	cancel  context.CancelFunc
	started bool
}

// NewClusterDiscoveryService creates a new cluster discovery service
func NewClusterDiscoveryService(
	h host.Host,
	discoveryMgr *discovery.Manager,
	rqliteManager *RQLiteManager,
	nodeID string,
	nodeType string,
	raftAddress string,
	httpAddress string,
	dataDir string,
	lm *lifecycle.Manager,
	logger *zap.Logger,
) *ClusterDiscoveryService {
	minClusterSize := 1
	if rqliteManager != nil && rqliteManager.config != nil {
		minClusterSize = rqliteManager.config.MinClusterSize
	}

	// Extract WireGuard IP from the raft address (e.g., "10.0.0.1" from "10.0.0.1:7001")
	wgIP := ""
	if host, _, err := net.SplitHostPort(raftAddress); err == nil {
		wgIP = host
	}

	return &ClusterDiscoveryService{
		host:            h,
		discoveryMgr:    discoveryMgr,
		rqliteManager:   rqliteManager,
		nodeID:          nodeID,
		nodeType:        nodeType,
		raftAddress:     raftAddress,
		httpAddress:     httpAddress,
		wireGuardIP:     wgIP,
		dataDir:         dataDir,
		minClusterSize:  minClusterSize,
		lifecycle:       lm,
		knownPeers:      make(map[string]*discovery.RQLiteNodeMetadata),
		peerHealth:      make(map[string]*PeerHealth),
		updateInterval:  30 * time.Second,
		inactivityLimit: 24 * time.Hour,
		logger:          logger.With(zap.String("component", "cluster-discovery")),
	}
}

// Start begins the cluster discovery service
func (c *ClusterDiscoveryService) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return fmt.Errorf("cluster discovery already started")
	}
	c.started = true
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.logger.Info("Starting cluster discovery service",
		zap.String("raft_address", c.raftAddress),
		zap.String("node_type", c.nodeType),
		zap.String("http_address", c.httpAddress),
		zap.String("data_dir", c.dataDir),
		zap.Duration("update_interval", c.updateInterval),
		zap.Duration("inactivity_limit", c.inactivityLimit))

	// Start periodic sync in background
	go c.periodicSync(ctx)

	// Start periodic cleanup in background
	go c.periodicCleanup(ctx)

	c.logger.Info("Cluster discovery goroutines started")

	return nil
}

// Stop stops the cluster discovery service
func (c *ClusterDiscoveryService) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return
	}

	if c.cancel != nil {
		c.cancel()
	}
	c.started = false

	c.logger.Info("Cluster discovery service stopped")
}

// Lifecycle returns the node's lifecycle manager.
func (c *ClusterDiscoveryService) Lifecycle() *lifecycle.Manager {
	return c.lifecycle
}

// GetPeerLifecycleState returns the lifecycle state and last-seen time for a
// peer identified by its RQLite node ID (raft address). This method implements
// the MetadataReader interface used by the health monitor.
func (c *ClusterDiscoveryService) GetPeerLifecycleState(nodeID string) (state string, lastSeen time.Time, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peer, ok := c.knownPeers[nodeID]
	if !ok {
		return "", time.Time{}, false
	}
	return peer.EffectiveLifecycleState(), peer.LastSeen, true
}

// IsVoter returns true if the given raft address should be a voter
// in the default cluster based on the current known peers.
func (c *ClusterDiscoveryService) IsVoter(raftAddress string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.IsVoterLocked(raftAddress)
}

// periodicSync runs periodic cluster membership synchronization
func (c *ClusterDiscoveryService) periodicSync(ctx context.Context) {
	c.logger.Debug("periodicSync goroutine started, waiting for RQLite readiness")

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	// Wait for first ticker interval before syncing (RQLite needs time to start)
	for {
		select {
		case <-ctx.Done():
			c.logger.Debug("periodicSync goroutine stopping")
			return
		case <-ticker.C:
			c.updateClusterMembership()
		}
	}
}

// periodicCleanup runs periodic cleanup of inactive nodes
func (c *ClusterDiscoveryService) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.removeInactivePeers()
		}
	}
}
