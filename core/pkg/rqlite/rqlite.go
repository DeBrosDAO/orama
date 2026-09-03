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

	// onProcessStarted, when set, runs after the local rqlited process is
	// listening but BEFORE Start blocks waiting for a raft leader. See the call
	// site in Start for why that window is the only place transport-layer
	// prerequisites for raft can be repaired.
	//
	// It must be fast and best-effort: it runs on the startup path, and any
	// error it hits is its own to log. Set via SetOnProcessStarted.
	onProcessStarted func(context.Context)
}

// SetOnProcessStarted registers a callback invoked once the local rqlited is
// listening and before Start waits for a leader. Call before Start.
func (r *RQLiteManager) SetOnProcessStarted(fn func(context.Context)) {
	r.onProcessStarted = fn
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

	// rqlited is orama-namespace-rqlite@index, never a child of this process.
	r.logger.Info("Connecting to systemd-managed RQLite (orama-namespace-rqlite@index)")

	// The local rqlited is now listening but has not necessarily joined a
	// quorum. This is the ONLY window in which a node can repair transport-layer
	// prerequisites for raft, so give the caller a chance to act before we block
	// on a leader.
	//
	// It matters because raft runs over the WireGuard mesh while mesh membership
	// is stored in raft. A node whose interface has lost its peers cannot reach
	// any voter, so waitForReadyAndConnect below can never succeed, and the
	// repair step that would fix it is ordered after this call — unreachable.
	// One restart in that state is an unrecoverable outage. Hooking here inverts
	// the dependency: repair transport, then wait for consensus.
	if r.onProcessStarted != nil {
		r.onProcessStarted(ctx)
	}

	if err := r.waitForReadyAndConnect(ctx); err != nil {
		return err
	}

	if r.discoveryService != nil {
		go r.startHealthMonitoring(ctx)
		go r.startVoterReconciliation(ctx)
		go r.startOrphanedNodeRecovery(ctx) // C1 fix: recover nodes orphaned by failed voter changes
	}

	// Process watchdog is systemd Restart= on @index, not a child reaper.

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

	// Hand leadership over BEFORE the child-process guard below. rqlited runs as
	// orama-namespace-rqlite@index, never as a child of this process, so r.cmd
	// is always nil in production and the guard returned before the transfer
	// could ever run: every restart of the leader was a hard kill and a full
	// election with in-flight writes failing. The transfer needs only the HTTP
	// port, not a process handle.
	r.transferLeadershipIfLeader()

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}

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
