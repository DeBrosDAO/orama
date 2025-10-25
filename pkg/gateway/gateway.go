package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
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

	// Production domain for HTTPS/ACME
	Domain string
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
		logger:    logger,
		cfg:       cfg,
		client:    c,
		startedAt: time.Now(),
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
		gw.sqlDB = db
		orm := rqlite.NewClient(db)
		gw.ormClient = orm
		gw.ormHTTP = rqlite.NewHTTPGateway(orm, "/v1/db")
		logger.ComponentInfo(logging.ComponentGeneral, "RQLite ORM HTTP gateway ready",
			zap.String("dsn", dsn),
			zap.String("base_path", "/v1/db"),
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
}
