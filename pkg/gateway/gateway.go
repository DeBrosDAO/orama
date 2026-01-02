package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/multiformats/go-multiaddr"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"

	_ "github.com/rqlite/gorqlite/stdlib"
)

const (
	olricInitMaxAttempts    = 5
	olricInitInitialBackoff = 500 * time.Millisecond
	olricInitMaxBackoff     = 5 * time.Second
)

// Config holds configuration for the gateway server
type Config struct {
	ListenAddr      string
	ClientNamespace string
	BootstrapPeers  []string
	NodePeerID      string // The node's actual peer ID from its identity file

	// Optional DSN for rqlite database/sql driver, e.g. "http://localhost:4001"
	// If empty, defaults to "http://localhost:4001".
	RQLiteDSN string

	// HTTPS configuration
	EnableHTTPS bool   // Enable HTTPS with ACME (Let's Encrypt)
	DomainName  string // Domain name for HTTPS certificate
	TLSCacheDir string // Directory to cache TLS certificates (default: ~/.orama/tls-cache)

	// Olric cache configuration
	OlricServers []string      // List of Olric server addresses (e.g., ["localhost:3320"]). If empty, defaults to ["localhost:3320"]
	OlricTimeout time.Duration // Timeout for Olric operations (default: 10s)

	// IPFS Cluster configuration
	IPFSClusterAPIURL     string        // IPFS Cluster HTTP API URL (e.g., "http://localhost:9094"). If empty, gateway will discover from node configs
	IPFSAPIURL            string        // IPFS HTTP API URL for content retrieval (e.g., "http://localhost:5001"). If empty, gateway will discover from node configs
	IPFSTimeout           time.Duration // Timeout for IPFS operations (default: 60s)
	IPFSReplicationFactor int           // Replication factor for pins (default: 3)
	IPFSEnableEncryption  bool          // Enable client-side encryption before upload (default: true, discovered from node configs)
}

type Gateway struct {
	logger     *logging.ColoredLogger
	cfg        *Config
	client     client.NetworkClient
	nodePeerID string // The node's actual peer ID from its identity file (overrides client's peer ID)
	startedAt  time.Time

	// rqlite SQL connection and HTTP ORM gateway
	sqlDB     *sql.DB
	ormClient rqlite.Client
	ormHTTP   *rqlite.HTTPGateway

	// Olric cache client
	olricClient *olric.Client
	olricMu     sync.RWMutex

	// IPFS storage client
	ipfsClient ipfs.IPFSClient

	// Local pub/sub bypass for same-gateway subscribers
	localSubscribers map[string][]*localSubscriber // topic+namespace -> subscribers
	mu               sync.RWMutex

	// Serverless function engine
	serverlessEngine   *serverless.Engine
	serverlessRegistry *serverless.Registry
	serverlessInvoker  *serverless.Invoker
	serverlessWSMgr    *serverless.WSManager
	serverlessHandlers *ServerlessHandlers

	// Authentication service
	authService *auth.Service
}

// localSubscriber represents a WebSocket subscriber for local message delivery
type localSubscriber struct {
	msgChan   chan []byte
	namespace string
}

// New creates and initializes a new Gateway instance
func New(logger *logging.ColoredLogger, cfg *Config) (*Gateway, error) {
	logger.ComponentInfo(logging.ComponentGeneral, "Building client config...")

	// Build client config from gateway cfg
	cliCfg := client.DefaultClientConfig(cfg.ClientNamespace)
	if len(cfg.BootstrapPeers) > 0 {
		cliCfg.BootstrapPeers = cfg.BootstrapPeers
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Creating network client...")
	c, err := client.NewClient(cliCfg)
	if err != nil {
		logger.ComponentError(logging.ComponentClient, "failed to create network client", zap.Error(err))
		return nil, err
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Connecting network client...")
	if err := c.Connect(); err != nil {
		logger.ComponentError(logging.ComponentClient, "failed to connect network client", zap.Error(err))
		return nil, err
	}

	logger.ComponentInfo(logging.ComponentClient, "Network client connected",
		zap.String("namespace", cliCfg.AppName),
		zap.Int("peer_count", len(cliCfg.BootstrapPeers)),
	)

	logger.ComponentInfo(logging.ComponentGeneral, "Creating gateway instance...")
	gw := &Gateway{
		logger:           logger,
		cfg:              cfg,
		client:           c,
		nodePeerID:       cfg.NodePeerID,
		startedAt:        time.Now(),
		localSubscribers: make(map[string][]*localSubscriber),
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Initializing RQLite ORM HTTP gateway...")
	dsn := cfg.RQLiteDSN
	if dsn == "" {
		dsn = "http://localhost:5001"
	}
	db, dbErr := sql.Open("rqlite", dsn)
	if dbErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to open rqlite sql db; http orm gateway disabled", zap.Error(dbErr))
	} else {
		// Configure connection pool with proper timeouts and limits
		db.SetMaxOpenConns(25)                 // Maximum number of open connections
		db.SetMaxIdleConns(5)                  // Maximum number of idle connections
		db.SetConnMaxLifetime(5 * time.Minute) // Maximum lifetime of a connection
		db.SetConnMaxIdleTime(2 * time.Minute) // Maximum idle time before closing

		gw.sqlDB = db
		orm := rqlite.NewClient(db)
		gw.ormClient = orm
		gw.ormHTTP = rqlite.NewHTTPGateway(orm, "/v1/db")
		// Set a reasonable timeout for HTTP requests (30 seconds)
		gw.ormHTTP.Timeout = 30 * time.Second
		logger.ComponentInfo(logging.ComponentGeneral, "RQLite ORM HTTP gateway ready",
			zap.String("dsn", dsn),
			zap.String("base_path", "/v1/db"),
			zap.Duration("timeout", gw.ormHTTP.Timeout),
		)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Initializing Olric cache client...")

	// Discover Olric servers dynamically from LibP2P peers if not explicitly configured
	olricServers := cfg.OlricServers
	if len(olricServers) == 0 {
		logger.ComponentInfo(logging.ComponentGeneral, "Olric servers not configured, discovering from LibP2P peers...")
		discovered := discoverOlricServers(c, logger.Logger)
		if len(discovered) > 0 {
			olricServers = discovered
			logger.ComponentInfo(logging.ComponentGeneral, "Discovered Olric servers from LibP2P peers",
				zap.Strings("servers", olricServers))
		} else {
			// Fallback to localhost for local development
			olricServers = []string{"localhost:3320"}
			logger.ComponentInfo(logging.ComponentGeneral, "No Olric servers discovered, using localhost fallback")
		}
	} else {
		logger.ComponentInfo(logging.ComponentGeneral, "Using explicitly configured Olric servers",
			zap.Strings("servers", olricServers))
	}

	olricCfg := olric.Config{
		Servers: olricServers,
		Timeout: cfg.OlricTimeout,
	}
	olricClient, olricErr := initializeOlricClientWithRetry(olricCfg, logger)
	if olricErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize Olric cache client; cache endpoints disabled", zap.Error(olricErr))
		gw.startOlricReconnectLoop(olricCfg)
	} else {
		gw.setOlricClient(olricClient)
		logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client ready",
			zap.Strings("servers", olricCfg.Servers),
			zap.Duration("timeout", olricCfg.Timeout),
		)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Initializing IPFS Cluster client...")

	// Discover IPFS endpoints from node configs if not explicitly configured
	ipfsClusterURL := cfg.IPFSClusterAPIURL
	ipfsAPIURL := cfg.IPFSAPIURL
	ipfsTimeout := cfg.IPFSTimeout
	ipfsReplicationFactor := cfg.IPFSReplicationFactor
	ipfsEnableEncryption := cfg.IPFSEnableEncryption

	if ipfsClusterURL == "" {
		logger.ComponentInfo(logging.ComponentGeneral, "IPFS Cluster URL not configured, discovering from node configs...")
		discovered := discoverIPFSFromNodeConfigs(logger.Logger)
		if discovered.clusterURL != "" {
			ipfsClusterURL = discovered.clusterURL
			ipfsAPIURL = discovered.apiURL
			if discovered.timeout > 0 {
				ipfsTimeout = discovered.timeout
			}
			if discovered.replicationFactor > 0 {
				ipfsReplicationFactor = discovered.replicationFactor
			}
			ipfsEnableEncryption = discovered.enableEncryption
			logger.ComponentInfo(logging.ComponentGeneral, "Discovered IPFS endpoints from node configs",
				zap.String("cluster_url", ipfsClusterURL),
				zap.String("api_url", ipfsAPIURL),
				zap.Bool("encryption_enabled", ipfsEnableEncryption))
		} else {
			// Fallback to localhost defaults
			ipfsClusterURL = "http://localhost:9094"
			ipfsAPIURL = "http://localhost:5001"
			ipfsEnableEncryption = true // Default to true
			logger.ComponentInfo(logging.ComponentGeneral, "No IPFS config found in node configs, using localhost defaults")
		}
	}

	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:5001"
	}
	if ipfsTimeout == 0 {
		ipfsTimeout = 60 * time.Second
	}
	if ipfsReplicationFactor == 0 {
		ipfsReplicationFactor = 3
	}
	if !cfg.IPFSEnableEncryption && !ipfsEnableEncryption {
		// Only disable if explicitly set to false in both places
		ipfsEnableEncryption = false
	} else {
		// Default to true if not explicitly disabled
		ipfsEnableEncryption = true
	}

	ipfsCfg := ipfs.Config{
		ClusterAPIURL: ipfsClusterURL,
		Timeout:       ipfsTimeout,
	}
	ipfsClient, ipfsErr := ipfs.NewClient(ipfsCfg, logger.Logger)
	if ipfsErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize IPFS Cluster client; storage endpoints disabled", zap.Error(ipfsErr))
	} else {
		gw.ipfsClient = ipfsClient

		// Check peer count and warn if insufficient (use background context to avoid blocking)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if peerCount, err := ipfsClient.GetPeerCount(ctx); err == nil {
			if peerCount < ipfsReplicationFactor {
				logger.ComponentWarn(logging.ComponentGeneral, "insufficient cluster peers for replication factor",
					zap.Int("peer_count", peerCount),
					zap.Int("replication_factor", ipfsReplicationFactor),
					zap.String("message", "Some pin operations may fail until more peers join the cluster"))
			} else {
				logger.ComponentInfo(logging.ComponentGeneral, "IPFS Cluster peer count sufficient",
					zap.Int("peer_count", peerCount),
					zap.Int("replication_factor", ipfsReplicationFactor))
			}
		} else {
			logger.ComponentWarn(logging.ComponentGeneral, "failed to get cluster peer count", zap.Error(err))
		}

		logger.ComponentInfo(logging.ComponentGeneral, "IPFS Cluster client ready",
			zap.String("cluster_api_url", ipfsCfg.ClusterAPIURL),
			zap.String("ipfs_api_url", ipfsAPIURL),
			zap.Duration("timeout", ipfsCfg.Timeout),
			zap.Int("replication_factor", ipfsReplicationFactor),
			zap.Bool("encryption_enabled", ipfsEnableEncryption),
		)
	}
	// Store IPFS settings in gateway for use by handlers
	gw.cfg.IPFSAPIURL = ipfsAPIURL
	gw.cfg.IPFSReplicationFactor = ipfsReplicationFactor
	gw.cfg.IPFSEnableEncryption = ipfsEnableEncryption

	// Initialize serverless function engine
	logger.ComponentInfo(logging.ComponentGeneral, "Initializing serverless function engine...")
	if gw.ormClient != nil && gw.ipfsClient != nil {
		// Create serverless registry (stores functions in RQLite + IPFS)
		registryCfg := serverless.RegistryConfig{
			IPFSAPIURL: ipfsAPIURL,
		}
		registry := serverless.NewRegistry(gw.ormClient, gw.ipfsClient, registryCfg, logger.Logger)
		gw.serverlessRegistry = registry

		// Create WebSocket manager for function streaming
		gw.serverlessWSMgr = serverless.NewWSManager(logger.Logger)

		// Get underlying Olric client if available
		var olricClient olriclib.Client
		if oc := gw.getOlricClient(); oc != nil {
			olricClient = oc.UnderlyingClient()
		}

		// Create host functions provider (allows functions to call Orama services)
		// Note: pubsub and secrets are nil for now - can be added later
		hostFuncsCfg := serverless.HostFunctionsConfig{
			IPFSAPIURL:  ipfsAPIURL,
			HTTPTimeout: 30 * time.Second,
		}
		hostFuncs := serverless.NewHostFunctions(
			gw.ormClient,
			olricClient,
			gw.ipfsClient,
			nil, // pubsub adapter - TODO: integrate with gateway pubsub
			gw.serverlessWSMgr,
			nil, // secrets manager - TODO: implement
			hostFuncsCfg,
			logger.Logger,
		)

		// Create WASM engine configuration
		engineCfg := serverless.DefaultConfig()
		engineCfg.DefaultMemoryLimitMB = 128
		engineCfg.MaxMemoryLimitMB = 256
		engineCfg.DefaultTimeoutSeconds = 30
		engineCfg.MaxTimeoutSeconds = 60
		engineCfg.ModuleCacheSize = 100

		// Create WASM engine
		engine, engineErr := serverless.NewEngine(engineCfg, registry, hostFuncs, logger.Logger, serverless.WithInvocationLogger(registry))
		if engineErr != nil {
			logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize serverless engine; functions disabled", zap.Error(engineErr))
		} else {
			gw.serverlessEngine = engine

			// Create invoker
			gw.serverlessInvoker = serverless.NewInvoker(engine, registry, hostFuncs, logger.Logger)

			// Create HTTP handlers
			gw.serverlessHandlers = NewServerlessHandlers(
				gw.serverlessInvoker,
				registry,
				gw.serverlessWSMgr,
				logger.Logger,
			)

			// Initialize auth service
			// For now using ephemeral key, can be loaded from config later
			key, _ := rsa.GenerateKey(rand.Reader, 2048)
			keyPEM := pem.EncodeToMemory(&pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: x509.MarshalPKCS1PrivateKey(key),
			})
			authService, err := auth.NewService(logger, c, string(keyPEM), cfg.ClientNamespace)
			if err != nil {
				logger.ComponentError(logging.ComponentGeneral, "failed to initialize auth service", zap.Error(err))
			} else {
				gw.authService = authService
			}

			logger.ComponentInfo(logging.ComponentGeneral, "Serverless function engine ready",
				zap.Int("default_memory_mb", engineCfg.DefaultMemoryLimitMB),
				zap.Int("default_timeout_sec", engineCfg.DefaultTimeoutSeconds),
				zap.Int("module_cache_size", engineCfg.ModuleCacheSize),
			)
		}
	} else {
		logger.ComponentWarn(logging.ComponentGeneral, "serverless engine requires RQLite and IPFS; functions disabled")
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Gateway creation completed, returning...")
	return gw, nil
}

// withInternalAuth creates a context for internal gateway operations that bypass authentication
func (g *Gateway) withInternalAuth(ctx context.Context) context.Context {
	return client.WithInternalAuth(ctx)
}

// Close disconnects the gateway client
func (g *Gateway) Close() {
	// Close serverless engine first
	if g.serverlessEngine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := g.serverlessEngine.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during serverless engine close", zap.Error(err))
		}
		cancel()
	}
	if g.client != nil {
		if err := g.client.Disconnect(); err != nil {
			g.logger.ComponentWarn(logging.ComponentClient, "error during client disconnect", zap.Error(err))
		}
	}
	if g.sqlDB != nil {
		_ = g.sqlDB.Close()
	}
	if client := g.getOlricClient(); client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during Olric client close", zap.Error(err))
		}
	}
	if g.ipfsClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.ipfsClient.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during IPFS client close", zap.Error(err))
		}
	}
}

// getLocalSubscribers returns all local subscribers for a given topic and namespace
func (g *Gateway) getLocalSubscribers(topic, namespace string) []*localSubscriber {
	topicKey := namespace + "." + topic
	if subs, ok := g.localSubscribers[topicKey]; ok {
		return subs
	}
	return nil
}

func (g *Gateway) setOlricClient(client *olric.Client) {
	g.olricMu.Lock()
	defer g.olricMu.Unlock()
	g.olricClient = client
}

func (g *Gateway) getOlricClient() *olric.Client {
	g.olricMu.RLock()
	defer g.olricMu.RUnlock()
	return g.olricClient
}

func (g *Gateway) startOlricReconnectLoop(cfg olric.Config) {
	go func() {
		retryDelay := 5 * time.Second
		for {
			client, err := initializeOlricClientWithRetry(cfg, g.logger)
			if err == nil {
				g.setOlricClient(client)
				g.logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client connected after background retries",
					zap.Strings("servers", cfg.Servers),
					zap.Duration("timeout", cfg.Timeout))
				return
			}

			g.logger.ComponentWarn(logging.ComponentGeneral, "Olric cache client reconnect failed",
				zap.Duration("retry_in", retryDelay),
				zap.Error(err))

			time.Sleep(retryDelay)
			if retryDelay < olricInitMaxBackoff {
				retryDelay *= 2
				if retryDelay > olricInitMaxBackoff {
					retryDelay = olricInitMaxBackoff
				}
			}
		}
	}()
}

func initializeOlricClientWithRetry(cfg olric.Config, logger *logging.ColoredLogger) (*olric.Client, error) {
	backoff := olricInitInitialBackoff

	for attempt := 1; attempt <= olricInitMaxAttempts; attempt++ {
		client, err := olric.NewClient(cfg, logger.Logger)
		if err == nil {
			if attempt > 1 {
				logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client initialized after retries",
					zap.Int("attempts", attempt))
			}
			return client, nil
		}

		logger.ComponentWarn(logging.ComponentGeneral, "Olric cache client init attempt failed",
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", backoff),
			zap.Error(err))

		if attempt == olricInitMaxAttempts {
			return nil, fmt.Errorf("failed to initialize Olric cache client after %d attempts: %w", attempt, err)
		}

		time.Sleep(backoff)
		backoff *= 2
		if backoff > olricInitMaxBackoff {
			backoff = olricInitMaxBackoff
		}
	}

	return nil, fmt.Errorf("failed to initialize Olric cache client")
}

// discoverOlricServers discovers Olric server addresses from LibP2P peers
// Returns a list of IP:port addresses where Olric servers are expected to run (port 3320)
func discoverOlricServers(networkClient client.NetworkClient, logger *zap.Logger) []string {
	// Get network info to access peer information
	networkInfo := networkClient.Network()
	if networkInfo == nil {
		logger.Debug("Network info not available for Olric discovery")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peers, err := networkInfo.GetPeers(ctx)
	if err != nil {
		logger.Debug("Failed to get peers for Olric discovery", zap.Error(err))
		return nil
	}

	olricServers := make([]string, 0)
	seen := make(map[string]bool)

	for _, peer := range peers {
		for _, addrStr := range peer.Addresses {
			// Parse multiaddr
			ma, err := multiaddr.NewMultiaddr(addrStr)
			if err != nil {
				continue
			}

			// Extract IP address
			var ip string
			if ipv4, err := ma.ValueForProtocol(multiaddr.P_IP4); err == nil && ipv4 != "" {
				ip = ipv4
			} else if ipv6, err := ma.ValueForProtocol(multiaddr.P_IP6); err == nil && ipv6 != "" {
				ip = ipv6
			} else {
				continue
			}

			// Skip localhost loopback addresses (we'll use localhost:3320 as fallback)
			if ip == "localhost" || ip == "::1" {
				continue
			}

			// Build Olric server address (standard port 3320)
			olricAddr := net.JoinHostPort(ip, "3320")
			if !seen[olricAddr] {
				olricServers = append(olricServers, olricAddr)
				seen[olricAddr] = true
			}
		}
	}

	// Also check peers from config
	if cfg := networkClient.Config(); cfg != nil {
		for _, peerAddr := range cfg.BootstrapPeers {
			ma, err := multiaddr.NewMultiaddr(peerAddr)
			if err != nil {
				continue
			}

			var ip string
			if ipv4, err := ma.ValueForProtocol(multiaddr.P_IP4); err == nil && ipv4 != "" {
				ip = ipv4
			} else if ipv6, err := ma.ValueForProtocol(multiaddr.P_IP6); err == nil && ipv6 != "" {
				ip = ipv6
			} else {
				continue
			}

			// Skip localhost
			if ip == "localhost" || ip == "::1" {
				continue
			}

			olricAddr := net.JoinHostPort(ip, "3320")
			if !seen[olricAddr] {
				olricServers = append(olricServers, olricAddr)
				seen[olricAddr] = true
			}
		}
	}

	// If we found servers, log them
	if len(olricServers) > 0 {
		logger.Info("Discovered Olric servers from LibP2P network",
			zap.Strings("servers", olricServers))
	}

	return olricServers
}

// ipfsDiscoveryResult holds discovered IPFS configuration
type ipfsDiscoveryResult struct {
	clusterURL        string
	apiURL            string
	timeout           time.Duration
	replicationFactor int
	enableEncryption  bool
}

// discoverIPFSFromNodeConfigs discovers IPFS configuration from node.yaml files
// Checks node-1.yaml through node-5.yaml for IPFS configuration
func discoverIPFSFromNodeConfigs(logger *zap.Logger) ipfsDiscoveryResult {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Debug("Failed to get home directory for IPFS discovery", zap.Error(err))
		return ipfsDiscoveryResult{}
	}

	configDir := filepath.Join(homeDir, ".orama")

	// Try all node config files for IPFS settings
	configFiles := []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"}

	for _, filename := range configFiles {
		configPath := filepath.Join(configDir, filename)
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}

		var nodeCfg config.Config
		if err := config.DecodeStrict(strings.NewReader(string(data)), &nodeCfg); err != nil {
			logger.Debug("Failed to parse node config for IPFS discovery",
				zap.String("file", filename), zap.Error(err))
			continue
		}

		// Check if IPFS is configured
		if nodeCfg.Database.IPFS.ClusterAPIURL != "" {
			result := ipfsDiscoveryResult{
				clusterURL:        nodeCfg.Database.IPFS.ClusterAPIURL,
				apiURL:            nodeCfg.Database.IPFS.APIURL,
				timeout:           nodeCfg.Database.IPFS.Timeout,
				replicationFactor: nodeCfg.Database.IPFS.ReplicationFactor,
				enableEncryption:  nodeCfg.Database.IPFS.EnableEncryption,
			}

			if result.apiURL == "" {
				result.apiURL = "http://localhost:5001"
			}
			if result.timeout == 0 {
				result.timeout = 60 * time.Second
			}
			if result.replicationFactor == 0 {
				result.replicationFactor = 3
			}
			// Default encryption to true if not set
			if !result.enableEncryption {
				result.enableEncryption = true
			}

			logger.Info("Discovered IPFS config from node config",
				zap.String("file", filename),
				zap.String("cluster_url", result.clusterURL),
				zap.String("api_url", result.apiURL),
				zap.Bool("encryption_enabled", result.enableEncryption))

			return result
		}
	}

	return ipfsDiscoveryResult{}
}
