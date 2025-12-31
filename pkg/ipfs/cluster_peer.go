package ipfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
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

// DiscoverClusterPeersFromLibP2P uses libp2p host to find other cluster peers
func (cm *ClusterConfigManager) DiscoverClusterPeersFromLibP2P(h host.Host) error {
	if h == nil {
		return nil
	}

	var clusterPeers []string
	for _, p := range h.Peerstore().Peers() {
		if p == h.ID() {
			continue
		}

		info := h.Peerstore().PeerInfo(p)
		for _, addr := range info.Addrs {
			if strings.Contains(addr.String(), "/tcp/9096") || strings.Contains(addr.String(), "/tcp/9094") {
				ma := addr.Encapsulate(multiaddr.StringCast(fmt.Sprintf("/p2p/%s", p.String())))
				clusterPeers = append(clusterPeers, ma.String())
			}
		}
	}

	if len(clusterPeers) > 0 {
		return cm.UpdatePeerAddresses(clusterPeers)
	}

	return nil
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

