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

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/health"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	authhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/cache"
	deploymentshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/deployments"
	pubsubhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/pubsub"
	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
	wireguardhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/wireguard"
	sqlitehandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/sqlite"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)


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
	cacheHandlers *cache.CacheHandlers

	// IPFS storage client
	ipfsClient      ipfs.IPFSClient
	storageHandlers *storage.Handlers

	// Local pub/sub bypass for same-gateway subscribers
	localSubscribers map[string][]*localSubscriber // topic+namespace -> subscribers
	presenceMembers  map[string][]PresenceMember   // topicKey -> members
	mu               sync.RWMutex
	presenceMu       sync.RWMutex
	pubsubHandlers   *pubsubhandlers.PubSubHandlers

	// Serverless function engine
	serverlessEngine   *serverless.Engine
	serverlessRegistry *serverless.Registry
	serverlessInvoker  *serverless.Invoker
	serverlessWSMgr    *serverless.WSManager
	serverlessHandlers *serverlesshandlers.ServerlessHandlers

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

	// Rate limiter
	rateLimiter *RateLimiter

	// WireGuard peer exchange
	wireguardHandler *wireguardhandlers.Handler

	// Node join handler
	joinHandler *joinhandlers.Handler

	// Cluster provisioning for namespace clusters
	clusterProvisioner authhandlers.ClusterProvisioner
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

// authDatabaseAdapter adapts client.DatabaseClient to authhandlers.DatabaseClient
type authDatabaseAdapter struct {
	db client.DatabaseClient
}

func (a *authDatabaseAdapter) Query(ctx context.Context, sql string, args ...interface{}) (*authhandlers.QueryResult, error) {
	result, err := a.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
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
	}

	// Initialize handler instances
	gw.pubsubHandlers = pubsubhandlers.NewPubSubHandlers(deps.Client, logger)

	if deps.OlricClient != nil {
		gw.cacheHandlers = cache.NewCacheHandlers(logger, deps.OlricClient)
	}

	if deps.IPFSClient != nil {
		gw.storageHandlers = storage.New(deps.IPFSClient, logger, storage.Config{
			IPFSReplicationFactor: cfg.IPFSReplicationFactor,
			IPFSAPIURL:            cfg.IPFSAPIURL,
		}, deps.ORMClient)
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
	}

	// Initialize rate limiter (300 req/min, burst 50)
	gw.rateLimiter = NewRateLimiter(300, 50)
	gw.rateLimiter.StartCleanup(5*time.Minute, 10*time.Minute)

	// Initialize WireGuard peer exchange handler
	if deps.ORMClient != nil {
		gw.wireguardHandler = wireguardhandlers.NewHandler(logger.Logger, deps.ORMClient, cfg.ClusterSecret)
		gw.joinHandler = joinhandlers.NewHandler(logger.Logger, deps.ORMClient, cfg.DataDir)
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
		gw.deploymentService = deploymentshandlers.NewDeploymentService(
			deps.ORMClient,
			gw.homeNodeManager,
			gw.portAllocator,
			gw.replicaManager,
			logger.Logger,
		)
		// Set base domain from config
		if gw.cfg.BaseDomain != "" {
			gw.deploymentService.SetBaseDomain(gw.cfg.BaseDomain)
		}
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
		gw.healthChecker = health.NewHealthChecker(dbAdapter, logger.Logger)
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
			olricCfg.Servers = []string{"localhost:3320"}
		}
		gw.startOlricReconnectLoop(olricCfg)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Gateway creation completed")
	return gw, nil
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

// GetORMClient returns the RQLite ORM client for external use (e.g., by ClusterManager)
func (g *Gateway) GetORMClient() rqlite.Client {
	return g.ormClient
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

