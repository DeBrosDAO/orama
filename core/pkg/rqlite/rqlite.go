package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
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
	waitDone         chan struct{} // closed when cmd.Wait() completes (reaps zombie)
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
		go r.startVoterReconciliation(ctx)
		go r.startOrphanedNodeRecovery(ctx) // C1 fix: recover nodes orphaned by failed voter changes
	}

	// Start child process watchdog to detect and recover from crashes
	go r.startProcessWatchdog(ctx)

	// Start periodic RQLite backup loop (leader-only, self-checking)
	go r.startBackupLoop(ctx)

	if err := r.establishLeadershipOrJoin(ctx, rqliteDataDir); err != nil {
		return err
	}

	// Apply embedded migrations - these are compiled into the binary.
	// We tolerate apply errors here (joining an existing cluster, transient
	// leader election, etc.) — the gateway-process-side check is the
	// authoritative gate for "is schema usable". See gateway.dependencies.
	if err := r.ApplyEmbeddedMigrations(ctx, migrations.FS); err != nil {
		r.logger.Error("Failed to apply embedded migrations", zap.Error(err))
	} else {
		r.logger.Info("Database migrations applied successfully")
	}

	// Schema-drift visibility: even when apply returned nil, log if the
	// schema isn't at the binary's required version. Helps operators spot
	// lag in the rolling-upgrade window before the gateway flips fatal.
	if db, err := sql.Open("rqlite", fmt.Sprintf("http://localhost:%d?disableClusterDiscovery=true", r.config.RQLitePort)); err == nil {
		defer db.Close()
		if assertErr := migrations.AssertSchema(ctx, db); assertErr != nil {
			r.logger.Warn("Schema below required version after apply",
				zap.Int("required", migrations.RequiredVersion()),
				zap.Error(assertErr))
		}
	}

	return nil
}

// GetConnection returns the RQLite connection
func (r *RQLiteManager) GetConnection() *gorqlite.Connection {
	return r.connection
}

// Stop stops the RQLite node gracefully.
// If this node is the Raft leader, it attempts a leadership transfer first
// to minimize cluster disruption.
func (r *RQLiteManager) Stop() error {
	if r.connection != nil {
		r.connection.Close()
		r.connection = nil
	}

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}

	// Attempt leadership transfer if we are the leader
	r.transferLeadershipIfLeader()

	_ = r.cmd.Process.Signal(syscall.SIGTERM)

	// Wait for the background reaper goroutine (started in launchProcess) to
	// collect the child process. This avoids a double cmd.Wait() panic.
	if r.waitDone != nil {
		select {
		case <-r.waitDone:
		case <-time.After(30 * time.Second):
			r.logger.Warn("RQLite did not stop within 30s, sending SIGKILL")
			_ = r.cmd.Process.Kill()
			<-r.waitDone // wait for reaper after kill
		}
	}

	// Clean up PID file
	r.cleanupPIDFile()

	return nil
}

// transferLeadershipIfLeader checks if this node is the Raft leader and
// requests a leadership transfer to minimize election disruption.
func (r *RQLiteManager) transferLeadershipIfLeader() {
	if err := TransferLeadership(r.config.RQLitePort, r.logger); err != nil {
		r.logger.Warn("Leadership transfer failed, relying on SIGTERM", zap.Error(err))
	}
}

// cleanupPIDFile removes the PID file on shutdown
func (r *RQLiteManager) cleanupPIDFile() {
	logsDir := fmt.Sprintf("%s/../logs", r.dataDir)
	pidPath := logsDir + "/rqlited.pid"
	_ = os.Remove(pidPath)
}
