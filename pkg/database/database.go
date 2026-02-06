// Package database provides a generic database interface for the deployment system.
// This allows different database implementations (RQLite, SQLite, etc.) to be used
// interchangeably throughout the deployment handlers.
package database

import "context"

// Database is a generic interface for database operations
// It provides methods for executing queries and commands that can be implemented
// by various database clients (RQLite, SQLite, etc.)
type Database interface {
	// Query executes a SELECT query and scans results into dest
	// dest should be a pointer to a slice of structs with `db` tags
	Query(ctx context.Context, dest interface{}, query string, args ...interface{}) error

	// QueryOne executes a SELECT query and scans a single result into dest
	// dest should be a pointer to a struct with `db` tags
	// Returns an error if no rows are found or multiple rows are returned
	QueryOne(ctx context.Context, dest interface{}, query string, args ...interface{}) error

	// Exec executes an INSERT, UPDATE, or DELETE query
	// Returns the result (typically last insert ID or rows affected)
	Exec(ctx context.Context, query string, args ...interface{}) (interface{}, error)
}
