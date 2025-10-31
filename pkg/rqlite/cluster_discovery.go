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

// collectPeerMetadata collects RQLite metadata from LibP2P peers
func (c *ClusterDiscoveryService) collectPeerMetadata() []*discovery.RQLiteNodeMetadata {
	connectedPeers := c.host.Network().Peers()
	var metadata []*discovery.RQLiteNodeMetadata

	c.logger.Debug("Collecting peer metadata from LibP2P",
		zap.Int("connected_libp2p_peers", len(connectedPeers)))

	// Add ourselves
	ourMetadata := &discovery.RQLiteNodeMetadata{
		NodeID:         c.raftAddress, // RQLite uses raft address as node ID
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

// membershipUpdateResult contains the result of a membership update operation
type membershipUpdateResult struct {
	peersJSON []map[string]interface{}
	added     []string
	updated   []string
	changed   bool
}

// updateClusterMembership updates the cluster membership based on discovered peers
func (c *ClusterDiscoveryService) updateClusterMembership() {
	metadata := c.collectPeerMetadata()

	c.logger.Debug("Collected peer metadata",
		zap.Int("metadata_count", len(metadata)))

	// Compute membership changes while holding lock
	c.mu.Lock()
	result := c.computeMembershipChangesLocked(metadata)
	c.mu.Unlock()

	// Perform file I/O outside the lock
	if result.changed {
		// Log state changes (peer added/removed) at Info level
		if len(result.added) > 0 || len(result.updated) > 0 {
			c.logger.Info("Cluster membership changed",
				zap.Int("added", len(result.added)),
				zap.Int("updated", len(result.updated)),
				zap.Strings("added_ids", result.added),
				zap.Strings("updated_ids", result.updated))
		}

		// Write peers.json without holding lock
		if err := c.writePeersJSONWithData(result.peersJSON); err != nil {
			c.logger.Error("CRITICAL: Failed to write peers.json",
				zap.Error(err),
				zap.String("data_dir", c.dataDir),
				zap.Int("peer_count", len(result.peersJSON)))
		} else {
			c.logger.Debug("peers.json updated",
				zap.Int("peer_count", len(result.peersJSON)))
		}

		// Update lastUpdate timestamp
		c.mu.Lock()
		c.lastUpdate = time.Now()
		c.mu.Unlock()
	} else {
		c.mu.RLock()
		totalPeers := len(c.knownPeers)
		c.mu.RUnlock()
		c.logger.Debug("No changes to cluster membership",
			zap.Int("total_peers", totalPeers))
	}
}

// computeMembershipChangesLocked computes membership changes and returns snapshot data
// Must be called with lock held
func (c *ClusterDiscoveryService) computeMembershipChangesLocked(metadata []*discovery.RQLiteNodeMetadata) membershipUpdateResult {
	// Track changes
	added := []string{}
	updated := []string{}

	// Update known peers, but skip self for health tracking
	for _, meta := range metadata {
		// Skip self-metadata for health tracking (we only track remote peers)
		isSelf := meta.NodeID == c.raftAddress

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

		// Update health tracking only for remote peers
		if !isSelf {
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
	}

	// Determine if we should write peers.json
	shouldWrite := len(added) > 0 || len(updated) > 0 || c.lastUpdate.IsZero()

	if shouldWrite {
		// Log initial sync if this is the first time
		if c.lastUpdate.IsZero() {
			c.logger.Info("Initial cluster membership sync",
				zap.Int("total_peers", len(c.knownPeers)))
		}

		// Get peers JSON snapshot
		peers := c.getPeersJSONUnlocked()
		return membershipUpdateResult{
			peersJSON: peers,
			added:     added,
			updated:   updated,
			changed:   true,
		}
	}

	return membershipUpdateResult{
		changed: false,
	}
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
			"id":        peer.RaftAddress, // RQLite uses raft address as node ID
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
	// Expand ~ in data directory path
	dataDir := os.ExpandEnv(c.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Get the RQLite raft directory
	rqliteDir := filepath.Join(dataDir, "rqlite", "raft")

	c.logger.Debug("Writing peers.json",
		zap.String("data_dir", c.dataDir),
		zap.String("expanded_path", dataDir),
		zap.String("raft_dir", rqliteDir),
		zap.Int("peer_count", len(peers)))

	if err := os.MkdirAll(rqliteDir, 0755); err != nil {
		return fmt.Errorf("failed to create raft directory %s: %w", rqliteDir, err)
	}

	peersFile := filepath.Join(rqliteDir, "peers.json")
	backupFile := filepath.Join(rqliteDir, "peers.json.backup")

	// Backup existing peers.json if it exists
	if _, err := os.Stat(peersFile); err == nil {
		c.logger.Debug("Backing up existing peers.json", zap.String("backup_file", backupFile))
		data, err := os.ReadFile(peersFile)
		if err == nil {
			_ = os.WriteFile(backupFile, data, 0644)
		}
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal peers.json: %w", err)
	}

	c.logger.Debug("Marshaled peers.json", zap.Int("data_size", len(data)))

	// Write atomically using temp file + rename
	tempFile := peersFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp peers.json %s: %w", tempFile, err)
	}

	if err := os.Rename(tempFile, peersFile); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tempFile, peersFile, err)
	}

	nodeIDs := make([]string, 0, len(peers))
	for _, p := range peers {
		if id, ok := p["id"].(string); ok {
			nodeIDs = append(nodeIDs, id)
		}
	}

	c.logger.Info("peers.json written",
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
		// Skip self (compare by raft address since that's the NodeID now)
		if peer.NodeID == c.raftAddress {
			continue
		}
		peers = append(peers, peer)
	}

	return peers
}

// GetAllPeers returns a list of all known peers (including self)
func (c *ClusterDiscoveryService) GetAllPeers() []*discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := make([]*discovery.RQLiteNodeMetadata, 0, len(c.knownPeers))
	for _, peer := range c.knownPeers {
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
		// Skip self (compare by raft address since that's the NodeID now)
		if peer.NodeID == c.raftAddress {
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

// TriggerSync manually triggers a cluster membership sync
func (c *ClusterDiscoveryService) TriggerSync() {
	c.logger.Info("Manually triggering cluster membership sync")

	// For bootstrap nodes, wait a bit for peer discovery to stabilize
	if c.nodeType == "bootstrap" {
		c.logger.Info("Bootstrap node: waiting for peer discovery to complete")
		time.Sleep(5 * time.Second)
	}

	c.updateClusterMembership()
}

// TriggerPeerExchange actively exchanges peer information with connected peers
// This populates the peerstore with RQLite metadata from other nodes
func (c *ClusterDiscoveryService) TriggerPeerExchange(ctx context.Context) error {
	if c.discoveryMgr == nil {
		return fmt.Errorf("discovery manager not available")
	}

	c.logger.Info("Triggering peer exchange via discovery manager")
	collected := c.discoveryMgr.TriggerPeerExchange(ctx)
	c.logger.Info("Peer exchange completed", zap.Int("peers_with_metadata", collected))

	return nil
}

// UpdateOwnMetadata updates our own RQLite metadata in the peerstore
func (c *ClusterDiscoveryService) UpdateOwnMetadata() {
	metadata := &discovery.RQLiteNodeMetadata{
		NodeID:         c.raftAddress, // RQLite uses raft address as node ID
		RaftAddress:    c.raftAddress,
		HTTPAddress:    c.httpAddress,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
	}

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

	c.logger.Debug("Updated own RQLite metadata",
		zap.String("node_id", metadata.NodeID),
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
