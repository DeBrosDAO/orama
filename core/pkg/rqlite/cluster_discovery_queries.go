package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// GetActivePeers returns a list of active peers (not including self)
func (c *ClusterDiscoveryService) GetActivePeers() []*discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := make([]*discovery.RQLiteNodeMetadata, 0, len(c.knownPeers))
	for _, peer := range c.knownPeers {
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

// knowsRaftAddress reports whether discovery currently has a peer advertising
// raftAddr. Used as one of the independent signals before a member is evicted
// from the raft configuration: a node libp2p can still exchange metadata with
// is not gone, whatever raft's reachability column says.
func (c *ClusterDiscoveryService) knowsRaftAddress(raftAddr string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, peer := range c.knownPeers {
		if peer != nil && peer.RaftAddress == raftAddr {
			return true
		}
	}
	return false
}

// GetNodeWithHighestLogIndex returns the node with the highest Raft log index
func (c *ClusterDiscoveryService) GetNodeWithHighestLogIndex() *discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var highest *discovery.RQLiteNodeMetadata
	var maxIndex uint64 = 0

	for _, peer := range c.knownPeers {
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

	return time.Since(c.lastUpdate) < 5*time.Minute
}

// FindJoinTargets discovers join targets via LibP2P
func (c *ClusterDiscoveryService) FindJoinTargets() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	targets := []string{}

	type nodeWithIndex struct {
		address  string
		logIndex uint64
	}
	var nodes []nodeWithIndex
	for _, peer := range c.knownPeers {
		nodes = append(nodes, nodeWithIndex{peer.RaftAddress, peer.RaftLogIndex})
	}

	for i := 0; i < len(nodes)-1; i++ {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[j].logIndex > nodes[i].logIndex {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}
		}
	}

	for _, n := range nodes {
		targets = append(targets, n.address)
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

	c.updateClusterMembership()

	c.mu.RLock()
	peerCount := len(c.knownPeers)
	c.mu.RUnlock()

	c.logger.Info("Discovery settled",
		zap.Int("peer_count", peerCount))
}

// TriggerSync manually triggers a cluster membership sync
func (c *ClusterDiscoveryService) TriggerSync() {
	c.updateClusterMembership()
}

// ForceWritePeersJSON writes peers.json to the RAFT directory for intentional
// cluster recovery. rqlite v8 will read this on startup and reset its Raft
// configuration. Only call this when you explicitly want Raft recovery
// (e.g., after clearing raft state or during split-brain recovery).
func (c *ClusterDiscoveryService) ForceWritePeersJSON() error {
	c.logger.Info("Force writing recovery peers.json to raft directory")

	metadata := c.collectPeerMetadata()

	c.mu.Lock()
	for _, meta := range metadata {
		c.knownPeers[meta.NodeID] = meta
		if meta.NodeID != c.raftAddress {
			if _, ok := c.peerHealth[meta.NodeID]; !ok {
				c.peerHealth[meta.NodeID] = &PeerHealth{
					LastSeen:       time.Now(),
					LastSuccessful: time.Now(),
					Status:         "active",
				}
			} else {
				c.peerHealth[meta.NodeID].LastSeen = time.Now()
				c.peerHealth[meta.NodeID].Status = "active"
			}
		}
	}
	peers := c.getPeersJSONUnlocked()
	c.mu.Unlock()

	// Write to RAFT directory — this is intentional recovery
	if err := c.writeRecoveryPeersJSON(peers); err != nil {
		c.logger.Error("Failed to force write recovery peers.json",
			zap.Error(err),
			zap.String("data_dir", c.dataDir),
			zap.Int("peers", len(peers)))
		return err
	}

	// Also update discovery location
	_ = c.writePeersJSONWithData(peers)

	return nil
}

// TriggerPeerExchange actively exchanges peer information with connected peers
func (c *ClusterDiscoveryService) TriggerPeerExchange(ctx context.Context) error {
	if c.discoveryMgr == nil {
		return fmt.Errorf("discovery manager not available")
	}

	collected := c.discoveryMgr.TriggerPeerExchange(ctx)
	c.logger.Debug("Exchange completed", zap.Int("with_metadata", collected))

	return nil
}

// UpdateOwnMetadata updates our own RQLite metadata in the peerstore.
// This is called periodically and after significant state changes (lifecycle
// transitions, service status updates) to ensure peers have current info.
func (c *ClusterDiscoveryService) UpdateOwnMetadata() {
	c.mu.RLock()
	currentRaftAddr := c.raftAddress
	currentHTTPAddr := c.httpAddress
	c.mu.RUnlock()

	metadata := &discovery.RQLiteNodeMetadata{
		NodeID:         currentRaftAddr,
		RaftAddress:    currentRaftAddr,
		HTTPAddress:    currentHTTPAddr,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
		PeerID:         c.host.ID().String(),
		WireGuardIP:    c.wireGuardIP,
	}

	// Populate lifecycle state from the lifecycle manager
	if c.lifecycle != nil {
		state, ttl := c.lifecycle.Snapshot()
		metadata.LifecycleState = string(state)
		if state == "maintenance" {
			metadata.MaintenanceTTL = ttl
		}
	}

	if c.adjustSelfAdvertisedAddresses(metadata) {
		c.logger.Debug("Adjusted self-advertised RQLite addresses in UpdateOwnMetadata",
			zap.String("raft_address", metadata.RaftAddress),
			zap.String("http_address", metadata.HTTPAddress))
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		c.logger.Error("Failed to marshal own metadata", zap.Error(err))
		return
	}

	if err := c.host.Peerstore().Put(c.host.ID(), "rqlite_metadata", data); err != nil {
		c.logger.Error("Failed to store own metadata", zap.Error(err))
		return
	}

	c.logger.Debug("Metadata updated",
		zap.String("node", metadata.NodeID),
		zap.Uint64("log_index", metadata.RaftLogIndex),
		zap.String("lifecycle", metadata.LifecycleState))
}

// ProvideMetadata builds and returns the current node metadata without storing it.
// Implements discovery.MetadataProvider so the MetadataPublisher can call this
// on a regular interval and store the result in the peerstore.
func (c *ClusterDiscoveryService) ProvideMetadata() *discovery.RQLiteNodeMetadata {
	c.mu.RLock()
	currentRaftAddr := c.raftAddress
	currentHTTPAddr := c.httpAddress
	c.mu.RUnlock()

	metadata := &discovery.RQLiteNodeMetadata{
		NodeID:         currentRaftAddr,
		RaftAddress:    currentRaftAddr,
		HTTPAddress:    currentHTTPAddr,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
		PeerID:         c.host.ID().String(),
		WireGuardIP:    c.wireGuardIP,
	}

	if c.lifecycle != nil {
		state, ttl := c.lifecycle.Snapshot()
		metadata.LifecycleState = string(state)
		if state == "maintenance" {
			metadata.MaintenanceTTL = ttl
		}
	}

	c.adjustSelfAdvertisedAddresses(metadata)
	return metadata
}

// StoreRemotePeerMetadata stores metadata received from a remote peer
func (c *ClusterDiscoveryService) StoreRemotePeerMetadata(peerID peer.ID, metadata *discovery.RQLiteNodeMetadata) error {
	if metadata == nil {
		return fmt.Errorf("metadata is nil")
	}

	if updated, stale := c.adjustPeerAdvertisedAddresses(peerID, metadata); updated && stale != "" {
		c.mu.Lock()
		delete(c.knownPeers, stale)
		delete(c.peerHealth, stale)
		c.mu.Unlock()
	}

	metadata.LastSeen = time.Now()

	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := c.host.Peerstore().Put(peerID, "rqlite_metadata", data); err != nil {
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	c.logger.Debug("Metadata stored",
		zap.String("peer", shortPeerID(peerID)),
		zap.String("node", metadata.NodeID))

	return nil
}
