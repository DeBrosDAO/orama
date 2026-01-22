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

// NewRQLiteAdapter creates a new adapter that provides sql.DB interface for RQLite
func NewRQLiteAdapter(manager *RQLiteManager) (*RQLiteAdapter, error) {
	// Use the gorqlite database/sql driver
	db, err := sql.Open("rqlite", fmt.Sprintf("http://localhost:%d", manager.config.RQLitePort))
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
