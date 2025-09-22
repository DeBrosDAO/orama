package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// Client implements the NetworkClient interface
type Client struct {
	config *ClientConfig

	// Network components
	host     host.Host
	libp2pPS *libp2ppubsub.PubSub
	logger   *zap.Logger

	// Components
	database *DatabaseClientImpl
	network  *NetworkInfoImpl
	pubsub   *pubSubBridge

	// State
	connected bool
	startTime time.Time
	mu        sync.RWMutex

	// resolvedNamespace is the namespace derived from JWT/APIKey.
	resolvedNamespace string
}

// NewClient creates a new network client
func NewClient(config *ClientConfig) (NetworkClient, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.AppName == "" {
		return nil, fmt.Errorf("app name is required")
	}

	// Create zap logger via helper for consistency
	logger, err := newClientLogger(config.QuietMode)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	client := &Client{
		config:    config,
		logger:    logger,
		startTime: time.Now(),
	}

	// Initialize components (will be configured when connected)
	client.database = &DatabaseClientImpl{client: client}
	client.network = &NetworkInfoImpl{client: client}

	return client, nil
}

// Database returns the database client
func (c *Client) Database() DatabaseClient {
	return c.database
}

// PubSub returns the pub/sub client
func (c *Client) PubSub() PubSubClient {
	return c.pubsub
}

// Network returns the network info client
func (c *Client) Network() NetworkInfo {
	return c.network
}

// Config returns a snapshot copy of the client's configuration
func (c *Client) Config() *ClientConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config == nil {
		return nil
	}
	cp := *c.config
	if c.config.BootstrapPeers != nil {
		cp.BootstrapPeers = append([]string(nil), c.config.BootstrapPeers...)
	}
	if c.config.DatabaseEndpoints != nil {
		cp.DatabaseEndpoints = append([]string(nil), c.config.DatabaseEndpoints...)
	}
	return &cp
}

// Connect establishes connection to the network
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Derive and set namespace from provided credentials
	ns, err := c.deriveNamespace()
	if err != nil {
		return fmt.Errorf("failed to derive namespace: %w", err)
	}
	c.resolvedNamespace = ns

	// Create LibP2P host with optional Anyone proxy for TCP and optional QUIC disable
	var opts []libp2p.Option
	opts = append(opts,
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"), // Random port
		libp2p.Security(noise.ID, noise.New),
		libp2p.DefaultMuxers,
	)
	opts = append(opts, libp2p.Transport(tcp.NewTCPTransport))
	// Enable QUIC only when not proxying. When proxy is enabled, prefer TCP via SOCKS5.
	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to create libp2p host: %w", err)
	}

	c.host = h

	// Log host identity and listen addresses
	addrs := c.host.Addrs()
	addrStrs := make([]string, 0, len(addrs))
	for _, a := range addrs {
		addrStrs = append(addrStrs, a.String())
	}
	c.logger.Info("LibP2P host created",
		zap.String("peer_id", c.host.ID().String()),
		zap.Int("listen_addr_count", len(addrStrs)),
		zap.Strings("listen_addrs", addrStrs),
	)

	c.logger.Info("Creating GossipSub...")

	// Create LibP2P GossipSub with PeerExchange enabled (gossip-based peer exchange).
	// Peer exchange helps propagate peer addresses via pubsub gossip and is enabled
	// globally so discovery works without Anchat-specific branches.
	var ps *libp2ppubsub.PubSub
	ps, err = libp2ppubsub.NewGossipSub(context.Background(), h,
		libp2ppubsub.WithPeerExchange(true),
	)
	if err != nil {
		h.Close()
		return fmt.Errorf("failed to create pubsub: %w", err)
	}
	c.libp2pPS = ps
	c.logger.Info("GossipSub created successfully")

	c.logger.Info("Creating pubsub bridge...")

	c.logger.Info("Getting app namespace for pubsub...")
	// Access namespace directly to avoid deadlock (we already hold c.mu.Lock())
	var namespace string
	if c.resolvedNamespace != "" {
		namespace = c.resolvedNamespace
	} else {
		namespace = c.config.AppName
	}
	c.logger.Info("App namespace retrieved", zap.String("namespace", namespace))

	c.logger.Info("Calling pubsub.NewClientAdapter...")
	adapter := pubsub.NewClientAdapter(c.libp2pPS, namespace)
	c.logger.Info("pubsub.NewClientAdapter completed successfully")

	c.logger.Info("Creating pubSubBridge...")
	c.pubsub = &pubSubBridge{client: c, adapter: adapter}
	c.logger.Info("Pubsub bridge created successfully")

	c.logger.Info("Starting bootstrap peer connections...")

	// Connect to bootstrap peers FIRST
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout)
	defer cancel()

	bootstrapPeersConnected := 0
	for _, bootstrapAddr := range c.config.BootstrapPeers {
		c.logger.Info("Attempting to connect to bootstrap peer", zap.String("addr", bootstrapAddr))
		if err := c.connectToBootstrap(ctx, bootstrapAddr); err != nil {
			c.logger.Warn("Failed to connect to bootstrap peer",
				zap.String("addr", bootstrapAddr),
				zap.Error(err))
			continue
		}
		bootstrapPeersConnected++
		c.logger.Info("Successfully connected to bootstrap peer", zap.String("addr", bootstrapAddr))
	}

	if bootstrapPeersConnected == 0 {
		c.logger.Warn("No bootstrap peers connected, continuing anyway")
	} else {
		c.logger.Info("Bootstrap peer connections completed", zap.Int("connected_count", bootstrapPeersConnected))
	}

	c.logger.Info("Adding bootstrap peers to peerstore...")

	// Add bootstrap peers to peerstore so we can connect to them later
	for _, bootstrapAddr := range c.config.BootstrapPeers {
		if ma, err := multiaddr.NewMultiaddr(bootstrapAddr); err == nil {
			if peerInfo, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
				c.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour*24)
				c.logger.Debug("Added bootstrap peer to peerstore",
					zap.String("peer", peerInfo.ID.String()))
			}
		}
	}
	c.logger.Info("Bootstrap peers added to peerstore")

	c.logger.Info("Starting connection monitoring...")

	// Client is a lightweight P2P participant - no discovery needed
	// We only connect to known bootstrap peers and let nodes handle discovery
	c.logger.Debug("Client configured as lightweight P2P participant (no discovery)")

	// Start minimal connection monitoring
	c.logger.Info("Connection monitoring started")

	c.logger.Info("Setting connected state...")

	c.connected = true
	c.logger.Info("Connected state set to true")

	c.logger.Info("Client connected", zap.String("namespace", namespace))

	return nil
}

// Disconnect closes the connection to the network
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	// Close pubsub adapter
	if c.pubsub != nil && c.pubsub.adapter != nil {
		if err := c.pubsub.adapter.Close(); err != nil {
			c.logger.Error("Failed to close pubsub adapter", zap.Error(err))
		}
		c.pubsub = nil
	}

	// Close LibP2P host
	if c.host != nil {
		if err := c.host.Close(); err != nil {
			c.logger.Error("Failed to close host", zap.Error(err))
		}
	}

	c.connected = false

	c.logger.Info("Client disconnected")

	return nil
}

// Health returns the current health status
func (c *Client) Health() (*HealthStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := "healthy"
	if !c.connected {
		status = "unhealthy"
	}

	checks := map[string]string{
		"connection": "ok",
		"database":   "ok",
		"pubsub":     "ok",
	}

	if !c.connected {
		checks["connection"] = "disconnected"
	}

	return &HealthStatus{
		Status:       status,
		Checks:       checks,
		LastUpdated:  time.Now(),
		ResponseTime: time.Millisecond * 10, // Simulated
	}, nil
}

// isConnected checks if the client is connected
func (c *Client) isConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// getAppNamespace returns the namespace for this app
func (c *Client) getAppNamespace() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.resolvedNamespace != "" {
		return c.resolvedNamespace
	}
	return c.config.AppName
}

// requireAccess enforces that credentials are present and that any context-based namespace overrides match
func (c *Client) requireAccess(ctx context.Context) error {
	// Allow internal system operations to bypass authentication
	if IsInternalContext(ctx) {
		return nil
	}

	cfg := c.Config()
	if cfg == nil || (strings.TrimSpace(cfg.APIKey) == "" && strings.TrimSpace(cfg.JWT) == "") {
		return fmt.Errorf("access denied: API key or JWT required")
	}
	ns := c.getAppNamespace()
	if v := ctx.Value(pubsub.CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" && s != ns {
			return fmt.Errorf("access denied: namespace mismatch")
		}
	}
	return nil
}

// deriveNamespace determines the namespace from JWT or API key.
func (c *Client) deriveNamespace() (string, error) {
	// Prefer JWT claim {"Namespace": "..."}
	if strings.TrimSpace(c.config.JWT) != "" {
		ns, err := parseJWTNamespace(c.config.JWT)
		if err != nil {
			return "", err
		}
		if ns != "" {
			return ns, nil
		}
	}
	// Fallback to API key format ak_<random>:<namespace>
	if strings.TrimSpace(c.config.APIKey) != "" {
		ns, err := parseAPIKeyNamespace(c.config.APIKey)
		if err != nil {
			return "", err
		}
		if ns != "" {
			return ns, nil
		}
	}
	return c.config.AppName, nil
}

// parseJWTNamespace decodes base64url payload to extract Namespace claim (no signature verification)
func parseJWTNamespace(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT format")
	}
	payload := parts[1]
	// Decode base64url (raw, no padding)
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT payload: %w", err)
	}
	// Minimal JSON struct
	var claims struct {
		Namespace string `json:"Namespace"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT claims: %w", err)
	}
	return strings.TrimSpace(claims.Namespace), nil
}

// parseAPIKeyNamespace extracts the namespace from ak_<random>:<namespace>
func parseAPIKeyNamespace(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("invalid API key: empty")
	}
	// Allow but ignore prefix ak_
	parts := strings.Split(key, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid API key format: expected ak_<random>:<namespace>")
	}
	ns := strings.TrimSpace(parts[1])
	if ns == "" {
		return "", fmt.Errorf("invalid API key: empty namespace")
	}
	return ns, nil
}
