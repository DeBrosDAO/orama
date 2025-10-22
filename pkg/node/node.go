package node

import (
	"context"
	"fmt"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p"
	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Node represents a network node with RQLite database
type Node struct {
	config *config.Config
	logger *logging.ColoredLogger
	host   host.Host

	rqliteManager *database.RQLiteManager
	rqliteAdapter *database.RQLiteAdapter

	// Peer discovery
	bootstrapCancel context.CancelFunc

	// PubSub
	pubsub *pubsub.ClientAdapter

	// Discovery
	discoveryManager *discovery.Manager
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

	// Create RQLite manager
	n.rqliteManager = database.NewRQLiteManager(&n.config.Database, &n.config.Discovery, n.config.Node.DataDir, n.logger.Logger)

	// Start RQLite
	if err := n.rqliteManager.Start(ctx); err != nil {
		return err
	}

	// Create adapter for sql.DB compatibility
	adapter, err := database.NewRQLiteAdapter(n.rqliteManager)
	if err != nil {
		return fmt.Errorf("failed to create RQLite adapter: %w", err)
	}
	n.rqliteAdapter = adapter

	return nil
}

// hasBootstrapConnections checks if we're connected to any bootstrap peers
func (n *Node) hasBootstrapConnections() bool {
	if n.host == nil || len(n.config.Discovery.BootstrapPeers) == 0 {
		return false
	}

	connectedPeers := n.host.Network().Peers()
	if len(connectedPeers) == 0 {
		return false
	}

	// Parse bootstrap peer IDs
	bootstrapPeerIDs := make(map[peer.ID]bool)
	for _, bootstrapAddr := range n.config.Discovery.BootstrapPeers {
		ma, err := multiaddr.NewMultiaddr(bootstrapAddr)
		if err != nil {
			continue
		}
		peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			continue
		}
		bootstrapPeerIDs[peerInfo.ID] = true
	}

	// Check if any connected peer is a bootstrap peer
	for _, peerID := range connectedPeers {
		if bootstrapPeerIDs[peerID] {
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

// connectToBootstrapPeer connects to a single bootstrap peer
func (n *Node) connectToBootstrapPeer(ctx context.Context, addr string) error {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	// Extract peer info from multiaddr
	peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return fmt.Errorf("failed to extract peer info: %w", err)
	}

	// Avoid dialing ourselves: if the bootstrap address resolves to our own peer ID, skip.
	if n.host != nil && peerInfo.ID == n.host.ID() {
		n.logger.ComponentDebug(logging.ComponentNode, "Skipping bootstrap address because it resolves to self",
			zap.String("addr", addr),
			zap.String("peer_id", peerInfo.ID.String()))
		return nil
	}

	// Log resolved peer info prior to connect
	n.logger.ComponentDebug(logging.ComponentNode, "Resolved bootstrap peer",
		zap.String("peer_id", peerInfo.ID.String()),
		zap.String("addr", addr),
		zap.Int("addr_count", len(peerInfo.Addrs)),
	)

	// Connect to the peer
	if err := n.host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	n.logger.Info("Connected to bootstrap peer",
		zap.String("peer", peerInfo.ID.String()),
		zap.String("addr", addr))

	return nil
}

// connectToBootstrapPeers connects to configured LibP2P bootstrap peers
func (n *Node) connectToBootstrapPeers(ctx context.Context) error {
	if len(n.config.Discovery.BootstrapPeers) == 0 {
		n.logger.ComponentDebug(logging.ComponentNode, "No bootstrap peers configured")
		return nil
	}

	// Use passed context with a reasonable timeout for bootstrap connections
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, bootstrapAddr := range n.config.Discovery.BootstrapPeers {
		if err := n.connectToBootstrapPeer(connectCtx, bootstrapAddr); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect to bootstrap peer",
				zap.String("addr", bootstrapAddr),
				zap.Error(err))
			continue
		}
	}

	return nil
}

// startLibP2P initializes the LibP2P host
func (n *Node) startLibP2P() error {
	n.logger.ComponentInfo(logging.ComponentLibP2P, "Starting LibP2P host")

	// Get listen addresses
	listenAddrs, err := n.config.ParseMultiaddrs()
	if err != nil {
		return fmt.Errorf("failed to parse listen addresses: %w", err)
	}

	// Load or create persistent identity
	identity, err := n.loadOrCreateIdentity()
	if err != nil {
		return fmt.Errorf("failed to load identity: %w", err)
	}

	// Create LibP2P host with persistent identity
	// Build options allowing conditional proxying via Anyone SOCKS5
	var opts []libp2p.Option
	opts = append(opts,
		libp2p.Identity(identity),
		libp2p.ListenAddrs(listenAddrs...),
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultMuxers,
	)

	// TCP transport with optional SOCKS5 dialer override
	opts = append(opts, libp2p.Transport(tcp.NewTCPTransport))

	h, err := libp2p.New(opts...)
	if err != nil {
		return err
	}

	n.host = h

	// Initialize pubsub
	ps, err := libp2ppubsub.NewGossipSub(context.Background(), h,
		libp2ppubsub.WithPeerExchange(true),
	)
	if err != nil {
		return fmt.Errorf("failed to create pubsub: %w", err)
	}

	// Create pubsub adapter with "node" namespace
	n.pubsub = pubsub.NewClientAdapter(ps, n.config.Discovery.NodeNamespace)
	n.logger.Info("Initialized pubsub adapter on namespace", zap.String("namespace", n.config.Discovery.NodeNamespace))

	// Log configured bootstrap peers
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		n.logger.ComponentInfo(logging.ComponentNode, "Configured bootstrap peers",
			zap.Strings("peers", n.config.Discovery.BootstrapPeers))
	} else {
		n.logger.ComponentDebug(logging.ComponentNode, "No bootstrap peers configured")
	}

	// Connect to LibP2P bootstrap peers if configured
	if err := n.connectToBootstrapPeers(context.Background()); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to connect to bootstrap peers", zap.Error(err))
		// Don't fail - continue without bootstrap connections
	}

	// Start exponential backoff reconnection for bootstrap peers
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		bootstrapCtx, cancel := context.WithCancel(context.Background())
		n.bootstrapCancel = cancel

		go func() {
			interval := 5 * time.Second
			consecutiveFailures := 0

			n.logger.ComponentInfo(logging.ComponentNode, "Starting bootstrap peer reconnection with exponential backoff",
				zap.Duration("initial_interval", interval),
				zap.Duration("max_interval", 10*time.Minute))

			for {
				select {
				case <-bootstrapCtx.Done():
					n.logger.ComponentDebug(logging.ComponentNode, "Bootstrap reconnection loop stopped")
					return
				default:
				}

				// Check if we need to attempt connection
				if !n.hasBootstrapConnections() {
					n.logger.ComponentDebug(logging.ComponentNode, "Attempting bootstrap peer connection",
						zap.Duration("current_interval", interval),
						zap.Int("consecutive_failures", consecutiveFailures))

					if err := n.connectToBootstrapPeers(context.Background()); err != nil {
						consecutiveFailures++
						// Calculate next backoff interval
						jitteredInterval := addJitter(interval)
						n.logger.ComponentDebug(logging.ComponentNode, "Bootstrap connection failed, backing off",
							zap.Error(err),
							zap.Duration("next_attempt_in", jitteredInterval),
							zap.Int("consecutive_failures", consecutiveFailures))

						// Sleep with jitter
						select {
						case <-bootstrapCtx.Done():
							return
						case <-time.After(jitteredInterval):
						}

						// Increase interval for next attempt
						interval = calculateNextBackoff(interval)

						// Log interval increases occasionally to show progress
						if consecutiveFailures%5 == 0 {
							n.logger.ComponentInfo(logging.ComponentNode, "Bootstrap connection still failing",
								zap.Int("consecutive_failures", consecutiveFailures),
								zap.Duration("current_interval", interval))
						}
					} else {
						// Success! Reset interval and counters
						if consecutiveFailures > 0 {
							n.logger.ComponentInfo(logging.ComponentNode, "Successfully connected to bootstrap peers",
								zap.Int("failures_overcome", consecutiveFailures))
						}
						interval = 5 * time.Second
						consecutiveFailures = 0

						// Wait 30 seconds before checking connection again
						select {
						case <-bootstrapCtx.Done():
							return
						case <-time.After(30 * time.Second):
						}
					}
				} else {
					// We have bootstrap connections, just wait and check periodically
					select {
					case <-bootstrapCtx.Done():
						return
					case <-time.After(30 * time.Second):
					}
				}
			}
		}()
	}

	// Add bootstrap peers to peerstore for peer exchange
	if len(n.config.Discovery.BootstrapPeers) > 0 {
		n.logger.ComponentInfo(logging.ComponentNode, "Adding bootstrap peers to peerstore")
		for _, bootstrapAddr := range n.config.Discovery.BootstrapPeers {
			if ma, err := multiaddr.NewMultiaddr(bootstrapAddr); err == nil {
				if peerInfo, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
					// Add to peerstore with longer TTL for peer exchange
					n.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour*24)
					n.logger.ComponentDebug(logging.ComponentNode, "Added bootstrap peer to peerstore",
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

	// Stop bootstrap reconnection loop
	if n.bootstrapCancel != nil {
		n.bootstrapCancel()
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

// Starts the network node
func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("Starting network node", zap.String("data_dir", n.config.Node.DataDir))

	// Create data directory
	if err := os.MkdirAll(n.config.Node.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Start RQLite
	if err := n.startRQLite(ctx); err != nil {
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Start LibP2P host
	if err := n.startLibP2P(); err != nil {
		return fmt.Errorf("failed to start LibP2P: %w", err)
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
