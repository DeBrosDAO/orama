package node

import (
	"context"
	"fmt"
	"net"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/namespace"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
)

func (n *Node) startIndexPubsub(ctx context.Context) error {
	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
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

	sup, nodeID, err := n.indexSupervisor()
	if err != nil {
		return err
	}

	// The index gateway reaches rqlited where it binds; the spawner adds the
	// credentials when it writes the gateway YAML.
	rqliteEP, err := database.IndexEndpoint(&n.config.Database, &n.config.Discovery)
	if err != nil {
		return fmt.Errorf("index gateway: %w", err)
	}

	// The index Olric binds the same advertise host as rqlited
	// (startRQLiteLocal).
	olricServers := n.config.HTTPGateway.OlricServers
	if len(olricServers) == 0 {
		olricServers = []string{net.JoinHostPort(rqliteEP.Host, fmt.Sprintf("%d", namespace.IndexOlricHTTPPort))}
	}

	return sup.EnsureGateway(ctx, gatewayspec.InstanceConfig{
		NodeID:                nodeID,
		RQLiteDSN:             rqliteEP.BaseURL(),
		BaseDomain:            n.config.HTTPGateway.BaseDomain,
		OlricServers:          olricServers,
		OlricTimeout:          n.config.HTTPGateway.OlricTimeout,
		IPFSClusterAPIURL:     n.config.HTTPGateway.IPFSClusterAPIURL,
		IPFSAPIURL:            n.config.HTTPGateway.IPFSAPIURL,
		IPFSTimeout:           n.config.HTTPGateway.IPFSTimeout,
		IPFSReplicationFactor: n.config.Database.IPFS.ReplicationFactor,
		SecretsEncryptionKey:  n.config.HTTPGateway.SecretsEncryptionKey,
		// Bugboard #274: carry the host's self-hosted ntfy base URL into the
		// index gateway's YAML so the namespace cluster manager can forward it
		// to spawned namespace gateways, which otherwise register no ntfy push
		// provider at all.
		NtfyBaseURL: n.config.HTTPGateway.NtfyBaseURL,
		NodePeerID:  nodeID,
	})
}

// startIPFSClusterConfig initializes and ensures IPFS Cluster configuration.
// A node with no cluster API configured has nothing to do here.
//
// The manager is built at most once and published under depsMu, because the
// monitoring loop reads it on its own goroutine and this component can still be
// retrying when that loop starts. The config writes take clusterCfgMu for the
// same reason: the monitoring loop repairs the same service.json.
func (n *Node) startIPFSClusterConfig() error {
	if n.config.Database.IPFS.ClusterAPIURL == "" {
		return nil
	}

	cm := n.getClusterConfigManager()
	if cm == nil {
		n.logger.ComponentInfo(logging.ComponentNode, "Initializing IPFS Cluster configuration")
		built, err := ipfs.NewClusterConfigManager(n.config, n.logger.Logger)
		if err != nil {
			return err
		}
		n.depsMu.Lock()
		n.clusterConfigManager = built
		n.depsMu.Unlock()
		cm = built
	}

	// The Kubo repo's API and gateway bindings, and every listener in the
	// cluster's service.json, are install's and upgrade's to write
	// (pkg/install/installers); this sets only what the node owns.
	n.clusterCfgMu.Lock()
	defer n.clusterCfgMu.Unlock()
	return cm.EnsureConfig()
}

// getClusterConfigManager returns the IPFS cluster config manager, or nil if
// the config component has not built it yet.
func (n *Node) getClusterConfigManager() *ipfs.ClusterConfigManager {
	n.depsMu.RLock()
	defer n.depsMu.RUnlock()
	return n.clusterConfigManager
}

// getClusterDiscovery returns the cluster discovery service, or nil if the
// cluster-discovery component has not started it yet.
func (n *Node) getClusterDiscovery() *database.ClusterDiscoveryService {
	n.depsMu.RLock()
	defer n.depsMu.RUnlock()
	return n.clusterDiscovery
}

// discoverClusterPeers rewrites service.json's peer addresses, so it takes
// the lock the config component holds.
func (n *Node) discoverClusterPeers(cm *ipfs.ClusterConfigManager) error {
	n.clusterCfgMu.Lock()
	defer n.clusterCfgMu.Unlock()
	return cm.DiscoverClusterPeersFromLibP2P(n.host)
}
