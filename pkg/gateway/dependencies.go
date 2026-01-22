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
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/hostfunctions"
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

// Dependencies holds all service clients and components required by the Gateway.
// This struct encapsulates external dependencies to support dependency injection and testability.
type Dependencies struct {
	// Client is the network client for P2P communication
	Client client.NetworkClient

	// RQLite database dependencies
	SQLDB     *sql.DB
	ORMClient rqlite.Client
	ORMHTTP   *rqlite.HTTPGateway

	// Olric distributed cache client
	OlricClient *olric.Client

	// IPFS storage client
	IPFSClient ipfs.IPFSClient

	// Serverless function engine components
	ServerlessEngine   *serverless.Engine
	ServerlessRegistry *serverless.Registry
	ServerlessInvoker  *serverless.Invoker
	ServerlessWSMgr    *serverless.WSManager
	ServerlessHandlers *serverlesshandlers.ServerlessHandlers

	// Authentication service
	AuthService *auth.Service
}

// NewDependencies creates and initializes all gateway dependencies based on the provided configuration.
// It establishes connections to RQLite, Olric, IPFS, initializes the serverless engine, and creates
// the authentication service.
func NewDependencies(logger *logging.ColoredLogger, cfg *Config) (*Dependencies, error) {
	deps := &Dependencies{}

	// Create and connect network client
	logger.ComponentInfo(logging.ComponentGeneral, "Building client config...")
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

	deps.Client = c

	// Initialize RQLite ORM HTTP gateway
	if err := initializeRQLite(logger, cfg, deps); err != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "RQLite initialization failed", zap.Error(err))
	}

	// Initialize Olric cache client (with retry and background reconnection)
	initializeOlric(logger, cfg, deps, c)

	// Initialize IPFS Cluster client
	initializeIPFS(logger, cfg, deps)

	// Initialize serverless function engine (requires RQLite and IPFS)
	if err := initializeServerless(logger, cfg, deps, c); err != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "Serverless initialization failed", zap.Error(err))
	}

	return deps, nil
}

// initializeRQLite sets up the RQLite database connection and ORM HTTP gateway
func initializeRQLite(logger *logging.ColoredLogger, cfg *Config, deps *Dependencies) error {
	logger.ComponentInfo(logging.ComponentGeneral, "Initializing RQLite ORM HTTP gateway...")
	dsn := cfg.RQLiteDSN
	if dsn == "" {
		dsn = "http://localhost:5001"
	}

	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open rqlite sql db: %w", err)
	}

	// Configure connection pool with proper timeouts and limits
	db.SetMaxOpenConns(25)                 // Maximum number of open connections
	db.SetMaxIdleConns(5)                  // Maximum number of idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Maximum lifetime of a connection
	db.SetConnMaxIdleTime(2 * time.Minute) // Maximum idle time before closing

	deps.SQLDB = db
	orm := rqlite.NewClient(db)
	deps.ORMClient = orm
	deps.ORMHTTP = rqlite.NewHTTPGateway(orm, "/v1/db")
	// Set a reasonable timeout for HTTP requests (30 seconds)
	deps.ORMHTTP.Timeout = 30 * time.Second

	logger.ComponentInfo(logging.ComponentGeneral, "RQLite ORM HTTP gateway ready",
		zap.String("dsn", dsn),
		zap.String("base_path", "/v1/db"),
		zap.Duration("timeout", deps.ORMHTTP.Timeout),
	)

	return nil
}

// initializeOlric sets up the Olric distributed cache client with retry and background reconnection
func initializeOlric(logger *logging.ColoredLogger, cfg *Config, deps *Dependencies, networkClient client.NetworkClient) {
	logger.ComponentInfo(logging.ComponentGeneral, "Initializing Olric cache client...")

	// Discover Olric servers dynamically from LibP2P peers if not explicitly configured
	olricServers := cfg.OlricServers
	if len(olricServers) == 0 {
		logger.ComponentInfo(logging.ComponentGeneral, "Olric servers not configured, discovering from LibP2P peers...")
		discovered := discoverOlricServers(networkClient, logger.Logger)
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

	olricClient, err := initializeOlricClientWithRetry(olricCfg, logger)
	if err != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize Olric cache client; cache endpoints disabled", zap.Error(err))
		// Note: Background reconnection will be handled by the Gateway itself
	} else {
		deps.OlricClient = olricClient
		logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client ready",
			zap.Strings("servers", olricCfg.Servers),
			zap.Duration("timeout", olricCfg.Timeout),
		)
	}
}

// initializeOlricClientWithRetry attempts to create an Olric client with exponential backoff
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

// initializeIPFS sets up the IPFS Cluster client with automatic endpoint discovery
func initializeIPFS(logger *logging.ColoredLogger, cfg *Config, deps *Dependencies) {
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
		IPFSAPIURL:    ipfsAPIURL,
		Timeout:       ipfsTimeout,
	}

	ipfsClient, err := ipfs.NewClient(ipfsCfg, logger.Logger)
	if err != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize IPFS Cluster client; storage endpoints disabled", zap.Error(err))
		return
	}

	deps.IPFSClient = ipfsClient

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

	// Store IPFS settings back in config for use by handlers
	cfg.IPFSAPIURL = ipfsAPIURL
	cfg.IPFSReplicationFactor = ipfsReplicationFactor
	cfg.IPFSEnableEncryption = ipfsEnableEncryption
}

// initializeServerless sets up the serverless function engine and related components
func initializeServerless(logger *logging.ColoredLogger, cfg *Config, deps *Dependencies, networkClient client.NetworkClient) error {
	logger.ComponentInfo(logging.ComponentGeneral, "Initializing serverless function engine...")

	if deps.ORMClient == nil || deps.IPFSClient == nil {
		return fmt.Errorf("serverless engine requires RQLite and IPFS; functions disabled")
	}

	// Create serverless registry (stores functions in RQLite + IPFS)
	registryCfg := serverless.RegistryConfig{
		IPFSAPIURL: cfg.IPFSAPIURL,
	}
	registry := serverless.NewRegistry(deps.ORMClient, deps.IPFSClient, registryCfg, logger.Logger)
	deps.ServerlessRegistry = registry

	// Create WebSocket manager for function streaming
	deps.ServerlessWSMgr = serverless.NewWSManager(logger.Logger)

	// Get underlying Olric client if available
	var olricClient olriclib.Client
	if deps.OlricClient != nil {
		olricClient = deps.OlricClient.UnderlyingClient()
	}

	// Get pubsub adapter from client for serverless functions
	var pubsubAdapter *pubsub.ClientAdapter
	if networkClient != nil {
		if concreteClient, ok := networkClient.(*client.Client); ok {
			pubsubAdapter = concreteClient.PubSubAdapter()
			if pubsubAdapter != nil {
				logger.ComponentInfo(logging.ComponentGeneral, "pubsub adapter available for serverless functions")
			} else {
				logger.ComponentWarn(logging.ComponentGeneral, "pubsub adapter is nil - serverless pubsub will be unavailable")
			}
		}
	}

	// Create host functions provider (allows functions to call Orama services)
	hostFuncsCfg := hostfunctions.HostFunctionsConfig{
		IPFSAPIURL:  cfg.IPFSAPIURL,
		HTTPTimeout: 30 * time.Second,
	}
	hostFuncs := hostfunctions.NewHostFunctions(
		deps.ORMClient,
		olricClient,
		deps.IPFSClient,
		pubsubAdapter, // pubsub adapter for serverless functions
		deps.ServerlessWSMgr,
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
	engine, err := serverless.NewEngine(engineCfg, registry, hostFuncs, logger.Logger, serverless.WithInvocationLogger(registry))
	if err != nil {
		return fmt.Errorf("failed to initialize serverless engine: %w", err)
	}
	deps.ServerlessEngine = engine

	// Create invoker
	deps.ServerlessInvoker = serverless.NewInvoker(engine, registry, hostFuncs, logger.Logger)

	// Create HTTP handlers
	deps.ServerlessHandlers = serverlesshandlers.NewServerlessHandlers(
		deps.ServerlessInvoker,
		registry,
		deps.ServerlessWSMgr,
		logger.Logger,
	)

	// Initialize auth service
	// For now using ephemeral key, can be loaded from config later
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	authService, err := auth.NewService(logger, networkClient, string(keyPEM), cfg.ClientNamespace)
	if err != nil {
		return fmt.Errorf("failed to initialize auth service: %w", err)
	}
	deps.AuthService = authService

	logger.ComponentInfo(logging.ComponentGeneral, "Serverless function engine ready",
		zap.Int("default_memory_mb", engineCfg.DefaultMemoryLimitMB),
		zap.Int("default_timeout_sec", engineCfg.DefaultTimeoutSeconds),
		zap.Int("module_cache_size", engineCfg.ModuleCacheSize),
	)

	return nil
}

// discoverOlricServers discovers Olric server addresses from LibP2P peers.
// Returns a list of IP:port addresses where Olric servers are expected to run (port 3320).
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

// discoverIPFSFromNodeConfigs discovers IPFS configuration from node.yaml files.
// Checks node-1.yaml through node-5.yaml for IPFS configuration.
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
