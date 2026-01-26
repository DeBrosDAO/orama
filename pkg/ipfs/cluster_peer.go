package ipfs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"go.uber.org/zap"
)

// UpdatePeerAddresses updates the peer_addresses in service.json with given multiaddresses
func (cm *ClusterConfigManager) UpdatePeerAddresses(addrs []string) error {
	serviceJSONPath := filepath.Join(cm.clusterPath, "service.json")
	cfg, err := cm.loadOrCreateConfig(serviceJSONPath)
	if err != nil {
		return err
	}

	seen := make(map[string]bool)
	uniqueAddrs := []string{}
	for _, addr := range addrs {
		if !seen[addr] {
			uniqueAddrs = append(uniqueAddrs, addr)
			seen[addr] = true
		}
	}

	cfg.Cluster.PeerAddresses = uniqueAddrs
	return cm.saveConfig(serviceJSONPath, cfg)
}

// UpdateAllClusterPeers discovers all cluster peers from the gateway and updates local config
func (cm *ClusterConfigManager) UpdateAllClusterPeers() error {
	peers, err := cm.DiscoverClusterPeersFromGateway()
	if err != nil {
		return fmt.Errorf("failed to discover cluster peers: %w", err)
	}

	if len(peers) == 0 {
		return nil
	}

	peerAddrs := []string{}
	for _, p := range peers {
		peerAddrs = append(peerAddrs, p.Multiaddress)
	}

	return cm.UpdatePeerAddresses(peerAddrs)
}

// RepairPeerConfiguration attempts to fix configuration issues and re-synchronize peers
func (cm *ClusterConfigManager) RepairPeerConfiguration() error {
	cm.logger.Info("Attempting to repair IPFS Cluster peer configuration")

	_ = cm.FixIPFSConfigAddresses()

	peers, err := cm.DiscoverClusterPeersFromGateway()
	if err != nil {
		cm.logger.Warn("Could not discover peers from gateway during repair", zap.Error(err))
	} else {
		peerAddrs := []string{}
		for _, p := range peers {
			peerAddrs = append(peerAddrs, p.Multiaddress)
		}
		if len(peerAddrs) > 0 {
			_ = cm.UpdatePeerAddresses(peerAddrs)
		}
	}

	return nil
}

// DiscoverClusterPeersFromGateway queries the central gateway for registered IPFS Cluster peers
func (cm *ClusterConfigManager) DiscoverClusterPeersFromGateway() ([]ClusterPeerInfo, error) {
	// Not implemented - would require a central gateway URL in config
	return nil, nil
}

// DiscoverClusterPeersFromLibP2P discovers IPFS and IPFS Cluster peers by querying
// the /v1/network/status endpoint of connected libp2p peers.
// This is the correct approach since IPFS/Cluster peer IDs are different from libp2p peer IDs.
func (cm *ClusterConfigManager) DiscoverClusterPeersFromLibP2P(h host.Host) error {
	if h == nil {
		return nil
	}

	var clusterPeers []string
	var ipfsPeers []IPFSPeerEntry

	// Get unique IPs from connected libp2p peers
	peerIPs := make(map[string]bool)
	for _, p := range h.Peerstore().Peers() {
		if p == h.ID() {
			continue
		}

		info := h.Peerstore().PeerInfo(p)
		for _, addr := range info.Addrs {
			// Extract IP from multiaddr
			ip := extractIPFromMultiaddr(addr)
			if ip != "" && !strings.HasPrefix(ip, "127.") && !strings.HasPrefix(ip, "::1") {
				peerIPs[ip] = true
			}
		}
	}

	if len(peerIPs) == 0 {
		return nil
	}

	// Query each peer's /v1/network/status endpoint to get IPFS and Cluster info
	client := &http.Client{Timeout: 5 * time.Second}
	for ip := range peerIPs {
		statusURL := fmt.Sprintf("http://%s:6001/v1/network/status", ip)
		resp, err := client.Get(statusURL)
		if err != nil {
			cm.logger.Debug("Failed to query peer status", zap.String("ip", ip), zap.Error(err))
			continue
		}

		var status NetworkStatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			cm.logger.Debug("Failed to decode peer status", zap.String("ip", ip), zap.Error(err))
			continue
		}
		resp.Body.Close()

		// Add IPFS Cluster peer if available
		if status.IPFSCluster != nil && status.IPFSCluster.PeerID != "" {
			for _, addr := range status.IPFSCluster.Addresses {
				if strings.Contains(addr, "/tcp/9100") {
					clusterPeers = append(clusterPeers, addr)
					cm.logger.Info("Discovered IPFS Cluster peer", zap.String("peer", addr))
				}
			}
		}

		// Add IPFS peer if available
		if status.IPFS != nil && status.IPFS.PeerID != "" {
			for _, addr := range status.IPFS.SwarmAddresses {
				if strings.Contains(addr, "/tcp/4101") && !strings.Contains(addr, "127.0.0.1") {
					ipfsPeers = append(ipfsPeers, IPFSPeerEntry{
						ID:    status.IPFS.PeerID,
						Addrs: []string{addr},
					})
					cm.logger.Info("Discovered IPFS peer", zap.String("peer_id", status.IPFS.PeerID))
					break // One address per peer is enough
				}
			}
		}
	}

	// Update IPFS Cluster peer addresses
	if len(clusterPeers) > 0 {
		if err := cm.UpdatePeerAddresses(clusterPeers); err != nil {
			cm.logger.Warn("Failed to update cluster peer addresses", zap.Error(err))
		} else {
			cm.logger.Info("Updated IPFS Cluster peer addresses", zap.Int("count", len(clusterPeers)))
		}
	}

	// Update IPFS Peering.Peers
	if len(ipfsPeers) > 0 {
		if err := cm.UpdateIPFSPeeringConfig(ipfsPeers); err != nil {
			cm.logger.Warn("Failed to update IPFS peering config", zap.Error(err))
		} else {
			cm.logger.Info("Updated IPFS Peering.Peers", zap.Int("count", len(ipfsPeers)))
		}
	}

	return nil
}

// NetworkStatusResponse represents the response from /v1/network/status
type NetworkStatusResponse struct {
	PeerID      string                     `json:"peer_id"`
	PeerCount   int                        `json:"peer_count"`
	IPFS        *NetworkStatusIPFS         `json:"ipfs,omitempty"`
	IPFSCluster *NetworkStatusIPFSCluster  `json:"ipfs_cluster,omitempty"`
}

type NetworkStatusIPFS struct {
	PeerID         string   `json:"peer_id"`
	SwarmAddresses []string `json:"swarm_addresses"`
}

type NetworkStatusIPFSCluster struct {
	PeerID    string   `json:"peer_id"`
	Addresses []string `json:"addresses"`
}

// IPFSPeerEntry represents an IPFS peer for Peering.Peers config
type IPFSPeerEntry struct {
	ID    string   `json:"ID"`
	Addrs []string `json:"Addrs"`
}

// extractIPFromMultiaddr extracts the IP address from a multiaddr
func extractIPFromMultiaddr(ma multiaddr.Multiaddr) string {
	if ma == nil {
		return ""
	}

	// Try to convert to net.Addr and extract IP
	if addr, err := manet.ToNetAddr(ma); err == nil {
		addrStr := addr.String()
		// Handle "ip:port" format
		if idx := strings.LastIndex(addrStr, ":"); idx > 0 {
			return addrStr[:idx]
		}
		return addrStr
	}

	// Fallback: parse manually
	parts := strings.Split(ma.String(), "/")
	for i, part := range parts {
		if (part == "ip4" || part == "ip6") && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	return ""
}

// UpdateIPFSPeeringConfig updates the Peering.Peers section in IPFS config
func (cm *ClusterConfigManager) UpdateIPFSPeeringConfig(peers []IPFSPeerEntry) error {
	// Find IPFS config path
	ipfsRepoPath := cm.findIPFSRepoPath()
	if ipfsRepoPath == "" {
		return fmt.Errorf("could not find IPFS repo path")
	}

	configPath := filepath.Join(ipfsRepoPath, "config")

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read IPFS config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse IPFS config: %w", err)
	}

	// Get or create Peering section
	peering, ok := config["Peering"].(map[string]interface{})
	if !ok {
		peering = make(map[string]interface{})
	}

	// Get existing peers
	existingPeers := []IPFSPeerEntry{}
	if existingPeersList, ok := peering["Peers"].([]interface{}); ok {
		for _, p := range existingPeersList {
			if peerMap, ok := p.(map[string]interface{}); ok {
				entry := IPFSPeerEntry{}
				if id, ok := peerMap["ID"].(string); ok {
					entry.ID = id
				}
				if addrs, ok := peerMap["Addrs"].([]interface{}); ok {
					for _, a := range addrs {
						if addr, ok := a.(string); ok {
							entry.Addrs = append(entry.Addrs, addr)
						}
					}
				}
				if entry.ID != "" {
					existingPeers = append(existingPeers, entry)
				}
			}
		}
	}

	// Merge new peers with existing (avoid duplicates by ID)
	seenIDs := make(map[string]bool)
	mergedPeers := []interface{}{}

	// Add existing peers first
	for _, p := range existingPeers {
		seenIDs[p.ID] = true
		mergedPeers = append(mergedPeers, map[string]interface{}{
			"ID":    p.ID,
			"Addrs": p.Addrs,
		})
	}

	// Add new peers
	for _, p := range peers {
		if !seenIDs[p.ID] {
			seenIDs[p.ID] = true
			mergedPeers = append(mergedPeers, map[string]interface{}{
				"ID":    p.ID,
				"Addrs": p.Addrs,
			})
		}
	}

	// Update config
	peering["Peers"] = mergedPeers
	config["Peering"] = peering

	// Write back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal IPFS config: %w", err)
	}

	if err := os.WriteFile(configPath, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write IPFS config: %w", err)
	}

	return nil
}

// findIPFSRepoPath finds the IPFS repository path
func (cm *ClusterConfigManager) findIPFSRepoPath() string {
	dataDir := cm.cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[1:])
	}

	possiblePaths := []string{
		filepath.Join(dataDir, "ipfs", "repo"),
		filepath.Join(dataDir, "node-1", "ipfs", "repo"),
		filepath.Join(dataDir, "node-2", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "ipfs", "repo"),
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(filepath.Join(path, "config")); err == nil {
			return path
		}
	}

	return ""
}

func (cm *ClusterConfigManager) getPeerID() (string, error) {
	dataDir := cm.cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[1:])
	}

	possiblePaths := []string{
		filepath.Join(dataDir, "ipfs", "repo"),
		filepath.Join(dataDir, "node-1", "ipfs", "repo"),
		filepath.Join(dataDir, "node-2", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node-1", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node-2", "ipfs", "repo"),
	}

	var ipfsRepoPath string
	for _, path := range possiblePaths {
		if _, err := os.Stat(filepath.Join(path, "config")); err == nil {
			ipfsRepoPath = path
			break
		}
	}

	if ipfsRepoPath == "" {
		return "", fmt.Errorf("could not find IPFS repo path")
	}

	idCmd := exec.Command("ipfs", "id", "-f", "<id>")
	idCmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
	out, err := idCmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// ClusterPeerInfo represents information about an IPFS Cluster peer
type ClusterPeerInfo struct {
	ID           string    `json:"id"`
	Multiaddress string    `json:"multiaddress"`
	NodeName     string    `json:"node_name"`
	LastSeen     time.Time `json:"last_seen"`
}

