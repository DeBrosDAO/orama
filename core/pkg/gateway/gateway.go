// Package gateway provides the main API Gateway for the Orama Network.
// It orchestrates traffic between clients and various backend services including
// distributed caching (Olric), decentralized storage (IPFS), and serverless
// WebAssembly (WASM) execution. The gateway implements robust security through
// wallet-based cryptographic authentication and JWT lifecycle management.
package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/health"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	authhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/cache"
	deploymentshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/deployments"
	pubsubhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/pubsub"
	pushhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/push"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	enrollhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/enroll"
	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
	webrtchandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/webrtc"
	operatorhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	vaulthandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/vault"
	wireguardhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/wireguard"
	ratelimithandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/ratelimit"
	sqlitehandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/sqlite"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	nodehealth "github.com/DeBrosOfficial/network/pkg/node/health"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/ratelimit"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"github.com/DeBrosOfficial/network/pkg/turn"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)


type Gateway struct {
	logger     *logging.ColoredLogger
	cfg        *Config
	client     client.NetworkClient
	nodePeerID       string // The node's actual peer ID from its identity file (overrides client's peer ID)
	localWireGuardIP string // WireGuard IP of this node, used to prefer local namespace gateways
	startedAt        time.Time

	// rqlite SQL connection and HTTP ORM gateway
	sqlDB     *sql.DB
	ormClient rqlite.Client
	ormHTTP   *rqlite.HTTPGateway

	// Global RQLite client for API key validation (namespace gateways only)
	authClient client.NetworkClient

	// Authenticated anonymity tunnel (bugboard #168). tunnelLimiter bounds
	// concurrent tunnels per user and per node; tunnelIsolationSecret keys the
	// HMAC that turns a caller identity into an opaque per-user SOCKS circuit
	// selector, so the anonymity client never receives a wallet address.
	tunnelLimiter         *tunnelLimiter
	tunnelIsolationSecret string

	// Olric cache client
	olricClient *olric.Client
	olricMu     sync.RWMutex
	cacheHandlers *cache.CacheHandlers

	// Health check result cache (5s TTL)
	healthCacheMu sync.RWMutex
	healthCache   *cachedHealthResult

	// IPFS storage client
	ipfsClient      ipfs.IPFSClient
	storageHandlers *storage.Handlers

	// Local pub/sub bypass for same-gateway subscribers
	localSubscribers map[string][]*localSubscriber // topic+namespace -> subscribers
	presenceMembers  map[string][]PresenceMember   // topicKey -> members
	mu               sync.RWMutex
	presenceMu       sync.RWMutex
	pubsubHandlers   *pubsubhandlers.PubSubHandlers
	pushHandlers     *pushhandlers.Handlers

	// Serverless function engine
	serverlessEngine     *serverless.Engine
	serverlessRegistry   *serverless.Registry
	serverlessInvoker    *serverless.Invoker
	serverlessWSMgr      *serverless.WSManager
	serverlessHandlers   *serverlesshandlers.ServerlessHandlers
	pubsubDispatcher     *triggers.PubSubDispatcher
	persistentWSManager  *persistent.Manager
	cronScheduler        *triggers.CronScheduler

	// Authentication service
	authService  *auth.Service
	authHandlers *authhandlers.Handlers

	// Deployment system
	deploymentService    *deploymentshandlers.DeploymentService
	staticHandler        *deploymentshandlers.StaticDeploymentHandler
	nextjsHandler        *deploymentshandlers.NextJSHandler
	goHandler            *deploymentshandlers.GoHandler
	nodejsHandler        *deploymentshandlers.NodeJSHandler
	listHandler          *deploymentshandlers.ListHandler
	updateHandler        *deploymentshandlers.UpdateHandler
	rollbackHandler      *deploymentshandlers.RollbackHandler
	logsHandler          *deploymentshandlers.LogsHandler
	statsHandler         *deploymentshandlers.StatsHandler
	domainHandler        *deploymentshandlers.DomainHandler
	sqliteHandler        *sqlitehandlers.SQLiteHandler
	sqliteBackupHandler  *sqlitehandlers.BackupHandler
	replicaHandler       *deploymentshandlers.ReplicaHandler
	portAllocator        *deployments.PortAllocator
	homeNodeManager      *deployments.HomeNodeManager
	replicaManager       *deployments.ReplicaManager
	processManager       *process.Manager
	healthChecker        *health.HealthChecker

	// Middleware cache for auth/routing lookups (eliminates redundant DB queries)
	mwCache *middlewareCache

	// Request log batcher (aggregates writes instead of per-request inserts)
	logBatcher *requestLogBatcher

	// Rate limiters
	rateLimiter          *RateLimiter
	namespaceRateLimiter *NamespaceRateLimiter // legacy; superseded by rateLimitManager when set
	// rateLimitManager (feature #69) handles per-namespace rate limits with
	// tenant self-service config via /v1/namespace/rate-limit. When set,
	// namespaceRateLimitMiddleware uses it instead of the legacy
	// hardcoded-defaults limiter above. nil = falls back to namespaceRateLimiter.
	rateLimitManager       *ratelimit.Manager
	rateLimitConfigStore   ratelimit.ConfigStore
	rateLimitHandlers      *ratelimithandlers.Handlers

	// WebRTC signaling and TURN credentials
	webrtcHandlers *webrtchandlers.WebRTCHandlers
	// webrtcServeTURNCredentials gates the /v1/webrtc/turn/credentials
	// route; webrtcServeSFURoutes gates /v1/webrtc/signal + /rooms.
	// Decoupled (bugboard #25): TURN credentials only need the namespace
	// TURN secret (the actual TURN servers are remote), so a gateway node
	// that doesn't run a local SFU can still mint credentials. SFU
	// signaling/rooms require a local SFU port to proxy to.
	webrtcServeTURNCredentials bool
	webrtcServeSFURoutes       bool

	// WireGuard peer exchange
	wireguardHandler *wireguardhandlers.Handler

	// Node join handler
	joinHandler *joinhandlers.Handler

	// OramaOS node enrollment handler
	enrollHandler *enrollhandlers.Handler

	// Cluster provisioning for namespace clusters
	clusterProvisioner authhandlers.ClusterProvisioner

	// Namespace instance spawn handler (for distributed provisioning)
	spawnHandler http.Handler

	// Namespace delete handler
	namespaceDeleteHandler http.Handler

	// Namespace list handler
	namespaceListHandler http.Handler

	// Peer discovery for namespace gateways (libp2p mesh formation)
	peerDiscovery *PeerDiscovery

	// Node health monitor (ring-based peer failure detection)
	healthMonitor *nodehealth.Monitor

	// Node recovery handler (called when health monitor confirms a node dead or recovered)
	nodeRecoverer authhandlers.NodeRecoverer

	// WebRTC manager for enable/disable operations
	webrtcManager authhandlers.WebRTCManager

	// Circuit breakers for proxy targets (per-target failure tracking)
	circuitBreakers *CircuitBreakerRegistry

	// Shared HTTP transport for proxy connections (connection pooling)
	proxyTransport *http.Transport

	// Vault proxy handlers
	vaultHandlers    *vaulthandlers.Handlers
	operatorHandler  *operatorhandlers.Handler

	// Namespace health state (local service probes + hourly reconciliation)
	nsHealth *namespaceHealthState
}

// localSubscriber represents a WebSocket subscriber for local message delivery
type localSubscriber struct {
	msgChan   chan []byte
	namespace string
}

// PresenceMember represents a member in a topic's presence list
type PresenceMember struct {
	MemberID string                 `json:"member_id"`
	JoinedAt int64                  `json:"joined_at"` // Unix timestamp
	Meta     map[string]interface{} `json:"meta,omitempty"`
	ConnID   string                 `json:"-"` // Internal: for tracking which connection
}

// authClientAdapter adapts client.NetworkClient to authhandlers.NetworkClient
type authClientAdapter struct {
	client client.NetworkClient
}

func (a *authClientAdapter) Database() authhandlers.DatabaseClient {
	return &authDatabaseAdapter{db: a.client.Database()}
}

// authDatabaseAdapter adapts an apiKeyQuerier (the global-registry
// client.DatabaseClient returned by apiKeyDB() — see apikey_querier.go) to
// authhandlers.DatabaseClient.
type authDatabaseAdapter struct {
	db apiKeyQuerier
}

func (a *authDatabaseAdapter) Query(ctx context.Context, sql string, args ...interface{}) (*authhandlers.QueryResult, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("authDatabaseAdapter: no database configured, cannot run query %q", sql)
	}
	result, err := a.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("authDatabaseAdapter: query failed: %w", err)
	}
	// Convert client.QueryResult to authhandlers.QueryResult
	// The auth handlers expect []interface{} but client returns [][]interface{}
	convertedRows := make([]interface{}, len(result.Rows))
	for i, row := range result.Rows {
		convertedRows[i] = row
	}
	return &authhandlers.QueryResult{
		Count: int(result.Count),
		Rows:  convertedRows,
	}, nil
}

// deploymentDatabaseAdapter adapts rqlite.Client to database.Database
type deploymentDatabaseAdapter struct {
	client rqlite.Client
}

func (a *deploymentDatabaseAdapter) Query(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return a.client.Query(ctx, dest, query, args...)
}

func (a *deploymentDatabaseAdapter) QueryOne(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	// Query expects a slice, so we need to query into a slice and check length
	// Get the type of dest and create a slice of that type
	destType := reflect.TypeOf(dest).Elem()
	sliceType := reflect.SliceOf(destType)
	slice := reflect.New(sliceType).Interface()

	// Execute query into slice
	if err := a.client.Query(ctx, slice, query, args...); err != nil {
		return err
	}

	// Check that we got exactly one result
	sliceVal := reflect.ValueOf(slice).Elem()
	if sliceVal.Len() == 0 {
		return fmt.Errorf("no rows found")
	}
	if sliceVal.Len() > 1 {
		return fmt.Errorf("expected 1 row, got %d", sliceVal.Len())
	}

	// Copy the first element to dest
	reflect.ValueOf(dest).Elem().Set(sliceVal.Index(0))
	return nil
}

func (a *deploymentDatabaseAdapter) Exec(ctx context.Context, query string, args ...interface{}) (interface{}, error) {
	return a.client.Exec(ctx, query, args...)
}

// New creates and initializes a new Gateway instance.
// It establishes all necessary service connections and dependencies.
func New(logger *logging.ColoredLogger, cfg *Config) (*Gateway, error) {
	logger.ComponentInfo(logging.ComponentGeneral, "Creating gateway dependencies...")

	// Initialize all dependencies (network client, database, cache, storage, serverless)
	deps, err := NewDependencies(logger, cfg)
	if err != nil {
		logger.ComponentError(logging.ComponentGeneral, "failed to create dependencies", zap.Error(err))
		return nil, err
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Creating gateway instance...")
	gw := &Gateway{
		logger:             logger,
		cfg:                cfg,
		client:             deps.Client,
		nodePeerID:         cfg.NodePeerID,
		startedAt:          time.Now(),
		tunnelLimiter:      newTunnelLimiter(),
		sqlDB:              deps.SQLDB,
		ormClient:          deps.ORMClient,
		ormHTTP:            deps.ORMHTTP,
		olricClient:        deps.OlricClient,
		ipfsClient:         deps.IPFSClient,
		serverlessEngine:   deps.ServerlessEngine,
		serverlessRegistry: deps.ServerlessRegistry,
		serverlessInvoker:  deps.ServerlessInvoker,
		serverlessWSMgr:    deps.ServerlessWSMgr,
		serverlessHandlers: deps.ServerlessHandlers,
		authService:        deps.AuthService,
		localSubscribers:   make(map[string][]*localSubscriber),
		presenceMembers:    make(map[string][]PresenceMember),
		circuitBreakers:    NewCircuitBreakerRegistry(),
		proxyTransport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	// Wire the JWT verifier so the persistent WS handler can apply
	// mid-session auth refresh on the open WS (bugboard #321 control
	// frame). Skipped when either dep is nil — the handler then acks
	// "not supported" and the client falls back to legacy reconnect.
	if gw.serverlessHandlers != nil && gw.authService != nil {
		gw.serverlessHandlers.SetJWTVerifier(gw.authService)
	}

	// Resolve local WireGuard IP for local namespace gateway preference
	if wgIP, err := GetWireGuardIP(); err == nil {
		gw.localWireGuardIP = wgIP
		logger.ComponentInfo(logging.ComponentGeneral, "Detected local WireGuard IP for gateway routing",
			zap.String("wireguard_ip", wgIP))
	} else {
		logger.ComponentWarn(logging.ComponentGeneral, "Could not detect WireGuard IP, local gateway preference disabled",
			zap.Error(err))
	}

	// Per-user circuit isolation secret for the anonymity tunnel (bugboard #168).
	// Derived from credentials this node already holds and never leaves the
	// process: it only keys an HMAC whose output is handed to the local SOCKS
	// port as a circuit selector. A per-process random value would work equally
	// well for isolation but would re-shuffle every user onto a new circuit on
	// each gateway restart, so a stable node-local input is preferred.
	gw.tunnelIsolationSecret = tunnelSecretFrom(cfg)

	// Create separate auth client for global RQLite if GlobalRQLiteDSN is provided
	// This allows namespace gateways to validate API keys against the global database
	if cfg.GlobalRQLiteDSN != "" && cfg.GlobalRQLiteDSN != cfg.RQLiteDSN {
		logger.ComponentInfo(logging.ComponentGeneral, "Creating global auth client...",
			zap.String("global_dsn", cfg.GlobalRQLiteDSN),
		)

		// Create client config for global namespace
		authCfg := client.DefaultClientConfig("default") // Use "default" namespace for global
		authCfg.DatabaseEndpoints = []string{injectRQLiteAuth(cfg.GlobalRQLiteDSN, cfg.RQLiteUsername, cfg.RQLitePassword)}
		if len(cfg.BootstrapPeers) > 0 {
			authCfg.BootstrapPeers = cfg.BootstrapPeers
		}

		authClient, err := client.NewClient(authCfg)
		if err != nil {
			logger.ComponentWarn(logging.ComponentGeneral, "Failed to create global auth client", zap.Error(err))
		} else {
			if err := authClient.Connect(); err != nil {
				logger.ComponentWarn(logging.ComponentGeneral, "Failed to connect global auth client", zap.Error(err))
			} else {
				gw.authClient = authClient
				logger.ComponentInfo(logging.ComponentGeneral, "Global auth client connected")
			}
		}
	}

	// Initialize handler instances
	gw.pubsubHandlers = pubsubhandlers.NewPubSubHandlers(deps.Client, logger)

	// Wire PubSub trigger dispatch if serverless is available
	if deps.PubSubDispatcher != nil {
		gw.pubsubDispatcher = deps.PubSubDispatcher
		gw.pubsubHandlers.SetOnPublish(func(ctx context.Context, namespace, topic string, data []byte) {
			deps.PubSubDispatcher.Dispatch(ctx, namespace, topic, data, 0)
		})
		// Subscribe the dispatcher to libp2p pubsub for every literal
		// trigger pattern so WASM `oh.PubSubPublish` calls reach trigger
		// handlers (bugboard #282 — pre-fix, the dispatcher only fired
		// from the HTTP publish hook above, so internal WASM publishes
		// silently dropped every subscriber). Stop is called from
		// lifecycle.Close.
		if err := deps.PubSubDispatcher.Start(context.Background()); err != nil {
			logger.ComponentWarn(logging.ComponentGeneral,
				"PubSubDispatcher Start failed (libp2p subscribe path disabled — HTTP-publish triggers still work)",
				zap.Error(err))
		}
	}
	if deps.PersistentWSManager != nil {
		gw.persistentWSManager = deps.PersistentWSManager
	}
	if deps.CronScheduler != nil {
		gw.cronScheduler = deps.CronScheduler
		// Background goroutine — Stop is called from gateway.Close.
		gw.cronScheduler.Start(context.Background())
	}

	// Push notification handlers — disabled when no provider is configured.
	// The handlers themselves return 503 if dispatcher/store is nil; we
	// register them unconditionally so the routes always exist with a
	// predictable shape.
	//
	// Prefer the Manager-backed constructor (bug #220 follow-up) so
	// tenants can self-serve their push config via PUT /v1/push/config.
	// Fall back to the legacy constructor when only the YAML-derived
	// dispatcher is available (older deployments without ClusterSecret).
	if deps.PushManager != nil {
		gw.pushHandlers = pushhandlers.NewHandlersWithManager(
			deps.PushManager,
			deps.PushConfigStore,
			deps.PushDeviceStore,
			logger,
		)
	} else if deps.PushDispatcher != nil {
		gw.pushHandlers = pushhandlers.NewHandlers(deps.PushDispatcher, deps.PushDeviceStore, logger)
	}
	// Wire the per-provider credentials manager (feature #72) if push is
	// up. The handler nil-checks the manager internally so this is safe
	// even when push is partially configured.
	if gw.pushHandlers != nil && deps.PushCredentialsManager != nil {
		gw.pushHandlers.SetCredentialsManager(deps.PushCredentialsManager)
	}

	// WebRTC route registration. Construct the handler when EITHER a
	// local SFU is configured (for signal/rooms) OR a TURN secret is set
	// (for credentials) — the two are decoupled (bugboard #25). A gateway
	// node that isn't an SFU node but has the namespace TURN secret can
	// still serve /v1/webrtc/turn/credentials (the TURN servers are
	// remote; credentials are just an HMAC of the shared secret).
	gw.webrtcServeSFURoutes = shouldRegisterWebRTCRoutes(cfg)
	gw.webrtcServeTURNCredentials = shouldServeTURNCredentials(cfg)
	if gw.webrtcServeSFURoutes || gw.webrtcServeTURNCredentials {
		gw.webrtcHandlers = webrtchandlers.NewWebRTCHandlers(
			logger,
			gw.localWireGuardIP,
			cfg.SFUPort,
			cfg.TURNDomain,
			cfg.TURNSecret,
			gw.proxyWebSocket,
		)
		// TURNS (:5349) must advertise the single-label host so the *.<base>
		// wildcard cert validates it in browsers; UDP/TCP keep the legacy host.
		if tlsHost := turn.TLSHostFromLegacyTURNHost(cfg.TURNDomain); tlsHost != "" {
			gw.webrtcHandlers.SetTURNSTLSDomain(tlsHost)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "WebRTC handlers initialized",
			zap.Int("sfu_port", cfg.SFUPort),
			zap.Bool("turn_secret_set", cfg.TURNSecret != ""),
			zap.Bool("serve_turn_credentials", gw.webrtcServeTURNCredentials),
			zap.Bool("serve_sfu_routes", gw.webrtcServeSFURoutes),
			zap.Bool("legacy_webrtc_enabled_flag", cfg.WebRTCEnabled))
	}

	if deps.OlricClient != nil {
		gw.cacheHandlers = cache.NewCacheHandlers(logger, deps.OlricClient)
	}

	if deps.IPFSClient != nil {
		gw.storageHandlers = storage.New(deps.IPFSClient, logger, storage.Config{
			IPFSReplicationFactor: cfg.IPFSReplicationFactor,
			IPFSAPIURL:            cfg.IPFSAPIURL,
		}, deps.ORMClient, deps.GlobalORMClient)
	}

	if deps.AuthService != nil {
		// Create adapter for auth handlers to use the client
		authClientAdapter := &authClientAdapter{client: deps.Client}
		gw.authHandlers = authhandlers.NewHandlers(
			logger,
			deps.AuthService,
			authClientAdapter,
			cfg.ClientNamespace,
			gw.withInternalAuth,
		)

		// Configure Solana NFT verifier for Phantom auth (hardcoded collection + RPC)
		solanaVerifier := auth.NewDefaultSolanaNFTVerifier()
		gw.authHandlers.SetSolanaVerifier(solanaVerifier)
		logger.ComponentInfo(logging.ComponentGeneral, "Solana NFT verifier configured")

		// Wire the global-registry API-key querier (gw.apiKeyDB(), preferring
		// gw.authClient when GlobalRQLiteDSN is configured, else gw.client —
		// see apikey_querier.go) into the JWT-exchange handler's self-query
		// fallback, so it resolves against the SAME global registry the auth
		// middleware uses. API keys are only ever created in the global/core
		// registry, never in a namespace's own RQLite, so every gateway must
		// validate against it.
		gw.authHandlers.SetAPIKeyDB(&authDatabaseAdapter{db: gw.apiKeyDB()})
	}

	// Initialize middleware cache (60s TTL for auth/routing lookups)
	gw.mwCache = newMiddlewareCache(60 * time.Second)

	// Initialize request log batcher (flush every 5 seconds)
	gw.logBatcher = newRequestLogBatcher(gw, 5*time.Second, 100)

	// Initialize rate limiters.
	//
	// Per-IP: token bucket against the client IP. Generous so legitimate
	// users behind shared NATs aren't squeezed.
	gw.rateLimiter = NewRateLimiter(10000, 5000)
	gw.rateLimiter.StartCleanup(5*time.Minute, 10*time.Minute)

	// Per-namespace: feature #69 — backed by an LRU manager with
	// per-namespace overrides via /v1/namespace/rate-limit (config in
	// `namespace_rate_limit_config`, populated by migration 027).
	//
	// Defaults: 10000/min, burst 5000 — matches per-IP so a single user
	// can't saturate the namespace ceiling. Tenants tighten via PUT;
	// operators can raise/lower the Max* ceiling in YAML config.
	//
	// When `deps.ORMClient` is nil (test/standalone modes), we still
	// install a manager backed by a no-store ConfigStore so middleware
	// flow stays uniform; it returns the defaults for every namespace.
	rlDefaults := ratelimit.Defaults{
		RequestsPerMinute:    10000,
		Burst:                5000,
		MaxRequestsPerMinute: 100000, // operator ceiling: tenants can't request more
		MaxBurst:             50000,
	}
	if deps.ORMClient != nil {
		gw.rateLimitConfigStore = ratelimit.NewRqliteConfigStore(deps.ORMClient, logger.Logger)
	}
	gw.rateLimitManager = ratelimit.NewManager(gw.rateLimitConfigStore, rlDefaults, logger.Logger)
	gw.rateLimitHandlers = ratelimithandlers.NewHandlers(gw.rateLimitConfigStore, gw.rateLimitManager, logger)

	// Legacy fallback kept for now in case the manager is ever nil. The
	// middleware prefers rateLimitManager and only uses this if the
	// manager is unset.
	gw.namespaceRateLimiter = NewNamespaceRateLimiter(rlDefaults.RequestsPerMinute, rlDefaults.Burst)

	// Initialize WireGuard peer exchange handler
	if deps.ORMClient != nil {
		gw.wireguardHandler = wireguardhandlers.NewHandler(logger.Logger, deps.ORMClient, cfg.ClusterSecret)
		gw.joinHandler = joinhandlers.NewHandler(logger.Logger, deps.ORMClient, cfg.DataDir)
		gw.enrollHandler = enrollhandlers.NewHandler(logger.Logger, deps.ORMClient, cfg.DataDir)
		gw.vaultHandlers = vaulthandlers.NewHandlers(logger, deps.Client)
		gw.operatorHandler = operatorhandlers.NewHandler(logger.Logger, deps.ORMClient)
	}

	// Initialize deployment system
	if deps.ORMClient != nil && deps.IPFSClient != nil {
		// Convert rqlite.Client to database.Database interface for health checker
		dbAdapter := &deploymentDatabaseAdapter{client: deps.ORMClient}

		// Create deployment service components
		gw.portAllocator = deployments.NewPortAllocator(deps.ORMClient, logger.Logger)
		gw.homeNodeManager = deployments.NewHomeNodeManager(deps.ORMClient, gw.portAllocator, logger.Logger)
		gw.replicaManager = deployments.NewReplicaManager(deps.ORMClient, gw.homeNodeManager, gw.portAllocator, logger.Logger)
		gw.processManager = process.NewManager(logger.Logger)

		// Create deployment service
		baseDomain := gw.cfg.BaseDomain
		if baseDomain == "" {
			baseDomain = "dbrs.space"
		}
		gw.deploymentService = deploymentshandlers.NewDeploymentService(
			deps.ORMClient,
			gw.homeNodeManager,
			gw.portAllocator,
			gw.replicaManager,
			logger.Logger,
			baseDomain,
		)
		// Set node peer ID so deployments run on the node that receives the request
		if gw.cfg.NodePeerID != "" {
			gw.deploymentService.SetNodePeerID(gw.cfg.NodePeerID)
		}

		// Create deployment handlers
		gw.staticHandler = deploymentshandlers.NewStaticDeploymentHandler(
			gw.deploymentService,
			deps.IPFSClient,
			logger.Logger,
		)

		// Determine base deploy path from config
		baseDeployPath := filepath.Join(cfg.DataDir, "deployments")
		if cfg.DataDir == "" {
			baseDeployPath = "" // Let handlers use default
		}

		gw.nextjsHandler = deploymentshandlers.NewNextJSHandler(
			gw.deploymentService,
			gw.processManager,
			deps.IPFSClient,
			logger.Logger,
			baseDeployPath,
		)

		gw.goHandler = deploymentshandlers.NewGoHandler(
			gw.deploymentService,
			gw.processManager,
			deps.IPFSClient,
			logger.Logger,
			baseDeployPath,
		)

		gw.nodejsHandler = deploymentshandlers.NewNodeJSHandler(
			gw.deploymentService,
			gw.processManager,
			deps.IPFSClient,
			logger.Logger,
			baseDeployPath,
		)

		gw.listHandler = deploymentshandlers.NewListHandler(
			gw.deploymentService,
			gw.processManager,
			deps.IPFSClient,
			logger.Logger,
			baseDeployPath,
		)

		gw.updateHandler = deploymentshandlers.NewUpdateHandler(
			gw.deploymentService,
			gw.staticHandler,
			gw.nextjsHandler,
			gw.processManager,
			logger.Logger,
		)

		gw.rollbackHandler = deploymentshandlers.NewRollbackHandler(
			gw.deploymentService,
			gw.updateHandler,
			logger.Logger,
		)

		gw.replicaHandler = deploymentshandlers.NewReplicaHandler(
			gw.deploymentService,
			gw.processManager,
			deps.IPFSClient,
			logger.Logger,
			baseDeployPath,
		)

		gw.logsHandler = deploymentshandlers.NewLogsHandler(
			gw.deploymentService,
			gw.processManager,
			logger.Logger,
		)

		gw.statsHandler = deploymentshandlers.NewStatsHandler(
			gw.deploymentService,
			gw.processManager,
			logger.Logger,
			baseDeployPath,
		)

		gw.domainHandler = deploymentshandlers.NewDomainHandler(
			gw.deploymentService,
			logger.Logger,
		)

		// SQLite handlers
		gw.sqliteHandler = sqlitehandlers.NewSQLiteHandler(
			deps.ORMClient,
			gw.homeNodeManager,
			logger.Logger,
			cfg.DataDir,
			cfg.NodePeerID,
		)

		gw.sqliteBackupHandler = sqlitehandlers.NewBackupHandler(
			gw.sqliteHandler,
			deps.IPFSClient,
			logger.Logger,
		)

		// Start health checker
		gw.healthChecker = health.NewHealthChecker(dbAdapter, logger.Logger, cfg.NodePeerID, gw.processManager)
		gw.healthChecker.SetReconciler(cfg.RQLiteDSN, gw.replicaManager, gw.deploymentService)
		go gw.healthChecker.Start(context.Background())

		logger.ComponentInfo(logging.ComponentGeneral, "Deployment system initialized")
	}

	// Start background Olric reconnection if initial connection failed
	if deps.OlricClient == nil {
		olricCfg := olric.Config{
			Servers: cfg.OlricServers,
			Timeout: cfg.OlricTimeout,
		}
		if len(olricCfg.Servers) == 0 {
			olricCfg.Servers = []string{"localhost:10102"}
		}
		gw.startOlricReconnectLoop(olricCfg)
	}

	// Initialize peer discovery for namespace gateways
	// This allows the 3 namespace gateway instances to discover each other
	if cfg.ClientNamespace != "" && cfg.ClientNamespace != "default" && deps.Client != nil {
		logger.ComponentInfo(logging.ComponentGeneral, "Initializing peer discovery for namespace gateway...",
			zap.String("namespace", cfg.ClientNamespace))

		// Get libp2p host from client
		host := deps.Client.Host()
		if host != nil {
			// NOTE: we deliberately do NOT pass cfg.ListenAddr's port here
			// anymore — that's the gateway's HTTP API port, NOT the libp2p
			// port. Passing it caused every cross-node libp2p dial to land
			// on the HTTP server and fail the multistream handshake,
			// leaving the namespace mesh with 0 connected peers. The libp2p
			// port is OS-assigned and lives on host.Addrs() — peer
			// discovery extracts it from there at register time.

			// Create peer discovery manager
			gw.peerDiscovery = NewPeerDiscovery(
				host,
				deps.SQLDB,
				cfg.NodePeerID,
				cfg.ClientNamespace,
				logger.Logger,
			)

			// Start peer discovery
			ctx := context.Background()
			if err := gw.peerDiscovery.Start(ctx); err != nil {
				logger.ComponentWarn(logging.ComponentGeneral, "Failed to start peer discovery",
					zap.Error(err))
			} else {
				logger.ComponentInfo(logging.ComponentGeneral, "Peer discovery started successfully",
					zap.String("namespace", cfg.ClientNamespace))
			}
		} else {
			logger.ComponentWarn(logging.ComponentGeneral, "Cannot initialize peer discovery: libp2p host not available")
		}
	}

	// Start node health monitor (ring-based peer failure detection)
	if cfg.NodePeerID != "" && deps.SQLDB != nil {
		gw.healthMonitor = nodehealth.NewMonitor(nodehealth.Config{
			NodeID:        cfg.NodePeerID,
			DB:            deps.SQLDB,
			Logger:        logger.Logger,
			ProbeInterval: 10 * time.Second,
			Neighbors:     3,
		})
		gw.healthMonitor.OnNodeDead(func(nodeID string) {
			logger.ComponentError(logging.ComponentGeneral, "Node confirmed dead by quorum — starting recovery",
				zap.String("dead_node", nodeID))
			if gw.nodeRecoverer != nil {
				go gw.nodeRecoverer.HandleDeadNode(context.Background(), nodeID)
			}
		})
		gw.healthMonitor.OnNodeRecovered(func(nodeID string) {
			logger.ComponentInfo(logging.ComponentGeneral, "Node recovered — re-enabling DNS and checking for orphaned services",
				zap.String("node_id", nodeID))
			if gw.nodeRecoverer != nil {
				go gw.nodeRecoverer.HandleSuspectRecovery(context.Background(), nodeID)
				go gw.nodeRecoverer.HandleRecoveredNode(context.Background(), nodeID)
			}
		})
		gw.healthMonitor.OnNodeSuspect(func(nodeID string) {
			logger.ComponentWarn(logging.ComponentGeneral, "Node SUSPECT — disabling DNS records",
				zap.String("suspect_node", nodeID))
			if gw.nodeRecoverer != nil {
				go gw.nodeRecoverer.HandleSuspectNode(context.Background(), nodeID)
			}
		})
		go gw.healthMonitor.Start(context.Background())
		logger.ComponentInfo(logging.ComponentGeneral, "Node health monitor started",
			zap.String("node_id", cfg.NodePeerID))
	}

	// Start namespace health monitoring loop (local probes every 30s, reconciliation every 1h)
	if cfg.NodePeerID != "" && deps.SQLDB != nil {
		go gw.startNamespaceHealthLoop(context.Background())
		logger.ComponentInfo(logging.ComponentGeneral, "Namespace health monitor started")
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Gateway creation completed")
	return gw, nil
}

// shouldRegisterWebRTCRoutes decides whether `/v1/webrtc/*` routes
// (turn/credentials, signal, rooms) get wired up in the request mux.
//
// Bugboard #411 — pre-fix this required BOTH cfg.WebRTCEnabled AND
// cfg.SFUPort > 0. The boolean flag was a silent-404 footgun: spawn-
// handler-provisioned namespace gateways defaulted to
// WebRTCEnabled=false even when their SFU service was up and SFUPort
// was set. AnChat hit 404 on /v1/webrtc/turn/credentials for ~3
// months because of this even though TURN was operationally usable.
//
// Post-fix: SFUPort > 0 alone gates registration. SFUPort is the
// actual operational prerequisite — the SFU proxy can't function
// without it, and operators who set SFUPort have already opted in.
// cfg.WebRTCEnabled is kept on the Config struct for back-compat with
// operator YAML and the spawn-handler request shape, but ignored at
// this gate.
//
// TURNSecret intentionally NOT in the gate. /v1/webrtc/signal and
// /v1/webrtc/rooms work without TURN (the SFU proxy alone). The
// credentials endpoint internally 503s "TURN not configured" when
// TURNSecret is empty — that's an ACTIONABLE error operators can
// trace, unlike the silent 404 that #411 reported.
//
// Extracted to a named function so the route-gate test can exercise
// the EXACT runtime logic without spinning up a full Gateway. If you
// change this function, update the gate's call site at the same time
// — or the test passes while live behavior diverges.
func shouldRegisterWebRTCRoutes(cfg *Config) bool {
	return cfg.SFUPort > 0
}

// shouldServeTURNCredentials gates ONLY the /v1/webrtc/turn/credentials
// route, decoupled from the SFU gate above (bugboard #25).
//
// TURN credentials are a namespace-wide HMAC of the shared TURN secret;
// the actual TURN servers are remote (the namespace's TURN nodes), so a
// gateway node that runs NO local SFU can still mint valid credentials.
// Tying credentials to SFUPort>0 (the old single gate) meant non-SFU
// gateways 404'd on credentials even though they had the secret — that's
// the bug-25 symptom node 57 hit (~1/3 of requests routed to a non-SFU
// gateway). SFU signaling/rooms remain gated on SFUPort>0 because they
// proxy to a local SFU.
func shouldServeTURNCredentials(cfg *Config) bool {
	return cfg.TURNSecret != ""
}

// getLocalSubscribers returns all local subscribers for a given topic and namespace
func (g *Gateway) getLocalSubscribers(topic, namespace string) []*localSubscriber {
	topicKey := namespace + "." + topic
	if subs, ok := g.localSubscribers[topicKey]; ok {
		return subs
	}
	return nil
}

// SetClusterProvisioner sets the cluster provisioner for namespace cluster management.
// This enables automatic RQLite/Olric/Gateway cluster provisioning when new namespaces are created.
func (g *Gateway) SetClusterProvisioner(cp authhandlers.ClusterProvisioner) {
	g.clusterProvisioner = cp
	if g.authHandlers != nil {
		g.authHandlers.SetClusterProvisioner(cp)
	}
}

// SetNodeRecoverer sets the handler for dead node recovery and revived node cleanup.
func (g *Gateway) SetNodeRecoverer(nr authhandlers.NodeRecoverer) {
	g.nodeRecoverer = nr
}

// SetWebRTCManager sets the WebRTC lifecycle manager for enable/disable operations.
func (g *Gateway) SetWebRTCManager(wm authhandlers.WebRTCManager) {
	g.webrtcManager = wm
}

// SetSpawnHandler sets the handler for internal namespace spawn/stop requests.
func (g *Gateway) SetSpawnHandler(h http.Handler) {
	g.spawnHandler = h
}

// SetNamespaceDeleteHandler sets the handler for namespace deletion requests.
func (g *Gateway) SetNamespaceDeleteHandler(h http.Handler) {
	g.namespaceDeleteHandler = h
}

// SetNamespaceListHandler sets the handler for namespace list requests.
func (g *Gateway) SetNamespaceListHandler(h http.Handler) {
	g.namespaceListHandler = h
}

// GetORMClient returns the RQLite ORM client for external use (e.g., by ClusterManager)
func (g *Gateway) GetORMClient() rqlite.Client {
	return g.ormClient
}

// GetIPFSClient returns the IPFS client for external use (e.g., by namespace delete handler)
func (g *Gateway) GetIPFSClient() ipfs.IPFSClient {
	return g.ipfsClient
}

// setOlricClient atomically sets the Olric client and reinitializes cache handlers.
func (g *Gateway) setOlricClient(client *olric.Client) {
	g.olricMu.Lock()
	defer g.olricMu.Unlock()
	g.olricClient = client
	if client != nil {
		g.cacheHandlers = cache.NewCacheHandlers(g.logger, client)
	}
}

// getOlricClient atomically retrieves the current Olric client.
func (g *Gateway) getOlricClient() *olric.Client {
	g.olricMu.RLock()
	defer g.olricMu.RUnlock()
	return g.olricClient
}

// startOlricReconnectLoop starts a background goroutine that continuously attempts
// to reconnect to the Olric cluster with exponential backoff.
func (g *Gateway) startOlricReconnectLoop(cfg olric.Config) {
	go func() {
		retryDelay := 5 * time.Second
		maxBackoff := 30 * time.Second

		for {
			client, err := olric.NewClient(cfg, g.logger.Logger)
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
			if retryDelay < maxBackoff {
				retryDelay *= 2
				if retryDelay > maxBackoff {
					retryDelay = maxBackoff
				}
			}
		}
	}()
}

// Cache handler wrappers - these check cacheHandlers dynamically to support
// background Olric reconnection. Without these, cache routes won't work if
// Olric wasn't available at gateway startup but connected later.

func (g *Gateway) cacheHealthHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.HealthHandler(w, r)
}

func (g *Gateway) cacheGetHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.GetHandler(w, r)
}

func (g *Gateway) cacheMGetHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.MultiGetHandler(w, r)
}

func (g *Gateway) cachePutHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.SetHandler(w, r)
}

func (g *Gateway) cacheDeleteHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.DeleteHandler(w, r)
}

func (g *Gateway) cacheScanHandler(w http.ResponseWriter, r *http.Request) {
	g.olricMu.RLock()
	handlers := g.cacheHandlers
	g.olricMu.RUnlock()
	if handlers == nil {
		writeError(w, http.StatusServiceUnavailable, "cache service unavailable")
		return
	}
	handlers.ScanHandler(w, r)
}

// namespaceClusterStatusHandler handles GET /v1/namespace/status?id={cluster_id}
// This endpoint is public (no API key required) to allow polling during provisioning.
func (g *Gateway) namespaceClusterStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clusterID := r.URL.Query().Get("id")
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "cluster_id parameter required")
		return
	}

	if g.clusterProvisioner == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster provisioning not enabled")
		return
	}

	status, err := g.clusterProvisioner.GetClusterStatusByID(r.Context(), clusterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// namespaceClusterRepairHandler handles POST /v1/internal/namespace/repair?namespace={name}
// This endpoint repairs under-provisioned namespace clusters by adding missing nodes.
// Internal-only: authenticated by X-Orama-Internal-Auth header.
func (g *Gateway) namespaceClusterRepairHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Internal auth check: header + WireGuard subnet verification
	if r.Header.Get("X-Orama-Internal-Auth") != "namespace-coordination" || !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	namespaceName := r.URL.Query().Get("namespace")
	if namespaceName == "" {
		writeError(w, http.StatusBadRequest, "namespace parameter required")
		return
	}

	if g.nodeRecoverer == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster recovery not enabled")
		return
	}

	if err := g.nodeRecoverer.RepairCluster(r.Context(), namespaceName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "cluster repair completed",
	})
}

// namespaceWebRTCEnablePublicHandler handles POST /v1/namespace/webrtc/enable
// Public: authenticated by JWT/API key via auth middleware. Namespace from context.
func (g *Gateway) namespaceWebRTCEnablePublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespaceName, _ := r.Context().Value(CtxKeyNamespaceOverride).(string)
	if namespaceName == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	if err := g.webrtcManager.EnableWebRTC(r.Context(), namespaceName, "api"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "WebRTC enabled successfully",
	})
}

// namespaceWebRTCDisablePublicHandler handles POST /v1/namespace/webrtc/disable
// Public: authenticated by JWT/API key via auth middleware. Namespace from context.
func (g *Gateway) namespaceWebRTCDisablePublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespaceName, _ := r.Context().Value(CtxKeyNamespaceOverride).(string)
	if namespaceName == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	if err := g.webrtcManager.DisableWebRTC(r.Context(), namespaceName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "WebRTC disabled successfully",
	})
}

// namespaceWebRTCStealthPublicHandler handles POST /v1/namespace/webrtc/stealth/{enable|disable}
// (feat-124). Public: authenticated by JWT/API key via auth middleware;
// namespace from context. `enable` is true for the enable route.
func (g *Gateway) namespaceWebRTCStealthPublicHandler(w http.ResponseWriter, r *http.Request, enable bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespaceName, _ := r.Context().Value(CtxKeyNamespaceOverride).(string)
	if namespaceName == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	var err error
	action := "disabled"
	if enable {
		action = "enabled"
		err = g.webrtcManager.EnableWebRTCStealth(r.Context(), namespaceName)
	} else {
		err = g.webrtcManager.DisableWebRTCStealth(r.Context(), namespaceName)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "WebRTC stealth " + action + " successfully",
	})
}

// namespaceWebRTCStatusPublicHandler handles GET /v1/namespace/webrtc/status
// Public: authenticated by JWT/API key via auth middleware. Namespace from context.
func (g *Gateway) namespaceWebRTCStatusPublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespaceName, _ := r.Context().Value(CtxKeyNamespaceOverride).(string)
	if namespaceName == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	config, err := g.webrtcManager.GetWebRTCStatus(r.Context(), namespaceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if config == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"namespace": namespaceName,
			"enabled":   false,
		})
	} else {
		json.NewEncoder(w).Encode(config)
	}
}

// namespaceWebRTCEnableHandler handles POST /v1/internal/namespace/webrtc/enable?namespace={name}
// Internal-only: authenticated by X-Orama-Internal-Auth header + WireGuard subnet.
func (g *Gateway) namespaceWebRTCEnableHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if r.Header.Get("X-Orama-Internal-Auth") != "namespace-coordination" || !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	namespaceName := r.URL.Query().Get("namespace")
	if namespaceName == "" {
		writeError(w, http.StatusBadRequest, "namespace parameter required")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	if err := g.webrtcManager.EnableWebRTC(r.Context(), namespaceName, "cli"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "WebRTC enabled successfully",
	})
}

// namespaceWebRTCDisableHandler handles POST /v1/internal/namespace/webrtc/disable?namespace={name}
// Internal-only: authenticated by X-Orama-Internal-Auth header + WireGuard subnet.
func (g *Gateway) namespaceWebRTCDisableHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if r.Header.Get("X-Orama-Internal-Auth") != "namespace-coordination" || !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	namespaceName := r.URL.Query().Get("namespace")
	if namespaceName == "" {
		writeError(w, http.StatusBadRequest, "namespace parameter required")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	if err := g.webrtcManager.DisableWebRTC(r.Context(), namespaceName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"namespace": namespaceName,
		"message":   "WebRTC disabled successfully",
	})
}

// namespaceWebRTCStatusHandler handles GET /v1/internal/namespace/webrtc/status?namespace={name}
// Internal-only: authenticated by X-Orama-Internal-Auth header + WireGuard subnet.
func (g *Gateway) namespaceWebRTCStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if r.Header.Get("X-Orama-Internal-Auth") != "namespace-coordination" || !nodeauth.IsWireGuardPeer(r.RemoteAddr) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	namespaceName := r.URL.Query().Get("namespace")
	if namespaceName == "" {
		writeError(w, http.StatusBadRequest, "namespace parameter required")
		return
	}

	if g.webrtcManager == nil {
		writeError(w, http.StatusServiceUnavailable, "WebRTC management not enabled")
		return
	}

	config, err := g.webrtcManager.GetWebRTCStatus(r.Context(), namespaceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if config == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"namespace": namespaceName,
			"enabled":   false,
		})
	} else {
		json.NewEncoder(w).Encode(config)
	}
}

