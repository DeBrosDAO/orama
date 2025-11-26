package node

import (
	"context"
	"fmt"
	mathrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Node represents a network node with RQLite database
type Node struct {
	config *config.Config
	logger *logging.ColoredLogger
	host   host.Host

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

	// HTTP reverse proxy gateway
	httpGateway *gateway.HTTPGateway
}

// NewNode creates a new network node
func NewNode(cfg *config.Config) (*Node, error) {
	// Create colored logger
	logger, err := logging.NewColoredLogger(logging.ComponentNode, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	return &Node{
		config: cfg,
		logger: logger,
	}, nil
}

// startRQLite initializes and starts the RQLite database
func (n *Node) startRQLite(ctx context.Context) error {
	n.logger.Info("Starting RQLite database")

	// Determine node identifier for log filename - use node ID for unique filenames
	nodeID := n.config.Node.ID
	if nodeID == "" {
		// Default to "node" if ID is not set
		nodeID = "node"
	}

	// Create RQLite manager
	n.rqliteManager = database.NewRQLiteManager(&n.config.Database, &n.config.Discovery, n.config.Node.DataDir, n.logger.Logger)
	n.rqliteManager.SetNodeType(nodeID)

	// Initialize cluster discovery service if LibP2P host is available
	if n.host != nil && n.discoveryManager != nil {
		// Create cluster discovery service (all nodes are unified)
		n.clusterDiscovery = database.NewClusterDiscoveryService(
			n.host,
			n.discoveryManager,
			n.rqliteManager,
			n.config.Node.ID,
			"node", // Unified node type
			n.config.Discovery.RaftAdvAddress,
			n.config.Discovery.HttpAdvAddress,
			n.config.Node.DataDir,
			n.logger.Logger,
		)

		// Set discovery service on RQLite manager BEFORE starting RQLite
		// This is critical for pre-start cluster discovery during recovery
		n.rqliteManager.SetDiscoveryService(n.clusterDiscovery)

		// Start cluster discovery (but don't trigger initial sync yet)
		if err := n.clusterDiscovery.Start(ctx); err != nil {
			return fmt.Errorf("failed to start cluster discovery: %w", err)
		}

		// Publish initial metadata (with log_index=0) so peers can discover us during recovery
		// The metadata will be updated with actual log index after RQLite starts
		n.clusterDiscovery.UpdateOwnMetadata()

		n.logger.Info("Cluster discovery service started (waiting for RQLite)")
	}

	// Start RQLite FIRST before updating metadata
	if err := n.rqliteManager.Start(ctx); err != nil {
		return err
	}

	// NOW update metadata after RQLite is running
	if n.clusterDiscovery != nil {
		n.clusterDiscovery.UpdateOwnMetadata()
		n.clusterDiscovery.TriggerSync() // Do initial cluster sync now that RQLite is ready
		n.logger.Info("RQLite metadata published and cluster synced")
	}

	// Create adapter for sql.DB compatibility
	adapter, err := database.NewRQLiteAdapter(n.rqliteManager)
	if err != nil {
		return fmt.Errorf("failed to create RQLite adapter: %w", err)
	}
	n.rqliteAdapter = adapter

	return nil
}

// extractIPFromMultiaddr extracts the IP address from a peer multiaddr
// Supports IP4, IP6, DNS4, DNS6, and DNSADDR protocols
func extractIPFromMultiaddr(multiaddrStr string) string {
	ma, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return ""
	}

	// First, try to extract direct IP address
	var ip string
	var dnsName string
	multiaddr.ForEach(ma, func(c multiaddr.Component) bool {
		switch c.Protocol().Code {
		case multiaddr.P_IP4, multiaddr.P_IP6:
			ip = c.Value()
			return false // Stop iteration - found IP
		case multiaddr.P_DNS4, multiaddr.P_DNS6, multiaddr.P_DNSADDR:
			dnsName = c.Value()
			// Continue to check for IP, but remember DNS name as fallback
		}
		return true
	})

	// If we found a direct IP, return it
	if ip != "" {
		return ip
	}

	// If we found a DNS name, try to resolve it
	if dnsName != "" {
		if resolvedIPs, err := net.LookupIP(dnsName); err == nil && len(resolvedIPs) > 0 {
			// Prefer IPv4 addresses, but accept IPv6 if that's all we have
			for _, resolvedIP := range resolvedIPs {
				if resolvedIP.To4() != nil {
					return resolvedIP.String()
				}
			}
			// Return first IPv6 address if no IPv4 found
			return resolvedIPs[0].String()
		}
	}

	return ""
}

// peerSource returns a PeerSource that yields peers from configured peers.
func peerSource(peerAddrs []string, logger *zap.Logger) func(context.Context, int) <-chan peer.AddrInfo {
	return func(ctx context.Context, num int) <-chan peer.AddrInfo {
		out := make(chan peer.AddrInfo, num)
		go func() {
			defer close(out)
			count := 0
			for _, s := range peerAddrs {
				if count >= num {
					return
				}
				ma, err := multiaddr.NewMultiaddr(s)
				if err != nil {
					logger.Debug("invalid peer multiaddr", zap.String("addr", s), zap.Error(err))
					continue
				}
				ai, err := peer.AddrInfoFromP2pAddr(ma)
				if err != nil {
					logger.Debug("failed to parse peer address", zap.String("addr", s), zap.Error(err))
					continue
				}
				select {
				case out <- *ai:
					count++
				case <-ctx.Done():
					return
				}
			}
		}()
		return out
	}
}

// hasPeerConnections checks if we're connected to any peers
func (n *Node) hasPeerConnections() bool {
	if n.host == nil || len(n.config.Discovery.BootstrapPeers) == 0 {
		return false
	}

	connectedPeers := n.host.Network().Peers()
	if len(connectedPeers) == 0 {
		return false
	}

	// Parse peer IDs
	peerIDs := make(map[peer.ID]bool)
	for _, peerAddr := range n.config.Discovery.BootstrapPeers {
		ma, err := multiaddr.NewMultiaddr(peerAddr)
		if err != nil {
			continue
		}
		peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			continue
		}
		peerIDs[peerInfo.ID] = true
	}

	// Check if any connected peer is in our peer list
	for _, peerID := range connectedPeers {
		if peerIDs[peerID] {
			return true
		}
	}

	return false
}

// calculateNextBackoff calculates the next backoff interval with exponential growth
func calculateNextBackoff(current time.Duration) time.Duration {
	// Multiply by 1.5 for gentler exponential growth
	next := time.Duration(float64(current) * 1.5)
	// Cap at 10 minutes
	maxInterval := 10 * time.Minute
	if next > maxInterval {
		next = maxInterval
	}
	return next
}

// addJitter adds random jitter to prevent thundering herd
func addJitter(interval time.Duration) time.Duration {
	// Add ±20% jitter
	jitterPercent := 0.2
	jitterRange := float64(interval) * jitterPercent
	jitter := (mathrand.Float64() - 0.5) * 2 * jitterRange // -jitterRange to +jitterRange

	result := time.Duration(float64(interval) + jitter)
	// Ensure we don't go below 1 second
	if result < time.Second {
		result = time.Second
	}
	return result
}

// connectToPeerAddr connects to a single peer address
func (n *Node) connectToPeerAddr(ctx context.Context, addr string) error {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info from multiaddr
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return fmt.Errorf("failed to extract peer info: %w", err)
	}

	// Avoid dialing ourselves: if the address resolves to our own peer ID, skip.
	if n.host != nil && peerInfo.ID == n.host.ID() {
		n.logger.ComponentDebug(logging.ComponentNode, "Skipping peer address because it resolves to self",
			zap.String("addr", addr),
			zap.String("peer_id", peerInfo.ID.String()))
		return nil
	}

	// Log resolved peer info prior to connect
	n.logger.ComponentDebug(logging.ComponentNode, "Resolved peer",
		zap.String("peer_id", peerInfo.ID.String()),
		zap.String("addr", addr),
		zap.Int("addr_count", len(peerInfo.Addrs)),
	)

	// Connect to the peer
	if err := n.host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	n.logger.Info("Connected to peer",
		zap.String("peer", peerInfo.ID.String()),
		zap.String("addr", addr))

	return nil
}

// connectToPeers connects to configured LibP2P peers
func (n *Node) connectToPeers(ctx context.Context) error {
	if len(n.config.Discovery.BootstrapPeers) == 0 {
		n.logger.ComponentDebug(logging.ComponentNode, "No peers configured")
		return nil
	}

	// Use passed context with a reasonable timeout for peer connections
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, peerAddr := range n.config.Discovery.BootstrapPeers {
		if err := n.connectToPeerAddr(connectCtx, peerAddr); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect to peer",
				zap.String("addr", peerAddr),
				zap.Error(err))
			continue
		}
	}

	return nil
}

// startLibP2P initializes the LibP2P host
func (n *Node) startLibP2P() error {
	n.logger.ComponentInfo(logging.ComponentLibP2P, "Starting LibP2P host")

	// Load or create persistent identity
	identity, err := n.loadOrCreateIdentity()
	if err != nil {
		return fmt.Errorf("failed to load identity: %w", err)
	}

	// Create LibP2P host with explicit listen addresses
	var opts []libp2p.Option
	opts = append(opts,
		libp2p.Identity(identity),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultMuxers,
	)

	// Add explicit listen addresses from config
	if len(n.config.Node.ListenAddresses) > 0 {
		listenAddrs := make([]multiaddr.Multiaddr, 0, len(n.config.Node.ListenAddresses))
		for _, addr := range n.config.Node.ListenAddresses {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				return fmt.Errorf("invalid listen address %s: %w", addr, err)
			}
			listenAddrs = append(listenAddrs, ma)
		}
		opts = append(opts, libp2p.ListenAddrs(listenAddrs...))
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Configured listen addresses",
			zap.Strings("addrs", n.config.Node.ListenAddresses))
	}

	// For localhost/development, disable NAT services
	// For production, these would be enabled
	isLocalhost := len(n.config.Node.ListenAddresses) > 0 &&
		(strings.Contains(n.config.Node.ListenAddresses[0], "localhost") ||
			strings.Contains(n.config.Node.ListenAddresses[0], "127.0.0.1"))

	if isLocalhost {
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Localhost detected - disabling NAT services for local development")
		// Don't add NAT/AutoRelay options for localhost
	} else {
		n.logger.ComponentInfo(logging.ComponentLibP2P, "Production mode - enabling NAT services")
		opts = append(opts,
			libp2p.EnableNATService(),
			libp2p.EnableAutoNATv2(),
			libp2p.EnableRelay(),
			libp2p.NATPortMap(),
			libp2p.EnableAutoRelayWithPeerSource(
				peerSource(n.config.Discovery.BootstrapPeers, n.logger.Logger),
			),
		)
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return err
	}

	n.host = h

	// Initialize pubsub
	ps, err := libp2ppubsub.NewGossipSub(context.Background(), h,
		libp2ppubsub.WithPeerExchange(true),
		libp2ppubsub.WithFloodPublish(true), // Ensure messages reach all peers, not just mesh
		libp2ppubsub.WithDirectPeers(nil),   // Enable direct peer connections
	)
	if err != nil {
		return fmt.Errorf("failed to create pubsub: %w", err)
	}

	// Create pubsub adapter with "node" namespace
	n.pubsub = pubsub.NewClientAdapter(ps, n.config.Discovery.NodeNamespace)
	n.logger.Info("Initialized pubsub adapter on namespace", zap.String("namespace", n.config.Discovery.NodeNamespace))

	// Log configured peers
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		n.logger.ComponentInfo(logging.ComponentNode, "Configured peers",
			zap.Strings("peers", n.config.Discovery.BootstrapPeers))
	} else {
		n.logger.ComponentDebug(logging.ComponentNode, "No peers configured")
	}

	// Connect to LibP2P peers if configured
	if err := n.connectToPeers(context.Background()); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect to peers", zap.Error(err))
		// Don't fail - continue without peer connections
	}

	// Start exponential backoff reconnection for peers
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		peerCtx, cancel := context.WithCancel(context.Background())
		n.peerDiscoveryCancel = cancel

		go func() {
			interval := 5 * time.Second
			consecutiveFailures := 0

			n.logger.ComponentInfo(logging.ComponentNode, "Starting peer reconnection with exponential backoff",
				zap.Duration("initial_interval", interval),
				zap.Duration("max_interval", 10*time.Minute))

			for {
				select {
				case <-peerCtx.Done():
					n.logger.ComponentDebug(logging.ComponentNode, "Peer reconnection loop stopped")
					return
				default:
				}

				// Check if we need to attempt connection
				if !n.hasPeerConnections() {
					n.logger.ComponentDebug(logging.ComponentNode, "Attempting peer connection",
						zap.Duration("current_interval", interval),
						zap.Int("consecutive_failures", consecutiveFailures))

					if err := n.connectToPeers(context.Background()); err != nil {
						consecutiveFailures++
						// Calculate next backoff interval
						jitteredInterval := addJitter(interval)
						n.logger.ComponentDebug(logging.ComponentNode, "Peer connection failed, backing off",
							zap.Error(err),
							zap.Duration("next_attempt_in", jitteredInterval),
							zap.Int("consecutive_failures", consecutiveFailures))

						// Sleep with jitter
						select {
						case <-peerCtx.Done():
							return
						case <-time.After(jitteredInterval):
						}

						// Increase interval for next attempt
						interval = calculateNextBackoff(interval)

						// Log interval increases occasionally to show progress
						if consecutiveFailures%5 == 0 {
							n.logger.ComponentInfo(logging.ComponentNode, "Peer connection still failing",
								zap.Int("consecutive_failures", consecutiveFailures),
								zap.Duration("current_interval", interval))
						}
					} else {
						// Success! Reset interval and counters
						if consecutiveFailures > 0 {
							n.logger.ComponentInfo(logging.ComponentNode, "Successfully connected to peers",
								zap.Int("failures_overcome", consecutiveFailures))
						}
						interval = 5 * time.Second
						consecutiveFailures = 0

						// Wait 30 seconds before checking connection again
						select {
						case <-peerCtx.Done():
							return
						case <-time.After(30 * time.Second):
						}
					}
				} else {
					// We have peer connections, just wait and check periodically
					select {
					case <-peerCtx.Done():
						return
					case <-time.After(30 * time.Second):
					}
				}
			}
		}()
	}

	// Add peers to peerstore for peer exchange
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		n.logger.ComponentInfo(logging.ComponentNode, "Adding peers to peerstore")
		for _, peerAddr := range n.config.Discovery.BootstrapPeers {
			if ma, err := multiaddr.NewMultiaddr(peerAddr); err == nil {
				if peerInfo, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
					// Add to peerstore with longer TTL for peer exchange
					n.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour*24)
					n.logger.ComponentDebug(logging.ComponentNode, "Added peer to peerstore",
						zap.String("peer", peerInfo.ID.String()))
				}
			}
		}
	}

	// Initialize discovery manager with peer exchange protocol
	n.discoveryManager = discovery.NewManager(h, nil, n.logger.Logger)
	n.discoveryManager.StartProtocolHandler()

	n.logger.ComponentInfo(logging.ComponentNode, "LibP2P host started successfully - using active peer exchange discovery")

	// Start peer discovery and monitoring
	n.startPeerDiscovery()

	n.logger.ComponentInfo(logging.ComponentLibP2P, "LibP2P host started",
		zap.String("peer_id", h.ID().String()))

	return nil
}

// loadOrCreateIdentity loads an existing identity or creates a new one
// loadOrCreateIdentity loads an existing identity or creates a new one
func (n *Node) loadOrCreateIdentity() (crypto.PrivKey, error) {
	identityFile := filepath.Join(n.config.Node.DataDir, "identity.key")

	// Expand ~ in data directory path
	identityFile = os.ExpandEnv(identityFile)
	if strings.HasPrefix(identityFile, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine home directory: %w", err)
		}
		identityFile = filepath.Join(home, identityFile[1:])
	}

	// Try to load existing identity using the shared package
	if _, err := os.Stat(identityFile); err == nil {
		info, err := encryption.LoadIdentity(identityFile)
		if err != nil {
			n.logger.Warn("Failed to load existing identity, creating new one", zap.Error(err))
		} else {
			n.logger.ComponentInfo(logging.ComponentNode, "Loaded existing identity",
				zap.String("file", identityFile),
				zap.String("peer_id", info.PeerID.String()))
			return info.PrivateKey, nil
		}
	}

	// Create new identity using shared package
	n.logger.Info("Creating new identity", zap.String("file", identityFile))
	info, err := encryption.GenerateIdentity()
	if err != nil {
		return nil, fmt.Errorf("failed to generate identity: %w", err)
	}

	// Save identity using shared package
	if err := encryption.SaveIdentity(info, identityFile); err != nil {
		return nil, fmt.Errorf("failed to save identity: %w", err)
	}

	n.logger.Info("Identity saved",
		zap.String("file", identityFile),
		zap.String("peer_id", info.PeerID.String()))

	return info.PrivateKey, nil
}

// GetPeerID returns the peer ID of this node
func (n *Node) GetPeerID() string {
	if n.host == nil {
		return ""
	}
	return n.host.ID().String()
}

// startPeerDiscovery starts periodic peer discovery for the node
func (n *Node) startPeerDiscovery() {
	if n.discoveryManager == nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Discovery manager not initialized")
		return
	}

	// Start the discovery manager with config from node config
	discoveryConfig := discovery.Config{
		DiscoveryInterval: n.config.Discovery.DiscoveryInterval,
		MaxConnections:    n.config.Node.MaxConnections,
	}

	if err := n.discoveryManager.Start(discoveryConfig); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to start discovery manager", zap.Error(err))
		return
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Peer discovery manager started",
		zap.Duration("interval", discoveryConfig.DiscoveryInterval),
		zap.Int("max_connections", discoveryConfig.MaxConnections))
}

// stopPeerDiscovery stops peer discovery
func (n *Node) stopPeerDiscovery() {
	if n.discoveryManager != nil {
		n.discoveryManager.Stop()
	}
	n.logger.ComponentInfo(logging.ComponentNode, "Peer discovery stopped")
}

// getListenAddresses returns the current listen addresses as strings
// Stop stops the node and all its services
func (n *Node) Stop() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Stopping network node")

	// Stop HTTP Gateway
	if n.httpGateway != nil {
		_ = n.httpGateway.Stop()
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

// startHTTPGateway initializes and starts the HTTP reverse proxy gateway
func (n *Node) startHTTPGateway(ctx context.Context) error {
	if !n.config.HTTPGateway.Enabled {
		n.logger.ComponentInfo(logging.ComponentNode, "HTTP Gateway disabled in config")
		return nil
	}

	// Create separate logger for unified gateway
	logFile := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..", "logs", fmt.Sprintf("gateway-%s.log", n.config.HTTPGateway.NodeName))

	// Ensure logs directory exists
	logsDir := filepath.Dir(logFile)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	httpGatewayLogger, err := logging.NewFileLogger(logging.ComponentGeneral, logFile, false)
	if err != nil {
		return fmt.Errorf("failed to create HTTP gateway logger: %w", err)
	}

	// Create and start HTTP gateway with its own logger
	n.httpGateway, err = gateway.NewHTTPGateway(httpGatewayLogger, &n.config.HTTPGateway)
	if err != nil {
		return fmt.Errorf("failed to create HTTP gateway: %w", err)
	}

	// Start gateway in a goroutine (it handles its own lifecycle)
	go func() {
		if err := n.httpGateway.Start(ctx); err != nil {
			n.logger.ComponentError(logging.ComponentNode, "HTTP Gateway error", zap.Error(err))
		}
	}()

	return nil
}

// Starts the network node
func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("Starting network node", zap.String("data_dir", n.config.Node.DataDir))

	// Expand ~ in data directory path
	dataDir := n.config.Node.DataDir
	dataDir = os.ExpandEnv(dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Create data directory
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Start HTTP Gateway first (doesn't depend on other services)
	if err := n.startHTTPGateway(ctx); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to start HTTP Gateway", zap.Error(err))
		// Don't fail node startup if gateway fails
	}

	// Start LibP2P host first (needed for cluster discovery)
	if err := n.startLibP2P(); err != nil {
		return fmt.Errorf("failed to start LibP2P: %w", err)
	}

	// Initialize IPFS Cluster configuration if enabled
	if n.config.Database.IPFS.ClusterAPIURL != "" {
		if err := n.startIPFSClusterConfig(); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to initialize IPFS Cluster config", zap.Error(err))
			// Don't fail node startup if cluster config fails
		}
	}

	// Start RQLite with cluster discovery
	if err := n.startRQLite(ctx); err != nil {
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Get listen addresses for logging
	var listenAddrs []string
	for _, addr := range n.host.Addrs() {
		listenAddrs = append(listenAddrs, addr.String())
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Network node started successfully",
		zap.String("peer_id", n.host.ID().String()),
		zap.Strings("listen_addrs", listenAddrs),
	)

	n.startConnectionMonitoring()

	return nil
}

// startIPFSClusterConfig initializes and ensures IPFS Cluster configuration
func (n *Node) startIPFSClusterConfig() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Initializing IPFS Cluster configuration")

	// Create config manager
	cm, err := ipfs.NewClusterConfigManager(n.config, n.logger.Logger)
	if err != nil {
		return fmt.Errorf("failed to create cluster config manager: %w", err)
	}
	n.clusterConfigManager = cm

	// Fix IPFS config addresses (localhost -> 127.0.0.1) before ensuring cluster config
	if err := cm.FixIPFSConfigAddresses(); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to fix IPFS config addresses", zap.Error(err))
		// Don't fail startup if config fix fails - cluster config will handle it
	}

	// Ensure configuration exists and is correct
	if err := cm.EnsureConfig(); err != nil {
		return fmt.Errorf("failed to ensure cluster config: %w", err)
	}

	// Try to repair peer configuration automatically
	// This will be retried periodically if peer is not available yet
	if success, err := cm.RepairPeerConfiguration(); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to repair peer configuration, will retry later", zap.Error(err))
	} else if success {
		n.logger.ComponentInfo(logging.ComponentNode, "Peer configuration repaired successfully")
	} else {
		n.logger.ComponentDebug(logging.ComponentNode, "Peer not available yet, will retry periodically")
	}

	n.logger.ComponentInfo(logging.ComponentNode, "IPFS Cluster configuration initialized")
	return nil
}
