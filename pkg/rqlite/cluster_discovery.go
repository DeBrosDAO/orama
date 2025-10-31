package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// ClusterDiscoveryService bridges LibP2P discovery with RQLite cluster management
type ClusterDiscoveryService struct {
	host          host.Host
	discoveryMgr  *discovery.Manager
	rqliteManager *RQLiteManager
	nodeID        string
	nodeType      string
	raftAddress   string
	httpAddress   string
	dataDir       string

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
	logger *zap.Logger,
) *ClusterDiscoveryService {
	return &ClusterDiscoveryService{
		host:            h,
		discoveryMgr:    discoveryMgr,
		rqliteManager:   rqliteManager,
		nodeID:          nodeID,
		nodeType:        nodeType,
		raftAddress:     raftAddress,
		httpAddress:     httpAddress,
		dataDir:         dataDir,
		knownPeers:      make(map[string]*discovery.RQLiteNodeMetadata),
		peerHealth:      make(map[string]*PeerHealth),
		updateInterval:  30 * time.Second,
		inactivityLimit: 24 * time.Hour,
		logger:          logger,
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
		zap.String("node_id", c.nodeID),
		zap.String("node_type", c.nodeType),
		zap.String("raft_address", c.raftAddress),
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

// periodicSync runs periodic cluster membership synchronization
func (c *ClusterDiscoveryService) periodicSync(ctx context.Context) {
	c.logger.Info("periodicSync goroutine started, doing initial sync immediately")

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	// Do initial sync immediately
	c.logger.Info("Running initial cluster membership sync")
	c.updateClusterMembership()
	c.logger.Info("Initial cluster membership sync completed")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("periodicSync goroutine stopping")
			return
		case <-ticker.C:
			c.logger.Debug("Running periodic cluster membership sync")
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

// collectPeerMetadata collects RQLite metadata from LibP2P peers
func (c *ClusterDiscoveryService) collectPeerMetadata() []*discovery.RQLiteNodeMetadata {
	connectedPeers := c.host.Network().Peers()
	var metadata []*discovery.RQLiteNodeMetadata

	c.logger.Debug("Collecting peer metadata from LibP2P",
		zap.Int("connected_libp2p_peers", len(connectedPeers)))

	// Add ourselves
	ourMetadata := &discovery.RQLiteNodeMetadata{
		NodeID:         c.nodeID,
		RaftAddress:    c.raftAddress,
		HTTPAddress:    c.httpAddress,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
	}
	metadata = append(metadata, ourMetadata)

	// Query connected peers for their RQLite metadata
	// For now, we'll use a simple approach - store metadata in peer metadata store
	// In a full implementation, this would use a custom protocol to exchange RQLite metadata
	for _, peerID := range connectedPeers {
		// Try to get stored metadata from peerstore
		// This would be populated by a peer exchange protocol
		if val, err := c.host.Peerstore().Get(peerID, "rqlite_metadata"); err == nil {
			if jsonData, ok := val.([]byte); ok {
				var peerMeta discovery.RQLiteNodeMetadata
				if err := json.Unmarshal(jsonData, &peerMeta); err == nil {
					peerMeta.LastSeen = time.Now()
					metadata = append(metadata, &peerMeta)
				}
			}
		}
	}

	return metadata
}

// updateClusterMembership updates the cluster membership based on discovered peers
func (c *ClusterDiscoveryService) updateClusterMembership() {
	metadata := c.collectPeerMetadata()

	c.logger.Debug("Collected peer metadata",
		zap.Int("metadata_count", len(metadata)))

	c.mu.Lock()
	defer c.mu.Unlock()

	// Track changes
	added := []string{}
	updated := []string{}

	// Update known peers
	for _, meta := range metadata {
		if existing, ok := c.knownPeers[meta.NodeID]; ok {
			// Update existing peer
			if existing.RaftLogIndex != meta.RaftLogIndex ||
				existing.HTTPAddress != meta.HTTPAddress ||
				existing.RaftAddress != meta.RaftAddress {
				updated = append(updated, meta.NodeID)
			}
		} else {
			// New peer discovered
			added = append(added, meta.NodeID)
			c.logger.Info("Node added to cluster",
				zap.String("node_id", meta.NodeID),
				zap.String("raft_address", meta.RaftAddress),
				zap.String("node_type", meta.NodeType),
				zap.Uint64("log_index", meta.RaftLogIndex))
		}

		c.knownPeers[meta.NodeID] = meta

		// Update health tracking
		if _, ok := c.peerHealth[meta.NodeID]; !ok {
			c.peerHealth[meta.NodeID] = &PeerHealth{
				LastSeen:       time.Now(),
				LastSuccessful: time.Now(),
				Status:         "active",
			}
		} else {
			c.peerHealth[meta.NodeID].LastSeen = time.Now()
			c.peerHealth[meta.NodeID].Status = "active"
			c.peerHealth[meta.NodeID].FailureCount = 0
		}
	}

	// Generate and write peers.json if there are changes OR if this is the first time
	shouldWrite := len(added) > 0 || len(updated) > 0 || c.lastUpdate.IsZero()

	if shouldWrite {
		c.logger.Info("Updating peers.json",
			zap.Int("added", len(added)),
			zap.Int("updated", len(updated)),
			zap.Int("total_peers", len(c.knownPeers)),
			zap.Bool("first_run", c.lastUpdate.IsZero()))

		// Get peers JSON while holding the lock
		peers := c.getPeersJSONUnlocked()

		// Release lock before file I/O
		c.mu.Unlock()

		// Write without holding lock
		if err := c.writePeersJSONWithData(peers); err != nil {
			c.logger.Error("CRITICAL: Failed to write peers.json",
				zap.Error(err),
				zap.String("data_dir", c.dataDir),
				zap.Int("peer_count", len(peers)))
		} else {
			c.logger.Info("Successfully wrote peers.json",
				zap.Int("peer_count", len(peers)))
		}

		// Re-acquire lock to update lastUpdate
		c.mu.Lock()
	} else {
		c.logger.Debug("No changes to cluster membership",
			zap.Int("total_peers", len(c.knownPeers)))
	}

	c.lastUpdate = time.Now()
}

// removeInactivePeers removes peers that haven't been seen for longer than the inactivity limit
func (c *ClusterDiscoveryService) removeInactivePeers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := []string{}

	for nodeID, health := range c.peerHealth {
		inactiveDuration := now.Sub(health.LastSeen)

		if inactiveDuration > c.inactivityLimit {
			// Mark as inactive and remove
			c.logger.Warn("Node removed from cluster",
				zap.String("node_id", nodeID),
				zap.String("reason", "inactive"),
				zap.Duration("inactive_duration", inactiveDuration))

			delete(c.knownPeers, nodeID)
			delete(c.peerHealth, nodeID)
			removed = append(removed, nodeID)
		}
	}

	// Regenerate peers.json if any peers were removed
	if len(removed) > 0 {
		c.logger.Info("Removed inactive nodes, regenerating peers.json",
			zap.Int("removed", len(removed)),
			zap.Strings("node_ids", removed))

		if err := c.writePeersJSON(); err != nil {
			c.logger.Error("Failed to write peers.json after cleanup", zap.Error(err))
		}
	}
}

// getPeersJSON generates the peers.json structure from active peers (acquires lock)
func (c *ClusterDiscoveryService) getPeersJSON() []map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getPeersJSONUnlocked()
}

// getPeersJSONUnlocked generates the peers.json structure (must be called with lock held)
func (c *ClusterDiscoveryService) getPeersJSONUnlocked() []map[string]interface{} {
	peers := make([]map[string]interface{}, 0, len(c.knownPeers))

	for _, peer := range c.knownPeers {
		peerEntry := map[string]interface{}{
			"id":        peer.NodeID,
			"address":   peer.RaftAddress,
			"non_voter": false,
		}
		peers = append(peers, peerEntry)
	}

	return peers
}

// writePeersJSON atomically writes the peers.json file (acquires lock)
func (c *ClusterDiscoveryService) writePeersJSON() error {
	c.mu.RLock()
	peers := c.getPeersJSONUnlocked()
	c.mu.RUnlock()

	return c.writePeersJSONWithData(peers)
}

// writePeersJSONWithData writes the peers.json file with provided data (no lock needed)
func (c *ClusterDiscoveryService) writePeersJSONWithData(peers []map[string]interface{}) error {
	c.logger.Info("writePeersJSON: Starting",
		zap.String("data_dir", c.dataDir))

	c.logger.Info("writePeersJSON: Got peers JSON",
		zap.Int("peer_count", len(peers)))

	// Expand ~ in data directory path
	dataDir := os.ExpandEnv(c.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	c.logger.Info("writePeersJSON: Expanded data dir",
		zap.String("expanded_path", dataDir))

	// Get the RQLite raft directory
	rqliteDir := filepath.Join(dataDir, "rqlite", "raft")

	c.logger.Info("writePeersJSON: Creating raft directory",
		zap.String("raft_dir", rqliteDir))

	if err := os.MkdirAll(rqliteDir, 0755); err != nil {
		return fmt.Errorf("failed to create raft directory %s: %w", rqliteDir, err)
	}

	peersFile := filepath.Join(rqliteDir, "peers.json")
	backupFile := filepath.Join(rqliteDir, "peers.json.backup")

	c.logger.Info("writePeersJSON: File paths",
		zap.String("peers_file", peersFile),
		zap.String("backup_file", backupFile))

	// Backup existing peers.json if it exists
	if _, err := os.Stat(peersFile); err == nil {
		c.logger.Info("writePeersJSON: Backing up existing peers.json")
		data, err := os.ReadFile(peersFile)
		if err == nil {
			_ = os.WriteFile(backupFile, data, 0644)
		}
	}

	// Marshal to JSON
	c.logger.Info("writePeersJSON: Marshaling to JSON")
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal peers.json: %w", err)
	}

	c.logger.Info("writePeersJSON: JSON marshaled",
		zap.Int("data_size", len(data)))

	// Write atomically using temp file + rename
	tempFile := peersFile + ".tmp"

	c.logger.Info("writePeersJSON: Writing temp file",
		zap.String("temp_file", tempFile))

	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp peers.json %s: %w", tempFile, err)
	}

	c.logger.Info("writePeersJSON: Renaming temp file to final")

	if err := os.Rename(tempFile, peersFile); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tempFile, peersFile, err)
	}

	nodeIDs := make([]string, 0, len(peers))
	for _, p := range peers {
		if id, ok := p["id"].(string); ok {
			nodeIDs = append(nodeIDs, id)
		}
	}

	c.logger.Info("peers.json successfully written!",
		zap.String("file", peersFile),
		zap.Int("node_count", len(peers)),
		zap.Strings("node_ids", nodeIDs))

	return nil
}

// GetActivePeers returns a list of active peers (not including self)
func (c *ClusterDiscoveryService) GetActivePeers() []*discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := make([]*discovery.RQLiteNodeMetadata, 0, len(c.knownPeers))
	for _, peer := range c.knownPeers {
		// Skip self
		if peer.NodeID == c.nodeID {
			continue
		}
		peers = append(peers, peer)
	}

	return peers
}

// GetNodeWithHighestLogIndex returns the node with the highest Raft log index
func (c *ClusterDiscoveryService) GetNodeWithHighestLogIndex() *discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var highest *discovery.RQLiteNodeMetadata
	var maxIndex uint64 = 0

	for _, peer := range c.knownPeers {
		// Skip self
		if peer.NodeID == c.nodeID {
			continue
		}

		if peer.RaftLogIndex > maxIndex {
			maxIndex = peer.RaftLogIndex
			highest = peer
		}
	}

	return highest
}

// HasRecentPeersJSON checks if peers.json was recently updated
func (c *ClusterDiscoveryService) HasRecentPeersJSON() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Consider recent if updated in last 5 minutes
	return time.Since(c.lastUpdate) < 5*time.Minute
}

// FindJoinTargets discovers join targets via LibP2P, prioritizing bootstrap nodes
func (c *ClusterDiscoveryService) FindJoinTargets() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	targets := []string{}

	// Prioritize bootstrap nodes
	for _, peer := range c.knownPeers {
		if peer.NodeType == "bootstrap" {
			targets = append(targets, peer.RaftAddress)
		}
	}

	// Add other nodes as fallback
	for _, peer := range c.knownPeers {
		if peer.NodeType != "bootstrap" {
			targets = append(targets, peer.RaftAddress)
		}
	}

	return targets
}

// WaitForDiscoverySettling waits for LibP2P discovery to settle (used on concurrent startup)
func (c *ClusterDiscoveryService) WaitForDiscoverySettling(ctx context.Context) {
	settleDuration := 60 * time.Second
	c.logger.Info("Waiting for discovery to settle",
		zap.Duration("duration", settleDuration))

	select {
	case <-ctx.Done():
		return
	case <-time.After(settleDuration):
	}

	// Collect final peer list
	c.updateClusterMembership()

	c.mu.RLock()
	peerCount := len(c.knownPeers)
	c.mu.RUnlock()

	c.logger.Info("Discovery settled",
		zap.Int("peer_count", peerCount))
}

// UpdateOwnMetadata updates our own RQLite metadata in the peerstore
func (c *ClusterDiscoveryService) UpdateOwnMetadata() {
	c.logger.Info("Updating own RQLite metadata for peer exchange",
		zap.String("node_id", c.nodeID))

	metadata := &discovery.RQLiteNodeMetadata{
		NodeID:         c.nodeID,
		RaftAddress:    c.raftAddress,
		HTTPAddress:    c.httpAddress,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
	}

	c.logger.Info("Created metadata struct",
		zap.String("node_id", metadata.NodeID),
		zap.String("raft_address", metadata.RaftAddress),
		zap.String("http_address", metadata.HTTPAddress),
		zap.Uint64("log_index", metadata.RaftLogIndex))

	// Store in our own peerstore for peer exchange
	data, err := json.Marshal(metadata)
	if err != nil {
		c.logger.Error("Failed to marshal own metadata", zap.Error(err))
		return
	}

	if err := c.host.Peerstore().Put(c.host.ID(), "rqlite_metadata", data); err != nil {
		c.logger.Error("Failed to store own metadata", zap.Error(err))
		return
	}

	c.logger.Info("Successfully stored own RQLite metadata in peerstore",
		zap.String("node_id", c.nodeID),
		zap.Uint64("log_index", metadata.RaftLogIndex))
}

// StoreRemotePeerMetadata stores metadata received from a remote peer
func (c *ClusterDiscoveryService) StoreRemotePeerMetadata(peerID peer.ID, metadata *discovery.RQLiteNodeMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := c.host.Peerstore().Put(peerID, "rqlite_metadata", data); err != nil {
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	c.logger.Debug("Stored remote peer metadata",
		zap.String("peer_id", peerID.String()[:8]+"..."),
		zap.String("node_id", metadata.NodeID))

	return nil
}
