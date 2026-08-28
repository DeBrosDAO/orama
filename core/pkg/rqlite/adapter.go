package rqlite

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/rqlite/gorqlite/stdlib" // Import the database/sql driver
)

// RQLiteAdapter adapts RQLite to the sql.DB interface
type RQLiteAdapter struct {
	manager *RQLiteManager
	db      *sql.DB

	// localDB is the lazily-created level=none read handle returned by
	// LocalDB(). Guarded by localMu. See localReadConsistencyLevel.
	localMu sync.Mutex
	localDB *sql.DB
}

// adapterReadConsistencyLevel is the rqlite consistency level used for
// gateway-internal SQL reads. Set to `weak` (matches gorqlite's own upstream
// default). MUST NOT be `none` — see bug #235: with `none`, reads serve from
// the local SQLite of whichever node the client is connected to, including
// followers that haven't replayed the most-recent Raft commits. Serverless
// functions running an `INSERT → UPDATE → SELECT` pattern in a single
// invocation saw the pre-write snapshot. `weak` routes reads to the leader,
// which always has the committed state, at a cost of ~1-2ms LAN hop over
// the WireGuard mesh.
const adapterReadConsistencyLevel = "weak"

// buildRQLiteDSN composes the DSN URL passed to gorqlite's stdlib driver.
// Pulled out for unit testing — the URL must encode `level=weak` (bug #235)
// in addition to `disableClusterDiscovery=true`.
func buildRQLiteDSN(host string, port int, username, password string) string {
	return buildRQLiteDSNWithLevel(host, port, username, password, adapterReadConsistencyLevel)
}

// buildRQLiteDSNWithLevel is buildRQLiteDSN with an explicit read level, so the
// bootstrap-only local handle (LocalDB) can ask for `none` while the main pool
// keeps `weak`.
func buildRQLiteDSNWithLevel(host string, port int, username, password, level string) string {
	if username != "" && password != "" {
		return fmt.Sprintf("http://%s:%s@%s:%d?disableClusterDiscovery=true&level=%s",
			username, password, host, port, level)
	}
	return fmt.Sprintf("http://%s:%d?disableClusterDiscovery=true&level=%s",
		host, port, level)
}

// NewRQLiteAdapter creates a new adapter that provides sql.DB interface for RQLite.
func NewRQLiteAdapter(manager *RQLiteManager) (*RQLiteAdapter, error) {
	dsn := buildRQLiteDSN("localhost", manager.config.RQLitePort,
		manager.config.RQLiteUsername, manager.config.RQLitePassword)
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open RQLite SQL connection: %w", err)
	}

	// Configure connection pool with proper timeouts and limits
	// Optimized for concurrent operations and fast bad connection eviction
	db.SetMaxOpenConns(100)                 // Allow more concurrent connections to prevent queuing
	db.SetMaxIdleConns(10)                  // Keep fewer idle connections to force fresh reconnects
	db.SetConnMaxLifetime(30 * time.Second) // Short lifetime ensures bad connections die quickly
	db.SetConnMaxIdleTime(10 * time.Second) // Kill idle connections quickly to prevent stale state

	return &RQLiteAdapter{
		manager: manager,
		db:      db,
	}, nil
}

// GetSQLDB returns the sql.DB interface for compatibility with existing storage service
func (a *RQLiteAdapter) GetSQLDB() *sql.DB {
	return a.db
}

// GetManager returns the underlying RQLite manager for advanced operations
func (a *RQLiteAdapter) GetManager() *RQLiteManager {
	return a.manager
}

// Close closes the adapter connections
func (a *RQLiteAdapter) Close() error {
	if a.db != nil {
		a.db.Close()
	}
	a.localMu.Lock()
	if a.localDB != nil {
		a.localDB.Close()
		a.localDB = nil
	}
	a.localMu.Unlock()
	return a.manager.Stop()
}

// localReadConsistencyLevel is the rqlite read level used by LocalDB(). Unlike
// adapterReadConsistencyLevel ("weak", leader-routed), `none` is answered from
// THIS node's local SQLite without contacting the raft leader.
//
// It exists for exactly one job: BOOTSTRAP reads that must succeed while the
// cluster has no leader. The canonical case is the WireGuard peer list — raft
// runs over the WireGuard mesh, so a node with no peers can never elect a
// leader, and a leader-routed read of `wireguard_peers` can never succeed. That
// is a deadlock: the data needed to repair connectivity is unreachable because
// connectivity is down. Reading the local replica breaks the cycle; the rows
// are already on disk.
//
// A `none` read may be stale (bug #235), so callers MUST treat it as a
// best-effort hint, never as authoritative cluster state — see
// (*Node).loadDesiredWireGuardPeers for the additive-only contract this
// enables.
const localReadConsistencyLevel = "none"

// LocalDB returns a lazily-created *sql.DB bound to this node's own rqlite at
// read level `none`. Reads never leave the box and therefore work with no raft
// leader; writes still route to the leader and fail without one, so this handle
// is for reads only.
//
// The handle is created on first use and cached. Callers must not Close it —
// Close() on the adapter owns its lifecycle.
func (a *RQLiteAdapter) LocalDB() (*sql.DB, error) {
	a.localMu.Lock()
	defer a.localMu.Unlock()
	if a.localDB != nil {
		return a.localDB, nil
	}
	if a.manager == nil || a.manager.config == nil {
		return nil, fmt.Errorf("rqlite adapter: manager config unavailable, cannot open local read connection")
	}
	dsn := buildRQLiteDSNWithLevel("localhost", a.manager.config.RQLitePort,
		a.manager.config.RQLiteUsername, a.manager.config.RQLitePassword,
		localReadConsistencyLevel)
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open local (level=none) RQLite connection: %w", err)
	}
	// Bootstrap-only path: a couple of connections is plenty and keeps the
	// footprint next to the main pool negligible.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)
	db.SetConnMaxIdleTime(10 * time.Second)
	a.localDB = db
	return a.localDB, nil
}
