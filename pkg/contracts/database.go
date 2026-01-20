package contracts

import (
	"context"
	"database/sql"
)

// DatabaseClient defines the interface for ORM-like database operations.
// Provides both raw SQL execution and fluent query building capabilities.
type DatabaseClient interface {
	// Query executes a SELECT query and scans results into dest.
	// dest must be a pointer to a slice of structs or []map[string]any.
	Query(ctx context.Context, dest any, query string, args ...any) error

	// Exec executes a write statement (INSERT/UPDATE/DELETE) and returns the result.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// FindBy retrieves multiple records matching the criteria.
	// dest must be a pointer to a slice, table is the table name,
	// criteria is a map of column->value filters, and opts customize the query.
	FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error

	// FindOneBy retrieves a single record matching the criteria.
	// dest must be a pointer to a struct or map.
	FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error

	// Save inserts or updates an entity based on its primary key.
	// If the primary key is zero, performs an INSERT.
	// If the primary key is set, performs an UPDATE.
	Save(ctx context.Context, entity any) error

	// Remove deletes an entity by its primary key.
	Remove(ctx context.Context, entity any) error

	// Repository returns a generic repository for a table.
	// Return type is any to avoid exposing generic type parameters in the interface.
	Repository(table string) any

	// CreateQueryBuilder creates a fluent query builder for advanced queries.
	// Supports joins, where clauses, ordering, grouping, and pagination.
	CreateQueryBuilder(table string) QueryBuilder

	// Tx executes a function within a database transaction.
	// If fn returns an error, the transaction is rolled back.
	// Otherwise, it is committed.
	Tx(ctx context.Context, fn func(tx DatabaseTransaction) error) error
}

// DatabaseTransaction provides database operations within a transaction context.
type DatabaseTransaction interface {
	// Query executes a SELECT query within the transaction.
	Query(ctx context.Context, dest any, query string, args ...any) error

	// Exec executes a write statement within the transaction.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// CreateQueryBuilder creates a query builder that executes within the transaction.
	CreateQueryBuilder(table string) QueryBuilder

	// Save inserts or updates an entity within the transaction.
	Save(ctx context.Context, entity any) error

	// Remove deletes an entity within the transaction.
	Remove(ctx context.Context, entity any) error
}

// QueryBuilder provides a fluent interface for building SQL queries.
type QueryBuilder interface {
	// Select specifies which columns to retrieve (default: *).
	Select(cols ...string) QueryBuilder

	// Alias sets a table alias for the query.
	Alias(alias string) QueryBuilder

	// Where adds a WHERE condition (same as AndWhere).
	Where(expr string, args ...any) QueryBuilder

	// AndWhere adds a WHERE condition with AND conjunction.
	AndWhere(expr string, args ...any) QueryBuilder

	// OrWhere adds a WHERE condition with OR conjunction.
	OrWhere(expr string, args ...any) QueryBuilder

	// InnerJoin adds an INNER JOIN clause.
	InnerJoin(table string, on string) QueryBuilder

	// LeftJoin adds a LEFT JOIN clause.
	LeftJoin(table string, on string) QueryBuilder

	// Join adds a JOIN clause (default join type).
	Join(table string, on string) QueryBuilder

	// GroupBy adds a GROUP BY clause.
	GroupBy(cols ...string) QueryBuilder

	// OrderBy adds an ORDER BY clause.
	// Supports expressions like "name ASC", "created_at DESC".
	OrderBy(exprs ...string) QueryBuilder

	// Limit sets the maximum number of rows to return.
	Limit(n int) QueryBuilder

	// Offset sets the number of rows to skip.
	Offset(n int) QueryBuilder

	// Build constructs the final SQL query and returns it with positional arguments.
	Build() (query string, args []any)

	// GetMany executes the query and scans results into dest (pointer to slice).
	GetMany(ctx context.Context, dest any) error

	// GetOne executes the query with LIMIT 1 and scans into dest (pointer to struct/map).
	GetOne(ctx context.Context, dest any) error
}

// FindOption is a function that configures a FindBy/FindOneBy query.
type FindOption func(q QueryBuilder)
