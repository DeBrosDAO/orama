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
	"strings"
	"sync"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/health"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	authhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/cache"
	deploymentshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/deployments"
	enrollhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/enroll"
	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
	operatorhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	pubsubhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/pubsub"
	pushhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/push"
	ratelimithandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/ratelimit"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	sqlitehandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/sqlite"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	vaulthandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/vault"
	webrtchandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/webrtc"
	wireguardhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/wireguard"
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
	logger           *logging.ColoredLogger
	cfg              *Config
	client           client.NetworkClient
	nodePeerID       string // The node's actual peer ID from its identity file (overrides client's peer ID)
	localWireGuardIP string // WireGuard IP of this node, used to prefer local namespace gateways
	startedAt        time.Time

	// rqlite SQL connection and HTTP ORM gateway
	// shutdownCtx is cancelled by Close. Background work owned by the gateway
	// derives from it so nothing keeps running against torn-down dependencies.
	shutdownCtx context.Context
	shutdown    context.CancelFunc

	// ready is the gateway's own start-up state — see readiness.go. Separate
	// from the subsystem health in /health, which is about what the gateway
	// talks to rather than whether the gateway itself can serve.
	//
	// A value, not a pointer, so a Gateway assembled without New cannot nil-
	// deref here and cannot accidentally read as ready. Gateway is only ever
	// used through a pointer, so the mutex inside is never copied.
	ready readiness

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
	olricClient   *olric.Client
	olricMu       sync.RWMutex
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
	serverlessEngine    *serverless.Engine
	serverlessRegistry  *serverless.Registry
	serverlessInvoker   *serverless.Invoker
	serverlessWSMgr     *serverless.WSManager
	serverlessHandlers  *serverlesshandlers.ServerlessHandlers
	pubsubDispatcher    *triggers.PubSubDispatcher
	persistentWSManager *persistent.Manager
	cronScheduler       *triggers.CronScheduler

	// Authentication service
	authService  *auth.Service
	authHandlers *authhandlers.Handlers

	// Deployment system
	deploymentService   *deploymentshandlers.DeploymentService
	staticHandler       *deploymentshandlers.StaticDeploymentHandler
	nextjsHandler       *deploymentshandlers.NextJSHandler
	goHandler           *deploymentshandlers.GoHandler
	nodejsHandler       *deploymentshandlers.NodeJSHandler
	listHandler         *deploymentshandlers.ListHandler
	envHandler          *deploymentshandlers.EnvHandler
	updateHandler       *deploymentshandlers.UpdateHandler
	rollbackHandler     *deploymentshandlers.RollbackHandler
	logsHandler         *deploymentshandlers.LogsHandler
	statsHandler        *deploymentshandlers.StatsHandler
	domainHandler       *deploymentshandlers.DomainHandler
	sqliteHandler       *sqlitehandlers.SQLiteHandler
	sqliteBackupHandler *sqlitehandlers.BackupHandler
	replicaHandler      *deploymentshandlers.ReplicaHandler
	portAllocator       *deployments.PortAllocator
	homeNodeManager     *deployments.HomeNodeManager
	replicaManager      *deployments.ReplicaManager
	processManager      *process.Manager
	healthChecker       *health.HealthChecker

	// internalAuthKey authenticates the X-Internal-Auth-* headers across a
	// proxy hop (see internal_auth_hop.go). Derived from the cluster secret,
	// so every node in a cluster has the same one and nobody outside it does.
	// Empty when this gateway has no cluster secret, and an empty key trusts
	// nothing.
	internalAuthKey []byte

	// Middleware cache for auth/routing lookups (eliminates redundant DB queries)
	mwCache *middlewareCache

	// Request log batcher (aggregates writes instead of per-request inserts)
	logBatcher *requestLogBatcher

	// Rate limiters
	rateLimiter *RateLimiter
	// authRateLimiter caps the endpoints that mint or exchange credentials,
	// far below the general limit. See isAuthRateLimitPath.
	authRateLimiter      *RateLimiter
	namespaceRateLimiter *NamespaceRateLimiter // legacy; superseded by rateLimitManager when set
	// rateLimitManager (feature #69) handles per-namespace rate limits with
	// tenant self-service config via /v1/namespace/rate-limit. When set,
	// namespaceRateLimitMiddleware uses it instead of the legacy
	// hardcoded-defaults limiter above. nil = falls back to namespaceRateLimiter.
	rateLimitManager     *ratelimit.Manager
	rateLimitConfigStore ratelimit.ConfigStore
	rateLimitHandlers    *ratelimithandlers.Handlers

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

	// namespaceCreateHandler serves POST /v1/namespaces. Creating a namespace
	// used to be a side effect of asking for a login challenge.
	namespaceCreateHandler http.Handler

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
	vaultHandlers   *vaulthandlers.Handlers
	operatorHandler *operatorhandlers.Handler

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
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	gw := &Gateway{
		logger:             logger,
		cfg:                cfg,
		client:             deps.Client,
		nodePeerID:         cfg.NodePeerID,
		startedAt:          time.Now(),
		tunnelLimiter:      newTunnelLimiter(),
		ready:              newReadiness(),
		shutdownCtx:        shutdownCtx,
		shutdown:           shutdown,
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
	// The hop key is what lets a namespace gateway believe this one validated
	// a request. Without a cluster secret there is no key, no internal-auth
	// header is ever trusted, and the proxy hop refuses rather than forwarding
	// an assertion it cannot back — which is the same configuration that
	// already breaks cross-gateway JWT verification.
	if key, err := internalAuthKey(cfg.ClusterSecret); err == nil {
		gw.internalAuthKey = key
	} else {
		logger.ComponentWarn(logging.ComponentGeneral,
			"No cluster secret: internal-auth headers will never be trusted and this "+
				"gateway cannot delegate auth to a namespace gateway",
			zap.Error(err))
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

	// A gateway configured with its own API-key registry must reach it.
	registryClient, err := connectAPIKeyRegistry(cfg, logger)
	if err != nil {
		return nil, err
	}
	if registryClient != nil {
		gw.authClient = registryClient
		if deps.AuthService != nil {
			deps.AuthService.SetAPIKeyRegistry(registryClient)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "Global auth client connected")
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

		// Wire the global-registry API-key querier (gw.apiKeyDB(), preferring
		// gw.authClient when GlobalRQLiteDSN is configured, else gw.client —
		// see apikey_querier.go) into the JWT-exchange handler's self-query
		// fallback, so it resolves against the SAME global registry the auth
		// middleware uses. API keys are only ever created in the global/core
		// registry, never in a namespace's own RQLite, so every gateway must
		// validate against it.
		gw.authHandlers.SetAPIKeyDB(&authDatabaseAdapter{db: gw.apiKeyDB()})

		// A namespace gateway's own database is not the key registry, and the
		// api_keys rows in it are leftovers — the pre-#163 ones being raw
		// credentials the tenant can read. They are removed here, on the only
		// gateways where the local database is provably not the registry.
		if usesSeparateAPIKeyRegistry(cfg) && gw.client != nil {
			if n, perr := purgeTenantPlaintextAPIKeys(context.Background(), gw.client.Database()); perr != nil {
				logger.ComponentWarn(logging.ComponentGeneral,
					"plaintext API keys are still on disk in this namespace's own database", zap.Error(perr))
			} else if n > 0 {
				logger.ComponentInfo(logging.ComponentGeneral,
					"Removed leftover plaintext API keys from this namespace's database", zap.Int("count", n))
			}
		}

		if strings.TrimSpace(cfg.APIKeyHMACSecret) != "" {
			n, merr := deps.AuthService.MigratePlaintextAPIKeys(context.Background())
			if merr != nil {
				return nil, fmt.Errorf("migrate plaintext API keys: %w", merr)
			}
			if n > 0 {
				logger.ComponentInfo(logging.ComponentGeneral, "Hashed leftover plaintext API keys",
					zap.Int("count", n))
			}
		}
		// Expired revocations deny nothing. Pruning keeps the table the size
		// of the revocations still in flight rather than growing forever.
		deps.AuthService.Revocations().StartPruning(context.Background())

		if on, oerr := deps.AuthService.RevokeOrphanedAPIKeys(context.Background()); oerr != nil {
			logger.ComponentWarn(logging.ComponentGeneral, "revoke orphaned API keys failed", zap.Error(oerr))
		} else if on > 0 {
			logger.ComponentInfo(logging.ComponentGeneral, "Revoked orphaned API keys",
				zap.Int("count", on))
		}
	}

	// Initialize middleware cache (60s TTL for auth/routing lookups)
	gw.mwCache = newMiddlewareCache(60 * time.Second)

	// Initialize request log batcher (flush every 5 seconds)
	gw.logBatcher = newRequestLogBatcher(gw, 5*time.Second, 100)

	// Initialize rate limiters.
	//
	// Per-IP: token bucket against the client IP. Generous so legitimate
	// users behind shared NATs aren't squeezed.
	configureRateLimiters(gw)

	// Challenges are Raft-replicated rows that stop being claimable the moment
	// they expire, and nothing removed them: the table only ever grew.
	if deps.AuthService != nil {
		deps.AuthService.StartNonceReaper(gw.shutdownCtx)
	}

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
		if deps.AuthService != nil {
			gw.operatorHandler.SetAuditLog(deps.AuthService.Audit())
		}
	}

	// Initialize deployment system.
	//
	// A deployment's environment is where the platform tells people to put
	// their secrets, and it is encrypted with a key derived from the cluster
	// secret. Without that secret there is no key, so the deployment system
	// does not start rather than storing every tenant's credentials in the
	// clear in a Raft-replicated table.
	envCodec, envCodecErr := deployments.NewEnvCodec(cfg.ClusterSecret)
	if envCodecErr != nil {
		logger.Logger.Error("deployments are unavailable on this gateway: without a cluster secret "+
			"there is no key to encrypt deployment environments with",
			zap.Error(envCodecErr))
	}
	if deps.ORMClient != nil && deps.IPFSClient != nil && envCodec != nil {
		// Convert rqlite.Client to database.Database interface for health checker
		dbAdapter := &deploymentDatabaseAdapter{client: deps.ORMClient}

		// Create deployment service
		baseDomain := gw.cfg.BaseDomain
		if baseDomain == "" {
			baseDomain = "dbrs.space"
		}

		// Create deployment service components
		gw.portAllocator = deployments.NewPortAllocator(deps.ORMClient, logger.Logger)
		gw.homeNodeManager = deployments.NewHomeNodeManager(deps.ORMClient, gw.portAllocator, logger.Logger)
		gw.replicaManager = deployments.NewReplicaManager(deps.ORMClient, gw.homeNodeManager, gw.portAllocator, logger.Logger)
		gw.processManager = process.NewManager(logger.Logger, process.Config{
			EnvDir:     deploymentEnvDir(cfg.DataDir),
			BaseDomain: baseDomain,
		})

		gw.deploymentService = deploymentshandlers.NewDeploymentService(
			deps.ORMClient,
			gw.homeNodeManager,
			gw.portAllocator,
			gw.replicaManager,
			logger.Logger,
			baseDomain,
			envCodec,
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

		gw.envHandler = deploymentshandlers.NewEnvHandler(
			gw.deploymentService,
			gw.processManager,
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
		// Waits for readiness: it queries the deployment tables, which do not
		// exist until the migrations this gateway is still applying have run.
		go func() {
			if gw.AwaitReady(context.Background()) {
				gw.healthChecker.Start(context.Background())
			}
		}()

		logger.ComponentInfo(logging.ComponentGeneral, "Deployment system initialized")
	}

	// Re-allocate IPFS content that has dropped below its replication factor.
	// One node per interval does the work; the cluster lock decides which.
	if ipfsClient, ok := deps.IPFSClient.(*ipfs.Client); ok && ipfsClient != nil {
		gw.StartPinSweep(gw.shutdownCtx, ipfsClient, cfg.IPFSReplicationFactor)
	}

	// Supervise Olric for the life of the process, whether or not the initial
	// connection worked. Arming this only on an initial failure meant the
	// common case — up at start, dies later — was never covered.
	{
		olricCfg := olric.Config{
			Servers: cfg.OlricServers,
			Timeout: cfg.OlricTimeout,
		}
		if len(olricCfg.Servers) == 0 {
			// The list discovery actually resolved, not a guess. The old
			// fallback used cfg.OlricServers (empty on a namespace gateway)
			// and then a hardcoded localhost, so a gateway that lost its cache
			// spent the rest of its life reconnecting to the wrong address.
			olricCfg.Servers = deps.OlricServers
		}
		if len(olricCfg.Servers) == 0 {
			olricCfg.Servers = []string{constants.OlricAddrFor("localhost")}
		}
		gw.startOlricSupervisor(gw.shutdownCtx, olricCfg)
	}

	// Bring the schema up in the background. Until it succeeds the gateway
	// serves /health as "starting" and refuses everything else; see
	// readiness.go for why this is not part of start-up.
	//
	// Bound to the gateway's own lifetime: Close closes the database, and a
	// loop still retrying against a closed handle would spin on
	// "sql: database is closed" until the process exited.
	gw.startSchemaReadiness(gw.shutdownCtx, cfg, deps)

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

			// Start peer discovery once the schema exists: it registers into
			// gateway_peers, a table the migrations this gateway is still
			// applying create.
			go func() {
				ctx := context.Background()
				if !gw.AwaitReady(ctx) {
					return
				}
				if err := gw.peerDiscovery.Start(ctx); err != nil {
					logger.ComponentWarn(logging.ComponentGeneral, "Failed to start peer discovery",
						zap.Error(err))
					return
				}
				logger.ComponentInfo(logging.ComponentGeneral, "Peer discovery started successfully",
					zap.String("namespace", cfg.ClientNamespace))
			}()
		} else {
			logger.ComponentWarn(logging.ComponentGeneral, "Cannot initialize peer discovery: libp2p host not available")
		}
	}

	// Start node health monitor (ring-based peer failure detection).
	//
	// Index gateway only. The ring is built from dns_nodes, which is written by
	// the node heartbeat into the INDEX rqlite; a tenant gateway's SQLDB is its
	// own namespace rqlite, where core migrations create dns_nodes but nothing
	// ever inserts a row (bugboard #153). Running the monitor there gave every
	// tenant gateway an empty ring — wasted work at best, and one schema change
	// away from acting on a half-populated table.
	if cfg.NodePeerID != "" && deps.SQLDB != nil && !isNamespaceGateway(cfg) {
		healthMonitor, healthErr := nodehealth.NewMonitor(nodehealth.Config{
			NodeID:        cfg.NodePeerID,
			DB:            deps.SQLDB,
			Logger:        logger.Logger,
			ProbeInterval: 10 * time.Second,
			Neighbors:     3,
			// Peers are probed on their index gateway, not on this process's
			// own port: a tenant gateway listens on a namespace port that no
			// other node serves /v1/internal/ping on.
			ProbePort: constants.GatewayAPIPort,
		})
		if healthErr != nil {
			return nil, fmt.Errorf("start node health monitor: %w", healthErr)
		}
		gw.healthMonitor = healthMonitor
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
		go func() {
			if gw.AwaitReady(context.Background()) {
				gw.healthMonitor.Start(context.Background())
			}
		}()
		logger.ComponentInfo(logging.ComponentGeneral, "Node health monitor started",
			zap.String("node_id", cfg.NodePeerID))
	}

	// Start namespace health monitoring loop (local probes every 30s, reconciliation every 1h)
	if cfg.NodePeerID != "" && deps.SQLDB != nil {
		go func() {
			if gw.AwaitReady(context.Background()) {
				gw.startNamespaceHealthLoop(context.Background())
			}
		}()
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

// SetNamespaceCreateHandler sets the handler for namespace creation.
func (g *Gateway) SetNamespaceCreateHandler(h http.Handler) {
	g.namespaceCreateHandler = h
}

// GetORMClient returns the RQLite ORM client for external use (e.g., by ClusterManager)
func (g *Gateway) GetORMClient() rqlite.Client {
	return g.ormClient
}

// GetIPFSClient returns the IPFS client for external use (e.g., by namespace delete handler)
// GetAuditLog returns the record of auth events, so handlers wired from
// outside this package can write to the same one.
func (g *Gateway) GetAuditLog() *auth.AuditLog {
	if g.authService == nil {
		return nil
	}
	return g.authService.Audit()
}

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

// Olric supervision bounds.
const (
	// olricProbeInterval is how often a connected client is re-checked. Short
	// enough that a namespace stops hanging on a dead cache within a probe or
	// two, long enough not to be its own load.
	olricProbeInterval = 10 * time.Second

	// olricUnhealthyThreshold is how many consecutive failed probes before the
	// client is dropped. One failure is a blip; three across half a minute is
	// the cache being gone.
	olricUnhealthyThreshold = 3

	// olricReconnectBase / olricReconnectMax bound the retry while
	// disconnected.
	olricReconnectBase = 5 * time.Second
	olricReconnectMax  = 30 * time.Second
)

// startOlricSupervisor keeps the Olric client in step with reality for the life
// of the process.
//
// It replaces a one-shot reconnect loop that returned as soon as it connected
// once, and which was only armed when the INITIAL connect had failed. So the
// common case — Olric up at start, dies later — left a stale client wired in
// for ever: handlers returned transport errors on every request instead of the
// honest 503 the nil-client branch produces, and /health kept saying healthy
// while namespace requests hung.
//
// Two states. Connected: probe, and on sustained failure drop the client so
// handlers answer 503. Disconnected: retry with backoff until one works. It
// never returns.
func (g *Gateway) startOlricSupervisor(ctx context.Context, cfg olric.Config) {
	go func() {
		failures := 0
		retryDelay := olricReconnectBase

		for {
			if g.getOlricClient() != nil {
				if err := g.probeOlric(ctx); err != nil {
					failures++
					g.logger.ComponentWarn(logging.ComponentGeneral, "Olric probe failed",
						zap.Int("consecutive_failures", failures),
						zap.Int("threshold", olricUnhealthyThreshold),
						zap.Error(err))

					if failures >= olricUnhealthyThreshold {
						// Drop it. A client that cannot reach its cluster
						// returns errors from every call; a nil one returns a
						// 503, which is the truthful answer and the one a
						// caller can act on.
						g.logger.ComponentError(logging.ComponentGeneral,
							"Olric is unreachable; cache requests will return 503 until it recovers",
							zap.Int("consecutive_failures", failures))
						g.setOlricClient(nil)
						failures = 0
						retryDelay = olricReconnectBase
					}
				} else {
					failures = 0
				}

				if !sleepCtx(ctx, olricProbeInterval) {
					return
				}
				continue
			}

			client, err := olric.NewClient(cfg, g.logger.Logger)
			if err == nil {
				g.setOlricClient(client)
				g.logger.ComponentInfo(logging.ComponentGeneral, "Olric cache client connected",
					zap.Strings("servers", cfg.Servers),
					zap.Duration("timeout", cfg.Timeout))
				retryDelay = olricReconnectBase
				if !sleepCtx(ctx, olricProbeInterval) {
					return
				}
				continue
			}

			g.logger.ComponentWarn(logging.ComponentGeneral, "Olric cache client reconnect failed",
				zap.Duration("retry_in", retryDelay),
				zap.Error(err))

			if !sleepCtx(ctx, retryDelay) {
				return
			}
			retryDelay *= 2
			if retryDelay > olricReconnectMax {
				retryDelay = olricReconnectMax
			}
		}
	}()
}

// probeOlric checks that the current client can still reach its cluster.
func (g *Gateway) probeOlric(ctx context.Context) error {
	client := g.getOlricClient()
	if client == nil {
		return fmt.Errorf("no olric client")
	}
	probeCtx, cancel := context.WithTimeout(ctx, olricProbeInterval)
	defer cancel()
	return client.Health(probeCtx)
}

// sleepCtx waits for d, and reports false if the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
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
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
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
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
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
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
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
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
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
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
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
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
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
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
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

// configureRateLimiters sets both buckets, so their relative size is one
// decision in one place and a test can check it.
//
// 30 credential operations a minute from one address, bursting to 10, against
// a general 10,000 a minute. A person signing in does two or three; anyone
// grinding nonces, signatures or token exchanges does far more, and the
// general limit is no obstacle at all to that.
func configureRateLimiters(gw *Gateway) {
	gw.rateLimiter = NewRateLimiter(10000, 5000)
	gw.rateLimiter.StartCleanup(5*time.Minute, 10*time.Minute)

	gw.authRateLimiter = NewRateLimiter(30, 10)
	gw.authRateLimiter.StartCleanup(5*time.Minute, 10*time.Minute)
}

// apiKeyRegistryProbeTimeout bounds the one query that proves the registry is
// there. Short: this runs on the boot path, and a registry that cannot answer
// a trivial read in this long is not one to start serving against.
const apiKeyRegistryProbeTimeout = 15 * time.Second

// usesSeparateAPIKeyRegistry reports whether this gateway validates API keys
// against a registry other than its own database.
//
// A namespace gateway does: its own rqlite is the tenant's, and keys live in
// the cluster's. The index gateway does not — there, its own database is the
// registry.
func usesSeparateAPIKeyRegistry(cfg *Config) bool {
	return cfg.GlobalRQLiteDSN != "" && cfg.GlobalRQLiteDSN != cfg.RQLiteDSN
}

// connectAPIKeyRegistry opens the client API-key validation reads from, or
// returns (nil, nil) when this gateway's own database is the registry.
//
// A failure here used to be a warning. The gateway then carried on with no
// registry client, and apiKeyDB() fell back to the local database — which on a
// namespace gateway is the tenant's own rqlite, holding an api_keys table the
// core migrations created there and the tenant can write. A gateway that could
// not reach the registry did not stop authenticating; it started authenticating
// against a table the tenant controls.
//
// There is no safe store to fall back to, so this is fatal. The consequence is
// deliberate: while the cluster's registry is unreachable, a namespace gateway
// does not start. Serving with the wrong idea of who holds which key is worse
// than not serving.
func connectAPIKeyRegistry(cfg *Config, logger *logging.ColoredLogger) (client.NetworkClient, error) {
	if !usesSeparateAPIKeyRegistry(cfg) {
		return nil, nil
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Creating global auth client...",
		zap.String("global_dsn", cfg.GlobalRQLiteDSN),
	)

	authCfg := client.DefaultClientConfig("default") // the registry is not a tenant namespace
	authCfg.DatabaseEndpoints = []string{injectRQLiteAuth(cfg.GlobalRQLiteDSN, cfg.RQLiteUsername, cfg.RQLitePassword)}
	if len(cfg.BootstrapPeers) > 0 {
		authCfg.BootstrapPeers = cfg.BootstrapPeers
	}

	registryClient, err := client.NewClient(authCfg)
	if err != nil {
		return nil, fmt.Errorf("this gateway validates API keys against the registry at %s, but the client "+
			"could not be created and there is no safe store to use instead: %w", cfg.GlobalRQLiteDSN, err)
	}
	// Connect brings up the client's own P2P side. It reports success without
	// having spoken to the database, so it is not evidence that the registry
	// is there — the probe below is. It is still checked, because a client
	// that could not start is a different failure and says so.
	if err := registryClient.Connect(); err != nil {
		return nil, fmt.Errorf("this gateway validates API keys against the registry at %s, but its client "+
			"could not start and there is no safe store to use instead: %w", cfg.GlobalRQLiteDSN, err)
	}

	// Ask the registry a question only the registry can answer.
	probeCtx, cancel := context.WithTimeout(context.Background(), apiKeyRegistryProbeTimeout)
	defer cancel()
	if _, err := registryClient.Database().Query(probeCtx, "SELECT 1 FROM api_keys LIMIT 1"); err != nil {
		registryClient.Disconnect()
		return nil, fmt.Errorf("this gateway validates API keys against the registry at %s, but it did not "+
			"answer and there is no safe store to use instead: %w", cfg.GlobalRQLiteDSN, err)
	}

	return registryClient, nil
}

// deploymentEnvDir is where the deployments' environment files live: beside
// their code, not inside it. A deployment's own directory has to be readable by
// the unprivileged user the deployment runs as, and its environment must not
// be.
func deploymentEnvDir(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	return filepath.Join(dataDir, "deployment-env")
}
