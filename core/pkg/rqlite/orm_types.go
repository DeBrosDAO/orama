package rqlite

// orm_types.go defines common types, interfaces, and structures used throughout the rqlite ORM package.

import (
	"context"
	"database/sql"
	"strings"
)

// TableNamer lets a struct provide its table name.
type TableNamer interface {
	TableName() string
}

// Client is the high-level ORM-like API.
type Client interface {
	// Query runs an arbitrary SELECT and scans rows into dest (pointer to slice of structs or []map[string]any).
	Query(ctx context.Context, dest any, query string, args ...any) error
	// Exec runs a write statement (INSERT/UPDATE/DELETE).
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// FindBy/FindOneBy provide simple map-based criteria filtering.
	FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error
	FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error

	// Save inserts or updates an entity (single-PK).
	Save(ctx context.Context, entity any) error
	// Remove deletes by PK (single-PK).
	Remove(ctx context.Context, entity any) error

	// Repositories (generic layer). Optional but convenient if you use Go generics.
	Repository(table string) any

	// Fluent query builder for advanced querying.
	CreateQueryBuilder(table string) *QueryBuilder

	// Tx executes a function within a transaction.
	Tx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx mirrors Client but executes within a transaction.
type Tx interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	CreateQueryBuilder(table string) *QueryBuilder

	// Optional: scoped Save/Remove inside tx
	Save(ctx context.Context, entity any) error
	Remove(ctx context.Context, entity any) error
}

// Repository provides typed entity operations for a table.
type Repository[T any] interface {
	Find(ctx context.Context, dest *[]T, criteria map[string]any, opts ...FindOption) error
	FindOne(ctx context.Context, dest *T, criteria map[string]any, opts ...FindOption) error
	Save(ctx context.Context, entity *T) error
	Remove(ctx context.Context, entity *T) error

	// Builder helpers
	Q() *QueryBuilder
}

// executor is implemented by *sql.DB and *sql.Tx.
type executor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// FindOption customizes Find queries.
type FindOption func(q *QueryBuilder)

// WithOrderBy adds ORDER BY clause to query.
func WithOrderBy(exprs ...string) FindOption {
	return func(q *QueryBuilder) { q.OrderBy(exprs...) }
}

// WithGroupBy adds GROUP BY clause to query.
func WithGroupBy(cols ...string) FindOption {
	return func(q *QueryBuilder) { q.GroupBy(cols...) }
}

// WithLimit adds LIMIT clause to query.
func WithLimit(n int) FindOption {
	return func(q *QueryBuilder) { q.Limit(n) }
}

// WithOffset adds OFFSET clause to query.
func WithOffset(n int) FindOption {
	return func(q *QueryBuilder) { q.Offset(n) }
}

// WithSelect specifies columns to select.
func WithSelect(cols ...string) FindOption {
	return func(q *QueryBuilder) { q.Select(cols...) }
}

// WithJoin adds a JOIN clause to query.
func WithJoin(kind, table, on string) FindOption {
	return func(q *QueryBuilder) {
		switch strings.ToUpper(kind) {
		case "INNER":
			q.InnerJoin(table, on)
		case "LEFT":
			q.LeftJoin(table, on)
		default:
			q.Join(table, on)
		}
	}
}

// fieldMeta holds metadata about struct fields for ORM operations.
type fieldMeta struct {
	index  int
	column string
	isPK   bool
	auto   bool
}
