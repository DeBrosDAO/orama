package node

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/host"
	"go.uber.org/zap"
)

// Node represents a network node with RQLite database
type Node struct {
	config *config.Config
	logger *logging.ColoredLogger
	host   host.Host

	// Lifecycle state machine (joining → active ⇄ maintenance)
	lifecycle *lifecycle.Manager

	rqliteManager    *database.RQLiteManager
	rqliteAdapter    *database.RQLiteAdapter
	clusterDiscovery *database.ClusterDiscoveryService

	// Peer discovery
	peerDiscoveryCancel context.CancelFunc

	// PubSub
	pubsub *pubsub.ClientAdapter

	// Discovery
	discoveryManager *discovery.Manager

	// IPFS Cluster config manager
	clusterConfigManager *ipfs.ClusterConfigManager

	// Full gateway (for API, auth, pubsub, and internal service routing)
	apiGateway       *gateway.Gateway
	apiGatewayServer *http.Server
}

// NewNode creates a new network node
func NewNode(cfg *config.Config) (*Node, error) {
	// Create colored logger
	logger, err := logging.NewColoredLogger(logging.ComponentNode, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	return &Node{
		config:    cfg,
		logger:    logger,
		lifecycle: lifecycle.NewManager(),
	}, nil
}

// Start starts the network node and all its services
func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("Starting network node", zap.String("data_dir", n.config.Node.DataDir))

	// Expand ~ and env vars in data directory path
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return fmt.Errorf("failed to expand data directory path: %w", err)
	}

	// Create data directory
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Start HTTP Gateway first (doesn't depend on other services)
	if err := n.startHTTPGateway(ctx); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to start HTTP Gateway", zap.Error(err))
	}

	// Start LibP2P host first (needed for cluster discovery)
	if err := n.startLibP2P(); err != nil {
		return fmt.Errorf("failed to start LibP2P: %w", err)
	}

	// Initialize IPFS Cluster configuration if enabled
	if n.config.Database.IPFS.ClusterAPIURL != "" {
		if err := n.startIPFSClusterConfig(); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to initialize IPFS Cluster config", zap.Error(err))
		}
	}

	// Start RQLite with cluster discovery
	if err := n.startRQLite(ctx); err != nil {
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Sync WireGuard peers from RQLite (if WG is active on this node)
	n.startWireGuardSyncLoop(ctx)

	// Sync IPFS swarm connections with all cluster peers
	n.startIPFSSwarmSyncLoop(ctx)

	// Register this node in dns_nodes table for deployment routing
	if err := n.registerDNSNode(ctx); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to register DNS node", zap.Error(err))
		// Don't fail startup if DNS registration fails, it will retry on heartbeat
	} else {
		// Start DNS heartbeat to keep node status fresh
		n.startDNSHeartbeat(ctx)

		// Ensure base DNS records exist for this node (self-healing)
		if err := n.ensureBaseDNSRecords(ctx); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure base DNS records", zap.Error(err))
		}
	}

	// Get listen addresses for logging
	var listenAddrs []string
	if n.host != nil {
		for _, addr := range n.host.Addrs() {
			listenAddrs = append(listenAddrs, addr.String())
		}
	}

	// All services started — transition lifecycle: joining → active
	if err := n.lifecycle.TransitionTo(lifecycle.StateActive); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to transition lifecycle to active", zap.Error(err))
	}

	// Publish updated metadata with active lifecycle state
	if n.clusterDiscovery != nil {
		n.clusterDiscovery.UpdateOwnMetadata()
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Network node started successfully",
		zap.String("peer_id", n.GetPeerID()),
		zap.Strings("listen_addrs", listenAddrs),
		zap.String("lifecycle", string(n.lifecycle.State())),
	)

	n.startConnectionMonitoring()

	return nil
}

// Stop stops the node and all its services
func (n *Node) Stop() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Stopping network node")

	// Enter maintenance so peers know we're shutting down
	if n.lifecycle.IsAvailable() {
		if err := n.lifecycle.EnterMaintenance(5 * time.Minute); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to enter maintenance on shutdown", zap.Error(err))
		}
		// Publish maintenance state before tearing down services
		if n.clusterDiscovery != nil {
			n.clusterDiscovery.UpdateOwnMetadata()
		}
	}

	// Stop HTTP Gateway server
	if n.apiGatewayServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = n.apiGatewayServer.Shutdown(ctx)
	}

	// Close Gateway client
	if n.apiGateway != nil {
		n.apiGateway.Close()
	}

	// Stop cluster discovery
	if n.clusterDiscovery != nil {
		n.clusterDiscovery.Stop()
	}

	// Stop peer reconnection loop
	if n.peerDiscoveryCancel != nil {
		n.peerDiscoveryCancel()
	}

	// Stop peer discovery
	n.stopPeerDiscovery()

	// Stop LibP2P host
	if n.host != nil {
		n.host.Close()
	}

	// Stop RQLite
	if n.rqliteAdapter != nil {
		n.rqliteAdapter.Close()
	}
	if n.rqliteManager != nil {
		_ = n.rqliteManager.Stop()
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Network node stopped")
	return nil
}
