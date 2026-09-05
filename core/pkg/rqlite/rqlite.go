package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"
)

// RQLiteManager manages an RQLite node instance
type RQLiteManager struct {
	config         *config.DatabaseConfig
	discoverConfig *config.DiscoveryConfig

	// Off-box backup destination. Without an uploader, snapshots stay on this
	// node's disk — which is what they did before, and is now reported.
	backupUploader    BackupUploader
	backupReplication int
	backupKey         []byte

	// peerID is this node's libp2p peer id, the stable half of its raft
	// identity. Empty until SetPeerID is called, in which case rqlite keeps
	// defaulting the raft id to the advertise address.
	peerID           string
	dataDir          string
	nodeType         string // Node type identifier
	logger           *zap.Logger
	discoveryService *ClusterDiscoveryService

	// connMu guards connection. The split-brain recovery path closes and
	// reopens it from its own goroutine while the boot supervisor's leader
	// probe is reading it, so the handle needs a lock rather than the bare
	// field it used to be.
	connMu     sync.RWMutex
	connection *gorqlite.Connection

	// onProcessStarted, when set, runs after the local rqlited process is
	// listening and before anything waits for a raft leader. See the call site
	// in StartLocal for why that window is the only place transport-layer
	// prerequisites for raft can be repaired.
	//
	// It must be fast and best-effort: it runs on the startup path, and any
	// error it hits is its own to log. Set via SetOnProcessStarted.
	onProcessStarted func(context.Context)

	// backgroundOnce guards the long-lived reconcilers started by JoinCluster,
	// which the boot supervisor may call more than once.
	backgroundOnce sync.Once

	// sqlDB is a lazily-opened database/sql handle on this node's own rqlite,
	// for the reconcilers that need SQL rather than raft's HTTP API.
	sqlOnce sync.Once
	sqlDB   *sql.DB
	sqlErr  error

	// stopOnce guards Stop. It has two callers — RQLiteAdapter.Close and
	// Node.Stop — and running the leadership transfer twice can push shutdown
	// past the unit's TimeoutStopSec, so systemd SIGKILLs the process during
	// the second transfer.
	stopOnce sync.Once
	stopErr  error
}

const (
	// supervisedRaftReadyTimeout and supervisedSQLTimeout bound ONE attempt at
	// joining the cluster. They are short on purpose: the boot supervisor
	// retries, so a long single attempt buys nothing and only delays the node
	// announcing that it is degraded.
	supervisedRaftReadyTimeout = 30 * time.Second
	supervisedSQLTimeout       = 30 * time.Second

	// leaderProbeTimeout bounds the periodic "can I still reach a leader"
	// check. gorqlite's QueryOne takes no context, so the probe runs in its own
	// goroutine and this is what gives up on it. The abandoned goroutine still
	// exits, because gorqlite's own HTTP client has a 10s default and the
	// connection URL sets no `timeout` parameter that would override it — do
	// not add one without bounding the probe some other way.
	leaderProbeTimeout = 10 * time.Second

	// localStatusTimeout bounds the periodic "is my own rqlited alive" check.
	// It only talks to localhost, so it can be short.
	localStatusTimeout = 3 * time.Second
)

// SetOnProcessStarted registers a callback invoked once the local rqlited is
// listening and before anything waits for a leader. Call before StartLocal.
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

// StartLocal brings up everything about this node's rqlite that does not need
// a raft quorum: the data directory, the local connection, and any transport
// repair the caller registered.
//
// It is deliberately separate from JoinCluster. Consensus is the one part of
// start-up a single node cannot make progress on alone, so folding it into the
// same call made every service ordered after rqlite hostage to other people's
// machines being up. Split, a node that boots alone still opens its store,
// still serves its local replica, and simply keeps retrying the cluster half.
func (r *RQLiteManager) StartLocal(ctx context.Context) error {
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
	// prerequisites for raft, so give the caller a chance to act before anything
	// waits on a leader.
	//
	// It matters because raft runs over the WireGuard mesh while mesh membership
	// is stored in raft. A node whose interface has lost its peers cannot reach
	// any voter, so the cluster half below can never succeed, and the repair
	// step that would fix it used to be ordered after it — unreachable. One
	// restart in that state was an unrecoverable outage. Hooking here inverts
	// the dependency: repair transport, then wait for consensus.
	if r.onProcessStarted != nil {
		r.onProcessStarted(ctx)
	}

	return r.connect(ctx)
}

// JoinCluster completes the half of start-up that needs the rest of the
// cluster: raft participation, a leader-routed read, the background
// reconcilers, and the embedded migrations.
//
// It returns an error whenever the node is not yet part of a working quorum.
// That error is a signal to retry, not a reason to exit: the caller (the boot
// supervisor) keeps calling until it succeeds, and the node serves from its
// local replica in the meantime.
func (r *RQLiteManager) JoinCluster(ctx context.Context) error {
	if r.GetConnection() == nil {
		return fmt.Errorf("rqlite: JoinCluster called before StartLocal opened a connection")
	}

	if err := WaitForRaftReady(ctx, r.config.RQLitePort, supervisedRaftReadyTimeout); err != nil {
		return err
	}

	sqlCtx, cancel := context.WithTimeout(ctx, supervisedSQLTimeout)
	defer cancel()
	if err := r.waitForSQLAvailable(sqlCtx); err != nil {
		return fmt.Errorf("rqlite has a raft state but cannot serve a leader-routed read: %w", err)
	}

	// The background reconcilers must outlive any single attempt, so they are
	// started once, from the supervisor's own context.
	r.backgroundOnce.Do(func() {
		if r.discoveryService != nil {
			go r.startHealthMonitoring(ctx)
			go r.startVoterReconciliation(ctx)
			go r.startOrphanedNodeRecovery(ctx)
		}
		go r.startBackupLoop(ctx)
	})

	// Apply embedded migrations - these are compiled into the binary.
	// We tolerate apply errors here (joining an existing cluster, transient
	// leader election, etc.) — the gateway-process-side check is the
	// authoritative gate for "is schema usable". See gateway.dependencies.
	if err := r.ApplyEmbeddedMigrations(ctx, migrations.FS); err != nil {
		r.logger.Error("Failed to apply embedded migrations", zap.Error(err))
	} else {
		r.logger.Info("Database migrations applied successfully")
	}

	// This node is participating in raft again, so any tombstone against it is
	// spent, and leaving the row would keep it out of orphan recovery until the
	// TTL expires. Runs AFTER the migrations above, which is what creates the
	// table on the first boot that carries this code.
	//
	// It only ever fires for a node that was tombstoned but stayed in the
	// configuration — a tombstone written just before a removal that failed. A
	// node that was actually removed cannot reach here at all: it has no
	// leader, so JoinCluster returns above. That case is what tombstoneTTL is
	// for.
	if db, dbErr := r.localSQLHandle(); dbErr == nil {
		if err := clearTombstone(ctx, db, r.discoverConfig.RaftAdvAddress); err != nil {
			r.logger.Warn("Could not clear this node's raft eviction tombstone",
				zap.String("node_id", r.discoverConfig.RaftAdvAddress), zap.Error(err))
		}
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

// LocalHealthy reports whether the local rqlited is still running and this
// manager still holds an open handle to it.
//
// It deliberately does NOT require a leader: a node that has lost quorum is
// still locally healthy, and treating it otherwise would restart a perfectly
// good rqlited in the middle of someone else's outage. Failing this check means
// the process is gone or the handle was closed — both of which need the local
// half of start-up to run again.
func (r *RQLiteManager) LocalHealthy(ctx context.Context) error {
	if r.GetConnection() == nil {
		return fmt.Errorf("rqlite: local connection is closed")
	}

	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("rqlite: build local status request: %w", err)
	}
	resp, err := tlsutil.NewHTTPClient(localStatusTimeout).Do(req)
	if err != nil {
		return fmt.Errorf("rqlite: local rqlited on port %d is not answering — check orama-namespace-rqlite@index: %w", r.config.RQLitePort, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rqlite: local rqlited on port %d returned HTTP %d", r.config.RQLitePort, resp.StatusCode)
	}
	return nil
}

// LeaderReachable reports whether this node can still serve a leader-routed
// read. It is the health signal for the cluster half of start-up: when it
// fails, the node has lost quorum and should go back to degraded rather than
// keep claiming it is fully converged.
//
// The probe is a bare SELECT 1, which works because r.connection is opened with
// no `level` parameter and gorqlite's default consistency is `weak` — reads are
// routed to the leader, so a leaderless node gets an error rather than a stale
// answer off its own replica. See adapterReadConsistencyLevel.
func (r *RQLiteManager) LeaderReachable(ctx context.Context) error {
	conn := r.GetConnection()
	if conn == nil {
		return fmt.Errorf("rqlite: no local connection")
	}
	probeCtx, cancel := context.WithTimeout(ctx, leaderProbeTimeout)
	defer cancel()

	// gorqlite's QueryOne takes no context, so the probe runs in its own
	// goroutine and the select below is what gives up on it. The channel is
	// buffered so an abandoned probe still exits when the query returns.
	done := make(chan error, 1)
	go func() {
		_, err := conn.QueryOne(readinessProbe)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-probeCtx.Done():
		return fmt.Errorf("rqlite: leader probe did not return within %s: %w", leaderProbeTimeout, probeCtx.Err())
	}
}

// GetConnection returns the RQLite connection, or nil if it is not open.
func (r *RQLiteManager) GetConnection() *gorqlite.Connection {
	r.connMu.RLock()
	defer r.connMu.RUnlock()
	return r.connection
}

// setConnection replaces the local handle, closing whatever it replaces.
func (r *RQLiteManager) setConnection(conn *gorqlite.Connection) {
	r.connMu.Lock()
	old := r.connection
	r.connection = conn
	r.connMu.Unlock()

	if old != nil && old != conn {
		old.Close()
	}
}

// Stop stops the RQLite node gracefully.
// If this node is the Raft leader, it attempts a leadership transfer first
// to minimize cluster disruption.
func (r *RQLiteManager) Stop() error {
	r.stopOnce.Do(func() { r.stopErr = r.shutdown() })
	return r.stopErr
}

// shutdown closes the connection, hands leadership over and reaps the process.
// Stop runs it exactly once; the split-brain recovery path calls it directly,
// because a recovery genuinely has to stop the instance every time it runs and
// is not a terminal shutdown.
func (r *RQLiteManager) shutdown() error {
	r.setConnection(nil)

	// Hand leadership over. rqlited runs as orama-namespace-rqlite@index, never
	// as a child of this process, so stopping it is systemd's job — all this
	// has to do is make sure the node is not the leader when it goes.
	//
	// There used to be a child-process guard above this, and because r.cmd is
	// always nil in production it returned before the transfer could ever run:
	// every restart of the leader was a hard kill and a full election with
	// in-flight writes failing. The transfer needs only the HTTP port.
	r.transferLeadershipIfLeader()

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

// SetPeerID supplies this node's libp2p peer id, which becomes its raft node id
// on a fresh node and after migration.
//
// It is set rather than passed to the constructor because the manager is built
// before libp2p starts; the boot graph orders rqlite-local after libp2p, so the
// id is available by the time the process is spawned.
func (r *RQLiteManager) SetPeerID(peerID string) {
	r.peerID = peerID
}

// PeerID returns the libp2p peer id this manager was given, if any.
func (r *RQLiteManager) PeerID() string {
	return r.peerID
}
