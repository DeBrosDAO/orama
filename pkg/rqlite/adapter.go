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
	db.SetMaxOpenConns(25)                 // Maximum number of open connections
	db.SetMaxIdleConns(5)                  // Maximum number of idle connections
	db.SetConnMaxLifetime(5 * time.Minute) // Maximum lifetime of a connection
	db.SetConnMaxIdleTime(2 * time.Minute) // Maximum idle time before closing

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
