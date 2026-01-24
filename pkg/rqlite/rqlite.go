package rqlite

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"
)

// RQLiteManager manages an RQLite node instance
type RQLiteManager struct {
	config           *config.DatabaseConfig
	discoverConfig   *config.DiscoveryConfig
	dataDir          string
	nodeType         string // Node type identifier
	logger           *zap.Logger
	cmd              *exec.Cmd
	connection       *gorqlite.Connection
	discoveryService *ClusterDiscoveryService
}

// NewRQLiteManager creates a new RQLite manager
func NewRQLiteManager(cfg *config.DatabaseConfig, discoveryCfg *config.DiscoveryConfig, dataDir string, logger *zap.Logger) *RQLiteManager {
	return &RQLiteManager{
		config:         cfg,
		discoverConfig: discoveryCfg,
		dataDir:        dataDir,
		logger:         logger.With(zap.String("component", "rqlite-manager")),
	}
}

// Start starts the RQLite node
func (r *RQLiteManager) Start(ctx context.Context) error {
	rqliteDataDir, err := r.prepareDataDir()
	if err != nil {
		return err
	}

	if r.discoverConfig.HttpAdvAddress == "" {
		return fmt.Errorf("discovery config HttpAdvAddress is empty")
	}

	if r.discoveryService != nil {
		if err := r.waitForMinClusterSizeBeforeStart(ctx, rqliteDataDir); err != nil {
			return err
		}
	}

	if needsClusterRecovery, err := r.checkNeedsClusterRecovery(rqliteDataDir); err == nil && needsClusterRecovery {
		if err := r.performPreStartClusterDiscovery(ctx, rqliteDataDir); err != nil {
			return err
		}
	}

	if err := r.launchProcess(ctx, rqliteDataDir); err != nil {
		return err
	}

	if err := r.waitForReadyAndConnect(ctx); err != nil {
		return err
	}

	if r.discoveryService != nil {
		go r.startHealthMonitoring(ctx)
	}

	if err := r.establishLeadershipOrJoin(ctx, rqliteDataDir); err != nil {
		return err
	}

	// Apply embedded migrations - these are compiled into the binary
	if err := r.ApplyEmbeddedMigrations(ctx, migrations.FS); err != nil {
		r.logger.Error("Failed to apply embedded migrations", zap.Error(err))
		// Don't fail startup - migrations may have already been applied by another node
		// or we may be joining an existing cluster
	} else {
		r.logger.Info("Database migrations applied successfully")
	}

	return nil
}

// GetConnection returns the RQLite connection
func (r *RQLiteManager) GetConnection() *gorqlite.Connection {
	return r.connection
}

// Stop stops the RQLite node
func (r *RQLiteManager) Stop() error {
	if r.connection != nil {
		r.connection.Close()
		r.connection = nil
	}

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}

	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = r.cmd.Process.Kill()
	}

	return nil
}
