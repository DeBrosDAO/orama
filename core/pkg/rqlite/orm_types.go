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
	//
	// CAVEAT: against RQLite, the underlying database/sql Begin/Commit are
	// NOT real transactions (the gorqlite stdlib driver doesn't support them).
	// Use Batch for true atomicity.
	Tx(ctx context.Context, fn func(tx Tx) error) error

	// Batch executes ops as a single atomic transaction via the native
	// RQLite /db/execute?transaction endpoint. All-or-nothing: any failing
	// exec rolls the entire batch back. Query ops are sequenced after the
	// commit and see the just-committed state.
	//
	// Requires the client to have been constructed with a *gorqlite.Connection
	// (NewClientWithDSN or NewClientWithConn). Returns an error otherwise.
	Batch(ctx context.Context, ops []BatchOp) (*BatchResult, error)

	// BatchWithSeq executes the user's ops atomically AND, in the same atomic
	// batch, increments the per-namespace publish sequence counter, returning
	// the assigned sequence number. Used by exec_and_publish to attach a seq
	// to wake-up messages so subscribers can detect replication-lag gaps.
	BatchWithSeq(ctx context.Context, namespace string, userOps []BatchOp) (*BatchResult, int64, error)

	// BatchQuery runs N SELECT statements in ONE HTTP request to RQLite's
	// /db/query endpoint, returning one OpResult per input op in the same
	// order. All queries execute on the leader (level=weak — same as our
	// default reads) in a single network round-trip — N queries cost ~one
	// query's worth of latency instead of N times.
	//
	// Use this for read-heavy functions that need to gather state from
	// multiple tables before doing work. Empirically on devnet (167ms RTT to
	// leader): 10 sequential c.Query calls = 3562ms; 1 BatchQuery with 10
	// statements = 338ms. 10× speedup.
	//
	// Per-query errors are surfaced in OpResult.Error and do NOT fail the
	// whole batch — each query's result is independent. A transport-level
	// failure (network, leader unreachable) returns a non-nil Go error and
	// the OpResults may be empty.
	//
	// Requires the client to have been constructed with a *gorqlite.Connection
	// (NewClientWithDSN or NewClientWithConn). Returns an error otherwise.
	BatchQuery(ctx context.Context, ops []BatchOp) ([]OpResult, error)
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
