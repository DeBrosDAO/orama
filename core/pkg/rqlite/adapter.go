package rqlite

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/rqlite/gorqlite/stdlib" // Import the database/sql driver
)

// RQLiteAdapter adapts RQLite to the sql.DB interface
type RQLiteAdapter struct {
	manager *RQLiteManager
	db      *sql.DB
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
	if username != "" && password != "" {
		return fmt.Sprintf("http://%s:%s@%s:%d?disableClusterDiscovery=true&level=%s",
			username, password, host, port, adapterReadConsistencyLevel)
	}
	return fmt.Sprintf("http://%s:%d?disableClusterDiscovery=true&level=%s",
		host, port, adapterReadConsistencyLevel)
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
	return a.manager.Stop()
}
