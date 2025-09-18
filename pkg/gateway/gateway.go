package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Config holds configuration for the gateway server
type Config struct {
	ListenAddr      string
	ClientNamespace string
	BootstrapPeers  []string
}

type Gateway struct {
	logger     *logging.ColoredLogger
	cfg        *Config
	client     client.NetworkClient
	startedAt  time.Time
	signingKey *rsa.PrivateKey
	keyID      string
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

	logger.ComponentInfo(logging.ComponentGeneral, "Starting database migrations goroutine...")
	// Non-blocking DB migrations: probe RQLite; if reachable, apply migrations asynchronously
	go func() {
		if gw.probeRQLiteReachable(3 * time.Second) {
			internalCtx := gw.withInternalAuth(context.Background())
			if err := gw.applyMigrations(internalCtx); err != nil {
				if err == errNoMigrationsFound {
					if err2 := gw.applyAutoMigrations(internalCtx); err2 != nil {
						logger.ComponentWarn(logging.ComponentDatabase, "auto migrations failed", zap.Error(err2))
					} else {
						logger.ComponentInfo(logging.ComponentDatabase, "auto migrations applied")
					}
				} else {
					logger.ComponentWarn(logging.ComponentDatabase, "migrations failed", zap.Error(err))
				}
			} else {
				logger.ComponentInfo(logging.ComponentDatabase, "migrations applied")
			}
		} else {
			logger.ComponentWarn(logging.ComponentDatabase, "RQLite not reachable; skipping migrations for now")
		}
	}()

	logger.ComponentInfo(logging.ComponentGeneral, "Gateway creation completed, returning...")
	return gw, nil
}

// withInternalAuth creates a context for internal gateway operations that bypass authentication
func (g *Gateway) withInternalAuth(ctx context.Context) context.Context {
	return client.WithInternalAuth(ctx)
}

// probeRQLiteReachable performs a quick GET /status against candidate endpoints with a short timeout.
func (g *Gateway) probeRQLiteReachable(timeout time.Duration) bool {
	endpoints := client.DefaultDatabaseEndpoints()
	httpClient := &http.Client{Timeout: timeout}
	for _, ep := range endpoints {
		url := ep
		if url == "" {
			continue
		}
		if url[len(url)-1] == '/' {
			url = url[:len(url)-1]
		}
		reqURL := url + "/status"
		resp, err := httpClient.Get(reqURL)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}
	return false
}

// Close disconnects the gateway client
func (g *Gateway) Close() {
	if g.client != nil {
		if err := g.client.Disconnect(); err != nil {
			g.logger.ComponentWarn(logging.ComponentClient, "error during client disconnect", zap.Error(err))
		}
	}
}
