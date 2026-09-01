package node

import (
	"context"
	"fmt"
	"net"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/namespace"
)

func (n *Node) startIndexPubsub(ctx context.Context) error {
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return err
	}
	sup := namespace.NewIndexSupervisor(filepath.Dir(dataDir), n.logger.Logger)
	nodeID := n.config.Node.ID
	if nodeID == "" {
		nodeID = "node"
	}
	return sup.EnsurePubsub(ctx, nodeID, n.config.Discovery.BootstrapPeers)
}

// startIndexGateway starts orama-namespace-gateway@index.
// orama-node does not bind the gateway port; Caddy reverse_proxies to it.
func (n *Node) startIndexGateway(ctx context.Context) error {
	if !n.config.HTTPGateway.Enabled {
		n.logger.ComponentInfo(logging.ComponentNode, "HTTP Gateway disabled in config")
		return nil
	}

	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return err
	}
	oramaDir := filepath.Dir(dataDir)
	sup := namespace.NewIndexSupervisor(oramaDir, n.logger.Logger)

	nodeID := n.config.Node.ID
	if nodeID == "" {
		nodeID = "node"
	}
	bindAddr, _, _ := net.SplitHostPort(n.config.Discovery.HttpAdvAddress)
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	olricServers := n.config.HTTPGateway.OlricServers
	if len(olricServers) == 0 {
		olricServers = []string{net.JoinHostPort(bindAddr, fmt.Sprintf("%d", namespace.IndexOlricHTTPPort))}
	}

	return sup.EnsureGateway(ctx, gateway.InstanceConfig{
		NodeID:                nodeID,
		BaseDomain:            n.config.HTTPGateway.BaseDomain,
		OlricServers:          olricServers,
		OlricTimeout:          n.config.HTTPGateway.OlricTimeout,
		IPFSClusterAPIURL:     n.config.HTTPGateway.IPFSClusterAPIURL,
		IPFSAPIURL:            n.config.HTTPGateway.IPFSAPIURL,
		IPFSTimeout:           n.config.HTTPGateway.IPFSTimeout,
		IPFSReplicationFactor: n.config.Database.IPFS.ReplicationFactor,
		SecretsEncryptionKey:  n.config.HTTPGateway.SecretsEncryptionKey,
		DataDir:               oramaDir,
		NodePeerID:            loadNodePeerIDFromIdentity(n.config.Node.DataDir),
	})
}

// startIPFSClusterConfig initializes and ensures IPFS Cluster configuration
func (n *Node) startIPFSClusterConfig() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Initializing IPFS Cluster configuration")

	cm, err := ipfs.NewClusterConfigManager(n.config, n.logger.Logger)
	if err != nil {
		return err
	}
	n.clusterConfigManager = cm

	_ = cm.FixIPFSConfigAddresses()
	if err := cm.EnsureConfig(); err != nil {
		return err
	}

	_ = cm.RepairPeerConfiguration()
	return nil
}
