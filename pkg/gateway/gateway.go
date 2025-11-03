package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	_ "github.com/rqlite/gorqlite/stdlib"
)

// Config holds configuration for the gateway server
type Config struct {
	ListenAddr      string
	ClientNamespace string
	BootstrapPeers  []string

	// Optional DSN for rqlite database/sql driver, e.g. "http://localhost:4001"
	// If empty, defaults to "http://localhost:4001".
	RQLiteDSN string

	// HTTPS configuration
	EnableHTTPS bool   // Enable HTTPS with ACME (Let's Encrypt)
	DomainName  string // Domain name for HTTPS certificate
	TLSCacheDir string // Directory to cache TLS certificates (default: ~/.debros/tls-cache)

	// Olric cache configuration
	OlricServers []string      // List of Olric server addresses (e.g., ["localhost:3320"]). If empty, defaults to ["localhost:3320"]
	OlricTimeout time.Duration // Timeout for Olric operations (default: 10s)
}

type Gateway struct {
	logger     *logging.ColoredLogger
	cfg        *Config
	client     client.NetworkClient
	startedAt  time.Time
	signingKey *rsa.PrivateKey
	keyID      string

	// rqlite SQL connection and HTTP ORM gateway
	sqlDB     *sql.DB
	ormClient rqlite.Client
	ormHTTP   *rqlite.HTTPGateway

	// Olric cache client
	olricClient *olric.Client

	// Local pub/sub bypass for same-gateway subscribers
	localSubscribers map[string][]*localSubscriber // topic+namespace -> subscribers
	mu               sync.RWMutex
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
		zap.Int("bootstrap_peer_count", len(cliCfg.BootstrapPeers)),
	)

	logger.ComponentInfo(logging.ComponentGeneral, "Creating gateway instance...")
	gw := &Gateway{
		logger:           logger,
		cfg:              cfg,
		client:           c,
		startedAt:        time.Now(),
		localSubscribers: make(map[string][]*localSubscriber),
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Generating RSA signing key...")
	// Generate local RSA signing key for JWKS/JWT (ephemeral for now)
	if key, err := rsa.GenerateKey(rand.Reader, 2048); err == nil {
		gw.signingKey = key
		gw.keyID = "gw-" + strconv.FormatInt(time.Now().Unix(), 10)
		logger.ComponentInfo(logging.ComponentGeneral, "RSA key generated successfully")
	} else {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to generate RSA key; jwks will be empty", zap.Error(err))
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
	olricClient, olricErr := olric.NewClient(olricCfg, logger.Logger)
	if olricErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "failed to initialize Olric cache client; cache endpoints disabled", zap.Error(olricErr))
	} else {
		gw.olricClient = olricClient
		logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client ready",
			zap.Strings("servers", olricCfg.Servers),
			zap.Duration("timeout", olricCfg.Timeout),
		)
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
	if g.client != nil {
		if err := g.client.Disconnect(); err != nil {
			g.logger.ComponentWarn(logging.ComponentClient, "error during client disconnect", zap.Error(err))
		}
	}
	if g.sqlDB != nil {
		_ = g.sqlDB.Close()
	}
	if g.olricClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.olricClient.Close(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral, "error during Olric client close", zap.Error(err))
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
			if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
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

	// Also check bootstrap peers from config
	if cfg := networkClient.Config(); cfg != nil {
		for _, bootstrapAddr := range cfg.BootstrapPeers {
			ma, err := multiaddr.NewMultiaddr(bootstrapAddr)
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
			if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
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
