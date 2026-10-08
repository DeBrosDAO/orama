package node

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"net"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"

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

	bootstrapPeers, err := gatewayBootstrapPeers(n.hostRef(), rqliteEP.Host, n.config.Discovery.BootstrapPeers)
	if err != nil {
		return fmt.Errorf("index gateway: %w", err)
	}

	return sup.EnsureGateway(ctx, gatewayspec.InstanceConfig{
		NodeID:                nodeID,
		RQLiteDSN:             rqliteEP.BaseURL(),
		BootstrapPeers:        bootstrapPeers,
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

		RelayAllowedSuffixes: n.config.HTTPGateway.RelayAllowedSuffixes,
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
func (n *Node) discoverClusterPeers(ctx context.Context, cm *ipfs.ClusterConfigManager) error {
	adapter := n.getRQLiteAdapter()
	if adapter == nil {
		return fmt.Errorf("cannot discover IPFS cluster peers: the node registry (RQLite) is not connected yet")
	}
	h := n.hostRef()
	if h == nil {
		return fmt.Errorf("cannot discover IPFS cluster peers: the libp2p host is not started yet")
	}
	// The registry is read before the lock, under its own deadline: a registry
	// without quorum must not hold up the other service.json writers.
	readCtx, cancel := context.WithTimeout(ctx, clusterPeerRegistryTimeout)
	targets, skipped, err := activeOverlayPeers(readCtx, adapter.GetSQLDB())
	cancel()
	if err != nil {
		return fmt.Errorf("cannot discover IPFS cluster peers: %w", err)
	}
	if skipped > 0 {
		n.logger.ComponentWarn(logging.ComponentNode, "IPFS cluster peer discovery skipped registry rows whose address is not on the WireGuard overlay",
			zap.Int("skipped", skipped))
	}
	n.clusterCfgMu.Lock()
	defer n.clusterCfgMu.Unlock()
	return cm.DiscoverClusterPeers(ctx, h.ID().String(), func(context.Context) ([]ipfs.PeerTarget, error) {
		return targets, nil
	})
}

// clusterPeerRegistryTimeout bounds the registry read of one discovery pass.
const clusterPeerRegistryTimeout = 10 * time.Second

// gatewayBootstrapPeers is the peer list the index gateway's libp2p client
// dials: this node first, over its overlay address, then the configured
// bootstrap peers. The node is always there and does the mesh discovery; the
// configured list is empty on the genesis node and names one other node on the
// rest, so a gateway bootstrapped from it alone reported no peers on the first
// and lost them all when that one node was down.
func gatewayBootstrapPeers(h host.Host, overlayIP string, configured []string) ([]string, error) {
	if h == nil {
		return nil, fmt.Errorf("this node's libp2p host is not running, so the gateway has no peer to bootstrap from")
	}
	self, err := overlayMultiaddr(h, overlayIP)
	if err != nil {
		return nil, err
	}
	peers := []string{self}
	for _, p := range configured {
		if p = strings.TrimSpace(p); p != "" && p != self {
			peers = append(peers, p)
		}
	}
	return peers, nil
}

// overlayMultiaddr is h's TCP listen address on overlayIP, with its peer id.
func overlayMultiaddr(h host.Host, overlayIP string) (string, error) {
	for _, a := range h.Addrs() {
		ip, err := a.ValueForProtocol(multiaddr.P_IP4)
		if err != nil || ip != overlayIP {
			continue
		}
		if _, err := a.ValueForProtocol(multiaddr.P_TCP); err != nil {
			continue
		}
		return fmt.Sprintf("%s/p2p/%s", a.String(), h.ID()), nil
	}
	return "", fmt.Errorf("this node's libp2p host listens on no TCP address on the overlay IP %s (listening on %v); check the node's listen addresses", overlayIP, h.Addrs())
}
