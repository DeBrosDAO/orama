package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/push"
	pushcreds "github.com/DeBrosOfficial/network/pkg/push/credentials"
	pushapns "github.com/DeBrosOfficial/network/pkg/push/providers/apns"
	pushexpo "github.com/DeBrosOfficial/network/pkg/push/providers/expo"
	pushntfy "github.com/DeBrosOfficial/network/pkg/push/providers/ntfy"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/hostfunctions"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"github.com/DeBrosOfficial/network/pkg/serverless/wsbridge"
	"github.com/multiformats/go-multiaddr"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"

	_ "github.com/rqlite/gorqlite/stdlib"
)

const (
	olricInitMaxAttempts    = 5
	olricInitInitialBackoff = 500 * time.Millisecond
	olricInitMaxBackoff     = 5 * time.Second

	// rqliteReadyTimeout bounds how long gateway startup waits for the local
	// RQLite to become leader-reachable before giving up. Generous enough to
	// cover a cold node whose raft is still replaying (a large namespace log
	// can take tens of seconds), short enough that a genuinely broken cluster
	// surfaces a clear error instead of hanging forever.
	rqliteReadyTimeout = 90 * time.Second
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

	// PubSub trigger dispatcher (used to wire into PubSubHandlers)
	PubSubDispatcher *triggers.PubSubDispatcher

	// Cron trigger store + scheduler. The scheduler is started by gateway
	// lifecycle code after Dependencies is constructed; Stop is called
	// during shutdown.
	CronTriggerStore *triggers.CronTriggerStore
	CronScheduler    *triggers.CronScheduler

	// PersistentWSManager tracks long-lived WS function instances.
	// Used by the WS handler when fn.WSPersistent=true; nil = disabled.
	PersistentWSManager *persistent.Manager

	// WSBridge wires PubSub topics directly to WS clients on this gateway.
	// Used by the ws_pubsub_bridge host function. Nil = disabled.
	WSBridge *wsbridge.Bridge

	// Push notification dispatcher (legacy single-tier; nil when push
	// isn't configured at all). When PushManager is also set, send paths
	// route through the manager instead so per-namespace config wins.
	PushDispatcher  *push.PushDispatcher
	PushDeviceStore push.PushDeviceStore

	// PushManager wraps the device store + per-namespace config store so
	// tenants self-serve their push provider config via PUT /v1/push/config.
	// Nil when push subsystem isn't initialized (cluster secret missing).
	// When set, this is the canonical send path; PushDispatcher is the
	// fallback used only if Manager is somehow missing.
	PushManager     *push.Manager
	PushConfigStore push.ConfigStore

	// PushCredentialsManager owns per-namespace, per-provider push
	// credentials (feature #72). Used by provider factories to look up
	// the right credential at send time, and by the HTTP credentials
	// handlers for tenant self-service PUT/GET/DELETE. Nil when the
	// cluster secret is unavailable.
	PushCredentialsManager *pushcreds.Manager

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
	// Ensure the gorqlite client can reach the local RQLite instance.
	// Without this, gorqlite has zero endpoints and all DB queries fail.
	if len(cliCfg.DatabaseEndpoints) == 0 {
		dsn := cfg.RQLiteDSN
		if dsn == "" {
			dsn = "http://localhost:5001"
		}
		dsn = injectRQLiteAuth(dsn, cfg.RQLiteUsername, cfg.RQLitePassword)
		cliCfg.DatabaseEndpoints = []string{dsn}
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

	// Inject basic auth credentials into DSN if available
	dsn = injectRQLiteAuth(dsn, cfg.RQLiteUsername, cfg.RQLitePassword)

	dsn = appendRQLiteQueryParams(dsn)
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
	// Use the DSN-aware constructor so the ORM client also has a native
	// *gorqlite.Connection for atomic Batch operations. If the native dial
	// fails, fall back to the stdlib-only client (Batch will be unavailable
	// but everything else works).
	orm, ormErr := rqlite.NewClientWithDSN(db, dsn)
	if ormErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral,
			"native gorqlite dial failed, atomic Batch will be unavailable",
			zap.Error(ormErr))
		orm = rqlite.NewClient(db)
	}
	deps.ORMClient = orm
	deps.ORMHTTP = rqlite.NewHTTPGateway(orm, "/v1/db")
	// Set a reasonable timeout for HTTP requests (30 seconds)
	deps.ORMHTTP.Timeout = 30 * time.Second

	logger.ComponentInfo(logging.ComponentGeneral, "RQLite ORM HTTP gateway ready",
		zap.String("dsn", dsn),
		zap.String("base_path", "/v1/db"),
		zap.Duration("timeout", deps.ORMHTTP.Timeout),
	)

	// Wait for the local RQLite to actually be able to serve a leader-routed
	// read before touching the schema.
	//
	// sql.Open above is lazy and rqlite accepts connections well before it has
	// elected a leader, so without this the migrations below fire into a
	// database that cannot answer them. systemd's After=/Requires= on the
	// rqlite unit does not help: for Type=simple it orders process start, not
	// readiness. Gating on the real signal turns "gateway dies at boot because
	// the database was 3 seconds behind it" into "gateway waits 3 seconds".
	readyCtx, readyCancel := context.WithTimeout(context.Background(), rqliteReadyTimeout)
	defer readyCancel()
	if err := rqlite.WaitForLeader(readyCtx, db, rqliteReadyTimeout); err != nil {
		return fmt.Errorf("rqlite not ready for schema work: %w", err)
	}
	logger.ComponentInfo(logging.ComponentGeneral, "RQLite ready (leader reachable), applying schema")

	// Apply embedded migrations to ensure schema is up-to-date.
	// This is critical for namespace gateways whose RQLite instances
	// don't get migrations from the main cluster RQLiteManager.
	migCtx, migCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer migCancel()

	// A NAMESPACE gateway's RQLite is ALSO the tenant app's own database (they
	// read/write it via /v1/rqlite and /v1/db/sqlite). Apply core's schema under
	// an ISOLATED tracker (orama_schema_migrations) and free the generic table
	// names the tenant needs to own — "schema_migrations" and the dead pubsub
	// "subscriptions" — so core and app schema never collide (bugboard #150).
	// Detection mirrors the global-auth-client check: a namespace gateway is the
	// one configured with a separate GlobalRQLiteDSN pointing at the main cluster.
	if cfg.GlobalRQLiteDSN != "" && cfg.GlobalRQLiteDSN != cfg.RQLiteDSN {
		if err := rqlite.ApplyEmbeddedMigrationsNamespace(migCtx, db, migrations.FS, logger.Logger); err != nil {
			return fmt.Errorf("apply namespace embedded migrations failed: %w "+
				"(hint: this namespace gateway can't safely run without its required schema; "+
				"check the namespace RQLite health and re-run startup)", err)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "Namespace-isolated migrations applied to gateway RQLite")

		// Schema-version contract against the ISOLATED tracker (a namespace
		// RQLite records core's applied versions in orama_schema_migrations,
		// leaving schema_migrations for the tenant).
		applied, err := rqlite.AppliedVersionFromTracker(migCtx, db, rqlite.NamespaceMigrationsTracker())
		if err != nil {
			return fmt.Errorf("namespace schema contract read failed: %w", err)
		}
		if required := migrations.RequiredVersion(); applied < required {
			return fmt.Errorf("namespace schema contract violation: applied=%d, required=%d", applied, required)
		}
		logger.ComponentInfo(logging.ComponentGeneral, "Namespace schema contract satisfied",
			zap.Int("required_version", migrations.RequiredVersion()))
		return nil
	}

	// Main cluster: full core schema, tracked in the standard schema_migrations.
	//
	// Failures here are FATAL: a gateway that can't bring its schema up
	// to the version its binary expects will silently corrupt deploys
	// later (e.g. INSERTing into missing columns and surfacing as a
	// cryptic SQL error to end users). Better to refuse to start with
	// a clear actionable error.
	if err := rqlite.ApplyEmbeddedMigrations(migCtx, db, migrations.FS, logger.Logger); err != nil {
		return fmt.Errorf("apply embedded migrations failed: %w "+
			"(hint: this gateway can't safely run without its required schema; "+
			"check the underlying RQLite cluster health and re-run startup)", err)
	}
	logger.ComponentInfo(logging.ComponentGeneral, "Embedded migrations applied to gateway RQLite")

	// Schema-version contract: even if the apply call returned nil, verify
	// that the highest migration the binary embeds is recorded as applied.
	// Catches:
	//   - silent partial-apply states where the marker row was never written
	//   - clusters where the binary was upgraded but RQLite has stale schema
	//   - operator manually deleted rows from schema_migrations
	if err := migrations.AssertSchema(migCtx, db); err != nil {
		return fmt.Errorf("schema contract violation: %w", err)
	}
	logger.ComponentInfo(logging.ComponentGeneral, "Schema contract satisfied",
		zap.Int("required_version", migrations.RequiredVersion()))

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

	// Backfill: re-pin every active function's WASM cluster-wide so functions
	// deployed before the pin-everywhere fix are durable + GC-safe. Incident
	// 2026-06-24: scheduled IPFS GC deleted unpinned function WASM. New deploys
	// are covered by uploadWASM's pin-everywhere + hard-fail; this protects
	// existing ones. Best-effort + idempotent (the cluster dedups pins), delayed
	// so it never races gateway startup. The rolling-restart cadence naturally
	// staggers it across nodes; the first node to run it pins everything
	// cluster-wide and the rest are no-ops.
	go func() {
		time.Sleep(2 * time.Minute) // let the namespace + IPFS cluster settle
		bctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		n, berr := registry.RepinAllWASM(bctx)
		if berr != nil {
			logger.Logger.Warn("serverless: WASM re-pin backfill failed", zap.Error(berr))
			return
		}
		logger.Logger.Info("serverless: WASM re-pin backfill complete", zap.Int("pinned", n))
	}()

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

	// Create WASM engine configuration (needed before secrets manager)
	engineCfg := serverless.DefaultConfig()
	engineCfg.DefaultMemoryLimitMB = 128
	engineCfg.MaxMemoryLimitMB = 256
	engineCfg.DefaultTimeoutSeconds = 30
	engineCfg.MaxTimeoutSeconds = 60
	engineCfg.ModuleCacheSize = 100
	// Surface the per-phase slow-invoke diagnostic (instantiate_ms / run_ms)
	// above 1s instead of the 5s default — a >1s serverless invocation is
	// genuinely slow (well-built handlers are <300ms), and this makes the
	// cold-start floor (bugboard #27: async-dispatched stateless handlers pay a
	// fresh instantiate + TinyGo _start per call) visible for correlation
	// against client-side request_ids.
	engineCfg.SlowInvokeThresholdMs = 1000

	// Create secrets manager for serverless functions (AES-256-GCM encrypted).
	//
	// The encryption key is DERIVED from the cluster secret via HKDF
	// (resolveSecretsEncryptionKeyHex), so every gateway in the cluster computes
	// the identical key and a secret written on one node decrypts on every other
	// node and survives rolling upgrades. This replaces the old per-node
	// crypto/rand key file, whose divergence across an upgraded cluster kept
	// get_secret broken (bugboard #837). The file key (cfg.SecretsEncryptionKey)
	// remains only as a fallback when no cluster secret is available (legacy /
	// single-node test rigs). allowEphemeral=false: a missing/invalid key fails
	// loudly here and disables get_secret rather than silently corrupting
	// secrets.
	var secretsMgr serverless.SecretsManager
	if secretsKeyHex, keyErr := resolveSecretsEncryptionKeyHex(cfg.ClusterSecret, cfg.SecretsEncryptionKey); keyErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "Failed to derive secrets encryption key; get_secret will be unavailable",
			zap.Error(keyErr))
	} else if smImpl, secretsErr := hostfunctions.NewDBSecretsManager(deps.ORMClient, secretsKeyHex, false, logger.Logger); secretsErr != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "Failed to initialize secrets manager; get_secret will be unavailable",
			zap.Error(secretsErr))
	} else {
		secretsMgr = smImpl
	}

	// Initialize push notification subsystem.
	//
	// Bug #220 follow-up: the subsystem now ALWAYS initializes when the
	// cluster secret is available (so tenants can register devices and
	// PUT their per-namespace push config), regardless of whether the
	// gateway YAML has a default provider configured. The Manager wraps
	// the device store + per-namespace ConfigStore; Send paths route
	// through Manager so per-namespace config takes effect.
	//
	// PushDispatcher (legacy) is set only when YAML defaults exist —
	// kept for back-compat with code that hasn't migrated to Manager.
	pushDispatcher, pushStore, pushManager, pushCfgStore, pushCredManager, err := buildPushDispatcher(cfg, deps.ORMClient, deps.Client, logger)
	if err != nil {
		// Non-fatal: log and continue. Functions calling push_send will get nil
		// (silent no-op) and HTTP /v1/push/* endpoints return 503.
		logger.ComponentWarn(logging.ComponentGeneral,
			"push notifications disabled (init failed)", zap.Error(err))
	}
	deps.PushDispatcher = pushDispatcher
	deps.PushDeviceStore = pushStore
	deps.PushManager = pushManager
	deps.PushConfigStore = pushCfgStore
	deps.PushCredentialsManager = pushCredManager

	// Create host functions provider (allows functions to call Orama services)
	hostFuncsCfg := hostfunctions.HostFunctionsConfig{
		IPFSAPIURL:  cfg.IPFSAPIURL,
		HTTPTimeout: 30 * time.Second,
		// feat-9 — TURN config for the turn_credentials host fn.
		// Empty TURNSecret → host fn returns {configured:false} envelope
		// (same shape as the HTTP endpoint's 503 semantically).
		TURNDomain:       cfg.TURNDomain,
		TURNSecret:       cfg.TURNSecret,
		StealthCDNDomain: cfg.StealthCDNDomain,
	}
	// WS-PubSub bridge: wire PubSub topics directly to WS clients without
	// per-event WASM invocation. The bridge is a thin layer over the
	// pubsub adapter + WSManager.
	deps.WSBridge = wsbridge.New(pubsubAdapter, deps.ServerlessWSMgr, logger.Logger)

	hostFuncs := hostfunctions.NewHostFunctions(
		deps.ORMClient,
		olricClient,
		deps.IPFSClient,
		pubsubAdapter, // pubsub adapter for serverless functions
		deps.ServerlessWSMgr,
		secretsMgr,
		pushDispatcher, // legacy — fallback when manager isn't wired
		pushManager,    // bug #220 follow-up — per-namespace config
		deps.WSBridge,  // may be nil; WSPubSubBridge returns explicit error
		hostFuncsCfg,
		logger.Logger,
	)

	// Create WASM engine with multi-tier rate limiter (per-(ns, fn, wallet, ip),
	// per-(ns, wallet), per-(ns)). The legacy global limit is honored as
	// the per-namespace ceiling so no behavior regresses for existing deployments.
	rlCfg := serverless.DefaultLimiterConfig()
	if engineCfg.GlobalRateLimitPerMinute > 0 {
		rlCfg.PerNamespacePerMinute = engineCfg.GlobalRateLimitPerMinute
	}
	rateLimiter := serverless.NewMultiTierLimiter(rlCfg)
	engine, err := serverless.NewEngine(engineCfg, registry, hostFuncs, logger.Logger,
		serverless.WithInvocationLogger(registry),
		serverless.WithRateLimiter(rateLimiter),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize serverless engine: %w", err)
	}
	deps.ServerlessEngine = engine

	// Create invoker
	deps.ServerlessInvoker = serverless.NewInvoker(engine, registry, hostFuncs, logger.Logger)

	// Wire the invoker back into hostFuncs so the function_invoke host
	// function can dispatch sub-invocations from inside a WASM function
	// (e.g. rpc-router routing client RPCs to per-op handlers).
	hostFuncs.SetInvoker(deps.ServerlessInvoker)

	// Create PubSub trigger store and dispatcher
	triggerStore := triggers.NewPubSubTriggerStore(deps.ORMClient, logger.Logger)

	var olricUnderlying olriclib.Client
	if deps.OlricClient != nil {
		olricUnderlying = deps.OlricClient.UnderlyingClient()
	}
	// Pass the pubsub adapter so the dispatcher can subscribe to libp2p
	// for every literal trigger pattern (bugboard #282 fix). nil-safe:
	// dispatcher's Start/Refresh become no-ops when adapter is unavailable,
	// preserving the legacy HTTP-only Dispatch hook.
	deps.PubSubDispatcher = triggers.NewPubSubDispatcher(
		triggerStore,
		deps.ServerlessInvoker,
		olricUnderlying,
		pubsubAdapter,
		logger.Logger,
	)

	// Wire the dispatcher into hostFuncs so PubSubPublish /
	// PubSubPublishBatch fire local wildcard triggers immediately on
	// publish — closes the bugboard #93 gap where WASM publishes to e.g.
	// "presence:user-1" never reached wildcard handlers like "presence:*"
	// because libp2p has no wildcard subscribe.
	hostFuncs.SetTriggerDispatcher(deps.PubSubDispatcher)

	// Cron trigger store + scheduler. The scheduler polls
	// function_cron_triggers and invokes due rows via the same
	// ServerlessInvoker used for PubSub triggers; the ↓ Start call wires
	// the goroutine up — Stop is invoked from gateway lifecycle shutdown.
	cronStore := triggers.NewCronTriggerStore(deps.ORMClient, logger.Logger)
	deps.CronTriggerStore = cronStore
	deps.CronScheduler = triggers.NewCronScheduler(
		cronStore,
		deps.ServerlessInvoker,
		logger.Logger,
		30*time.Second,
	)

	// Persistent WS instance manager. Cap from gateway config (TODO: surface
	// the knob); 5000 is a sensible default per plan 06.
	deps.PersistentWSManager = persistent.NewManager(5000, logger.Logger)

	// Create HTTP handlers
	deps.ServerlessHandlers = serverlesshandlers.NewServerlessHandlers(
		deps.ServerlessInvoker,
		deps.ServerlessEngine,
		registry,
		deps.ServerlessWSMgr,
		triggerStore,
		cronStore,
		deps.PubSubDispatcher,
		deps.PersistentWSManager,
		deps.WSBridge,
		secretsMgr,
		logger.Logger,
	)

	// Initialize auth service with persistent signing keys (RSA + EdDSA)
	keyPEM, err := loadOrCreateSigningKey(cfg.DataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to load or create JWT signing key: %w", err)
	}
	authService, err := auth.NewService(logger, networkClient, string(keyPEM), cfg.ClientNamespace)
	if err != nil {
		return fmt.Errorf("failed to initialize auth service: %w", err)
	}

	// Inject the lower-level rqlite client for code paths that need
	// rows-affected feedback. Feature #68 (atomic refresh-token rotation)
	// uses this for the compare-and-swap UPDATE. Without it, RefreshToken
	// returns ErrRotationNotConfigured rather than rotating non-atomically.
	if deps.ORMClient != nil {
		authService.SetRqliteClient(deps.ORMClient)
	}

	// Wire the namespace claims-provider hook (bugboard #548): at JWT mint time
	// the auth service invokes the namespace's reserved `auth-claims-provider`
	// function (if deployed) and merges its additive claims (e.g. account_id)
	// into the token. Fail-open — a missing/slow provider never breaks auth.
	if deps.ServerlessInvoker != nil {
		authService.SetClaimsResolver(newJWTClaimsProvider(deps.ServerlessInvoker, logger.Logger))
	}

	// Load or create EdDSA key for new JWT tokens. Bug #215 fix: when
	// cfg.ClusterSecret is set, the key is derived deterministically from
	// it via HKDF, so every gateway in the cluster shares the same Ed25519
	// keypair and JWTs verify cross-node. With an empty ClusterSecret the
	// per-node legacy behaviour is retained (single-node test deployments).
	if cfg.ClusterSecret == "" {
		// Loud warning: a multi-node cluster booted without a cluster
		// secret reproduces bug #215 (per-gateway random keys, JWTs
		// unverifiable cross-node). Single-node test rigs are the only
		// legitimate case.
		logger.ComponentWarn(logging.ComponentGeneral,
			"ClusterSecret is empty; JWT signing keys will be random per-node. "+
				"Multi-node clusters MUST set ClusterSecret or JWTs will not verify across gateways (bug #215).")
	}
	edKey, err := loadOrCreateEdSigningKey(cfg.DataDir, cfg.ClusterSecret, logger)
	if err != nil {
		logger.ComponentWarn(logging.ComponentGeneral, "Failed to load EdDSA signing key; new JWTs will use RS256",
			zap.Error(err))
	} else {
		authService.SetEdDSAKey(edKey)
		logger.ComponentInfo(logging.ComponentGeneral, "EdDSA signing key loaded; new JWTs will use EdDSA")
	}

	// Configure API key HMAC secret if available
	if cfg.APIKeyHMACSecret != "" {
		authService.SetAPIKeyHMACSecret(cfg.APIKeyHMACSecret)
		logger.ComponentInfo(logging.ComponentGeneral, "API key HMAC secret loaded; new API keys will be hashed")
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

// injectRQLiteAuth injects HTTP basic auth credentials into a RQLite DSN URL.
// If username or password is empty, the DSN is returned unchanged.
// Input: "http://localhost:5001" → Output: "http://orama:secret@localhost:5001"
func injectRQLiteAuth(dsn, username, password string) string {
	if username == "" || password == "" {
		return dsn
	}
	// Insert user:pass@ after the scheme (http:// or https://)
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(dsn, scheme) {
			return scheme + username + ":" + password + "@" + dsn[len(scheme):]
		}
	}
	return dsn
}

// appendRQLiteQueryParams adds the standard query parameters to a RQLite DSN:
//
//   - `disableClusterDiscovery=true` — gorqlite's discovery /nodes call is
//     unreliable when peers are unreachable; we manage topology ourselves.
//   - `level=weak` — Bug #235. Reads route to the leader (the only node
//     guaranteed to have all committed writes), so a SELECT after an UPDATE
//     in the same serverless invocation sees the new state. Previously
//     `level=none`, which read from the local follower's possibly-stale
//     snapshot. gorqlite's upstream default is `weak`; we were overriding
//     to `none` and that hid this bug.
//
// The cost of `weak` over `none` is one HTTP hop to the leader (~1-2ms over
// the WireGuard mesh) and applies only to reads. Writes are unaffected
// because rqlite always redirects them to the leader regardless of `level`.
func appendRQLiteQueryParams(dsn string) string {
	const params = "disableClusterDiscovery=true&level=weak"
	if strings.Contains(dsn, "?") {
		return dsn + "&" + params
	}
	return dsn + "?" + params
}

// buildPushDispatcher constructs the push subsystem.
//
// As of bug #220 follow-up, push always initializes when ClusterSecret is
// available, regardless of whether any YAML provider config is set:
//
//   - Device store + ConfigStore always build (tenants need to register
//     devices and set per-namespace push config even on gateways with no
//     YAML defaults).
//   - Manager wraps the stores + a YAML-derived Defaults fallback. Each
//     namespace can override any default via PUT /v1/push/config.
//   - The legacy single-tier dispatcher is built only when YAML defaults
//     are non-empty — kept for back-compat with code paths that haven't
//     migrated to Manager.
//
// Returns (nil, nil, nil, nil, nil) when ClusterSecret is missing
// (push subsystem disabled — credentials can't be encrypted safely).
// Returns hard error only on store-init failure.
func buildPushDispatcher(
	cfg *Config,
	db rqlite.Client,
	globalDB client.NetworkClient,
	logger *logging.ColoredLogger,
) (*push.PushDispatcher, push.PushDeviceStore, *push.Manager, push.ConfigStore, *pushcreds.Manager, error) {
	if cfg.ClusterSecret == "" {
		// Without the cluster secret we can't encrypt credentials at rest.
		// Disable the whole push subsystem; HTTP routes return 503.
		return nil, nil, nil, nil, nil, nil
	}

	store, err := push.NewRqliteDeviceStore(db, cfg.ClusterSecret, logger.Logger)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("init push device store: %w", err)
	}

	// Backfill token_fp for rows registered before the bugboard #981 migration
	// so token-exclusive eviction also covers pre-existing orphans. Best-effort
	// + idempotent; runs in the background so it never delays gateway startup.
	//
	// It can lose a startup race with migration 033 (the column doesn't exist
	// yet → "no such column"); retry until the migration applies. This was a real
	// bug observed live: the backfill failed permanently on the first attempt.
	go func() {
		bctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for {
			n, berr := store.BackfillTokenFP(bctx)
			if berr == nil {
				if n > 0 {
					logger.Logger.Info("push: token_fp backfill complete", zap.Int("updated", n))
				}
				return
			}
			if strings.Contains(berr.Error(), "no such column") {
				select {
				case <-time.After(15 * time.Second):
					continue // migration 033 not applied yet; retry
				case <-bctx.Done():
					return
				}
			}
			logger.Logger.Warn("push: token_fp backfill failed", zap.Error(berr))
			return
		}
	}()

	cfgStore, err := push.NewRqliteConfigStore(db, cfg.ClusterSecret, logger.Logger)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("init push config store: %w", err)
	}

	// Per-namespace, per-provider credentials (feature #72). Generic
	// store — used by APNs, ntfy (post-migration), FCM-direct (future).
	// Provider packages register their Validator at gateway startup
	// (see pushcreds.Register calls below).
	credStore, err := pushcreds.NewRqliteStore(db, cfg.ClusterSecret, logger.Logger)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("init push credentials store: %w", err)
	}
	credManager := pushcreds.NewManager(credStore, logger.Logger)

	// Register the Validators that this gateway accepts. Each provider
	// package owns its own JSON schema + redactor; we tell the
	// credentials package which ones to allow at PUT/GET time. Adding a
	// new provider (FCM-direct, SMS, etc.) means a single new Register
	// call here — no other code needs to know.
	pushcreds.Register(pushapns.NewValidator())
	pushcreds.Register(pushntfy.NewValidator())

	// ntfy cluster fan-out (bugboard #858): the default push infra runs an
	// independent ntfy per node with no shared store, so a publish must reach
	// EVERY active node for the subscriber's instance (picked by round-robin
	// DNS) to receive it. Build a resolver over the global dns_nodes table; the
	// factory attaches it only to providers using the shared default base URL
	// (a namespace pointing ntfy at its own server is never fanned across our
	// cluster). nil globalDB or an unparseable base URL → no fan-out (provider
	// falls back to the single base URL).
	var ntfyFanout *ntfyFanoutResolver
	var ntfyFanoutHost string
	if globalDB != nil {
		if base := strings.TrimSpace(cfg.NtfyBaseURL); base != "" {
			if u, perr := url.Parse(base); perr == nil && u.Hostname() != "" {
				ntfyFanoutHost = u.Hostname()
				ntfyFanout = newNtfyFanoutResolver(globalDB, u.Scheme, u.Port(), defaultNtfyFanoutTTL)
			}
		}
	}

	// ProviderFactory turns a resolved Config into the right set of
	// provider instances. Lives here in dependencies.go because this is
	// the only place that imports both the manager package and the
	// concrete provider sub-packages — keeps push core dep-cycle-free.
	//
	// Per-namespace credentialed providers (APNs — feature #72) are
	// constructed here by consulting the credentials manager. If a
	// namespace has stored credentials for a provider, that provider is
	// instantiated with those credentials and registered in the
	// dispatcher; otherwise it's omitted.
	factory := func(ctx context.Context, c push.Config) []push.PushProvider {
		var ps []push.PushProvider

		// ntfy provider — sourced from EITHER the new credentials store
		// (#72, preferred) OR the legacy 026 push_config row. New table
		// wins field-by-field; legacy fills any gap. ntfy is registered
		// only if a BaseURL ends up set; auth_token alone is useless
		// without a server to point at.
		ntfyCfg := pushntfy.Config{
			BaseURL:   c.NtfyBaseURL,
			AuthToken: c.NtfyAuthToken,
		}
		if c.Namespace != "" && credManager != nil {
			if cred, err := credManager.Get(ctx, c.Namespace, "ntfy"); err == nil && cred != nil {
				if ov, perr := pushntfy.ParseCredentials(cred.JSON); perr == nil {
					if ov.BaseURL != "" {
						ntfyCfg.BaseURL = ov.BaseURL
					}
					if ov.AuthToken != "" {
						ntfyCfg.AuthToken = ov.AuthToken
					}
				} else {
					logger.ComponentWarn(logging.ComponentGeneral,
						"ntfy credentials parse failed",
						zap.String("namespace", c.Namespace),
						zap.Error(perr))
				}
			}
		}
		if ntfyCfg.BaseURL != "" {
			// Fan out across all push nodes ONLY for the shared default infra.
			// A namespace that overrode BaseURL with its own ntfy server keeps
			// single-host delivery (its server, not our cluster).
			if ntfyFanout != nil && ntfyCfg.BaseURL == cfg.NtfyBaseURL {
				ntfyCfg.FanoutResolver = ntfyFanout.Hosts
				ntfyCfg.FanoutHostHeader = ntfyFanoutHost
			}
			ps = append(ps, pushntfy.New(ntfyCfg, logger.Logger))
		}
		if c.ExpoAccessToken != "" {
			ps = append(ps, pushexpo.New(pushexpo.Config{
				AccessToken: c.ExpoAccessToken,
			}, logger.Logger))
		}
		// APNs is fully credentialed — no YAML fallback. The presence of
		// per-namespace credentials is the trigger. Bugboard #408: a
		// single set of APNs credentials spawns BOTH an alert-kind
		// provider (registered as "apns") AND a VoIP/PushKit provider
		// (registered as "apns_voip"). Both share the same JWT signer +
		// HTTP/2 client pool — VoIP only differs in the per-Send wire
		// format (topic suffix, apns-push-type header, empty-payload
		// acceptance). Tenants register PushKit voipPushTokens against
		// provider="apns_voip" and the dispatcher routes accordingly.
		if c.Namespace != "" && credManager != nil {
			if cred, err := credManager.Get(ctx, c.Namespace, "apns"); err == nil && cred != nil {
				if apnsCfg, perr := pushapns.ParseCredentials(cred.JSON); perr == nil {
					if provider, nerr := pushapns.New(apnsCfg, logger.Logger); nerr == nil {
						ps = append(ps, provider)
					} else {
						logger.ComponentWarn(logging.ComponentGeneral,
							"apns provider construction failed",
							zap.String("namespace", c.Namespace),
							zap.Error(nerr))
					}
					if voipProvider, nerr := pushapns.NewVoIP(apnsCfg, logger.Logger); nerr == nil {
						ps = append(ps, voipProvider)
					} else {
						logger.ComponentWarn(logging.ComponentGeneral,
							"apns_voip provider construction failed",
							zap.String("namespace", c.Namespace),
							zap.Error(nerr))
					}
				} else {
					logger.ComponentWarn(logging.ComponentGeneral,
						"apns credentials parse failed",
						zap.String("namespace", c.Namespace),
						zap.Error(perr))
				}
			}
		}
		return ps
	}

	defaults := push.Defaults{
		NtfyBaseURL:     cfg.NtfyBaseURL,
		NtfyAuthToken:   cfg.NtfyAuthToken,
		ExpoAccessToken: cfg.ExpoAccessToken,
	}
	manager := push.NewManager(store, cfgStore, defaults, factory, logger.Logger)

	// Legacy single-tier dispatcher kept ONLY when YAML defaults exist —
	// some non-Manager code paths (notably the WASM push_send hostfunc
	// before its migration to Manager) still expect a populated
	// PushDispatcher. New code routes via Manager.
	var legacy *push.PushDispatcher
	if !defaults.IsEmpty() {
		legacy = push.New(store, logger.Logger)
		// Boot-time construction: no request context yet. Use Background
		// — the credential lookups here are fast (in-memory cache miss
		// reads rqlite once) and cancellation is irrelevant during boot.
		for _, p := range factory(context.Background(), push.Config{
			NtfyBaseURL:     defaults.NtfyBaseURL,
			NtfyAuthToken:   defaults.NtfyAuthToken,
			ExpoAccessToken: defaults.ExpoAccessToken,
		}) {
			legacy.Register(p)
		}
	}

	if defaults.NtfyBaseURL != "" {
		logger.ComponentInfo(logging.ComponentGeneral, "push default provider: ntfy",
			zap.String("base_url", defaults.NtfyBaseURL))
	}
	if defaults.ExpoAccessToken != "" {
		logger.ComponentInfo(logging.ComponentGeneral, "push default provider: expo configured")
	}
	logger.ComponentInfo(logging.ComponentGeneral,
		"push subsystem initialized; tenants can self-serve via PUT /v1/push/config")

	return legacy, store, manager, cfgStore, credManager, nil
}
