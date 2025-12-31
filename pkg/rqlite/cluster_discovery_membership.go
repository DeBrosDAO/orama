package rqlite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/discovery"
	"go.uber.org/zap"
)

// collectPeerMetadata collects RQLite metadata from LibP2P peers
func (c *ClusterDiscoveryService) collectPeerMetadata() []*discovery.RQLiteNodeMetadata {
	connectedPeers := c.host.Network().Peers()
	var metadata []*discovery.RQLiteNodeMetadata

	c.mu.RLock()
	currentRaftAddr := c.raftAddress
	currentHTTPAddr := c.httpAddress
	c.mu.RUnlock()

	// Add ourselves
	ourMetadata := &discovery.RQLiteNodeMetadata{
		NodeID:         currentRaftAddr, // RQLite uses raft address as node ID
		RaftAddress:    currentRaftAddr,
		HTTPAddress:    currentHTTPAddr,
		NodeType:       c.nodeType,
		RaftLogIndex:   c.rqliteManager.getRaftLogIndex(),
		LastSeen:       time.Now(),
		ClusterVersion: "1.0",
	}

	if c.adjustSelfAdvertisedAddresses(ourMetadata) {
		c.logger.Debug("Adjusted self-advertised RQLite addresses",
			zap.String("raft_address", ourMetadata.RaftAddress),
			zap.String("http_address", ourMetadata.HTTPAddress))
	}

	metadata = append(metadata, ourMetadata)

	staleNodeIDs := make([]string, 0)

	for _, peerID := range connectedPeers {
		if val, err := c.host.Peerstore().Get(peerID, "rqlite_metadata"); err == nil {
			if jsonData, ok := val.([]byte); ok {
				var peerMeta discovery.RQLiteNodeMetadata
				if err := json.Unmarshal(jsonData, &peerMeta); err == nil {
					if updated, stale := c.adjustPeerAdvertisedAddresses(peerID, &peerMeta); updated && stale != "" {
						staleNodeIDs = append(staleNodeIDs, stale)
					}
					peerMeta.LastSeen = time.Now()
					metadata = append(metadata, &peerMeta)
				}
			}
		}
	}

	if len(staleNodeIDs) > 0 {
		c.mu.Lock()
		for _, id := range staleNodeIDs {
			delete(c.knownPeers, id)
			delete(c.peerHealth, id)
		}
		c.mu.Unlock()
	}

	return metadata
}

type membershipUpdateResult struct {
	peersJSON []map[string]interface{}
	added     []string
	updated   []string
	changed   bool
}

func (c *ClusterDiscoveryService) updateClusterMembership() {
	metadata := c.collectPeerMetadata()

	c.mu.Lock()
	result := c.computeMembershipChangesLocked(metadata)
	c.mu.Unlock()

	if result.changed {
		if len(result.added) > 0 || len(result.updated) > 0 {
			c.logger.Info("Membership changed",
				zap.Int("added", len(result.added)),
				zap.Int("updated", len(result.updated)),
				zap.Strings("added", result.added),
				zap.Strings("updated", result.updated))
		}

		if err := c.writePeersJSONWithData(result.peersJSON); err != nil {
			c.logger.Error("Failed to write peers.json",
				zap.Error(err),
				zap.String("data_dir", c.dataDir),
				zap.Int("peers", len(result.peersJSON)))
		} else {
			c.logger.Debug("peers.json updated",
				zap.Int("peers", len(result.peersJSON)))
		}

		c.mu.Lock()
		c.lastUpdate = time.Now()
		c.mu.Unlock()
	}
}

func (c *ClusterDiscoveryService) computeMembershipChangesLocked(metadata []*discovery.RQLiteNodeMetadata) membershipUpdateResult {
	added := []string{}
	updated := []string{}

	for _, meta := range metadata {
		isSelf := meta.NodeID == c.raftAddress

		if existing, ok := c.knownPeers[meta.NodeID]; ok {
			if existing.RaftLogIndex != meta.RaftLogIndex ||
				existing.HTTPAddress != meta.HTTPAddress ||
				existing.RaftAddress != meta.RaftAddress {
				updated = append(updated, meta.NodeID)
			}
		} else {
			added = append(added, meta.NodeID)
			c.logger.Info("Node added",
				zap.String("node", meta.NodeID),
				zap.String("raft", meta.RaftAddress),
				zap.String("type", meta.NodeType),
				zap.Uint64("log_index", meta.RaftLogIndex))
		}

		c.knownPeers[meta.NodeID] = meta

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

	remotePeerCount := 0
	for _, peer := range c.knownPeers {
		if peer.NodeID != c.raftAddress {
			remotePeerCount++
		}
	}

	peers := c.getPeersJSONUnlocked()
	shouldWrite := len(added) > 0 || len(updated) > 0 || c.lastUpdate.IsZero()

	if shouldWrite {
		if c.lastUpdate.IsZero() {
			requiredRemotePeers := c.minClusterSize - 1

			if remotePeerCount < requiredRemotePeers {
				c.logger.Info("Waiting for peers",
					zap.Int("have", remotePeerCount),
					zap.Int("need", requiredRemotePeers),
					zap.Int("min_size", c.minClusterSize))
				return membershipUpdateResult{
					changed: false,
				}
			}
		}

		if len(peers) == 0 && c.lastUpdate.IsZero() {
			c.logger.Info("No remote peers - waiting")
			return membershipUpdateResult{
				changed: false,
			}
		}

		if c.lastUpdate.IsZero() {
			c.logger.Info("Initial sync",
				zap.Int("total", len(c.knownPeers)),
				zap.Int("remote", remotePeerCount),
				zap.Int("in_json", len(peers)))
		}

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

func (c *ClusterDiscoveryService) removeInactivePeers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := []string{}

	for nodeID, health := range c.peerHealth {
		inactiveDuration := now.Sub(health.LastSeen)

		if inactiveDuration > c.inactivityLimit {
			c.logger.Warn("Node removed",
				zap.String("node", nodeID),
				zap.String("reason", "inactive"),
				zap.Duration("inactive_duration", inactiveDuration))

			delete(c.knownPeers, nodeID)
			delete(c.peerHealth, nodeID)
			removed = append(removed, nodeID)
		}
	}

	if len(removed) > 0 {
		c.logger.Info("Removed inactive",
			zap.Int("count", len(removed)),
			zap.Strings("nodes", removed))

		if err := c.writePeersJSON(); err != nil {
			c.logger.Error("Failed to write peers.json after cleanup", zap.Error(err))
		}
	}
}

func (c *ClusterDiscoveryService) getPeersJSON() []map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getPeersJSONUnlocked()
}

func (c *ClusterDiscoveryService) getPeersJSONUnlocked() []map[string]interface{} {
	peers := make([]map[string]interface{}, 0, len(c.knownPeers))

	for _, peer := range c.knownPeers {
		peerEntry := map[string]interface{}{
			"id":        peer.RaftAddress,
			"address":   peer.RaftAddress,
			"non_voter": false,
		}
		peers = append(peers, peerEntry)
	}

	return peers
}

func (c *ClusterDiscoveryService) writePeersJSON() error {
	c.mu.RLock()
	peers := c.getPeersJSONUnlocked()
	c.mu.RUnlock()

	return c.writePeersJSONWithData(peers)
}

func (c *ClusterDiscoveryService) writePeersJSONWithData(peers []map[string]interface{}) error {
	dataDir := os.ExpandEnv(c.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	rqliteDir := filepath.Join(dataDir, "rqlite", "raft")

	if err := os.MkdirAll(rqliteDir, 0755); err != nil {
		return fmt.Errorf("failed to create raft directory %s: %w", rqliteDir, err)
	}

	peersFile := filepath.Join(rqliteDir, "peers.json")
	backupFile := filepath.Join(rqliteDir, "peers.json.backup")

	if _, err := os.Stat(peersFile); err == nil {
		data, err := os.ReadFile(peersFile)
		if err == nil {
			_ = os.WriteFile(backupFile, data, 0644)
		}
	}

	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal peers.json: %w", err)
	}

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
		zap.Int("peers", len(peers)),
		zap.Strings("nodes", nodeIDs))

	return nil
}

