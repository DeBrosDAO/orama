package rqlite

// client.go provides the main ORM-like client that coordinates all components.
// It builds on the rqlite stdlib driver to behave like a regular SQL-backed ORM.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rqlite/gorqlite"
)

// NewClient wires the ORM client to a *sql.DB (from your RQLiteAdapter).
//
// The client constructed here can do everything EXCEPT atomic Batch — that
// requires the native gorqlite connection, which has no path through
// database/sql. Use NewClientWithDSN or NewClientWithConn if you need Batch.
func NewClient(db *sql.DB) Client {
	return &client{db: db}
}

// NewClientWithDSN wires the ORM client to BOTH a *sql.DB (for Query/Exec) and
// a native *gorqlite.Connection (for Batch atomicity).
//
// The DSN must be the standard rqlite connection URL ("http://user:pass@host:port"
// or "https://..."). Both connections share configuration but are independent
// HTTP clients.
//
// Returns an error if the gorqlite native dial fails. The *sql.DB is not
// validated here — callers should already have done that.
func NewClientWithDSN(db *sql.DB, dsn string) (Client, error) {
	conn, err := gorqlite.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("rqlite.NewClientWithDSN: native dial failed: %w", err)
	}
	return &client{db: db, conn: conn}, nil
}

// NewClientWithConn wires the ORM client when the caller already has a
// *gorqlite.Connection. Useful when reusing the connection from RQLiteManager.
func NewClientWithConn(db *sql.DB, conn *gorqlite.Connection) Client {
	return &client{db: db, conn: conn}
}

// NewClientFromAdapter is convenient if you already created the adapter.
// Note: Batch is unavailable on this client; use the DSN/Conn constructors
// when atomicity matters.
func NewClientFromAdapter(adapter *RQLiteAdapter) Client {
	return NewClient(adapter.GetSQLDB())
}

// client implements Client over *sql.DB plus an optional *gorqlite.Connection
// for the atomic Batch path. When conn is nil, Batch returns an error.
type client struct {
	db   *sql.DB
	conn *gorqlite.Connection
}

// Query runs an arbitrary SELECT and scans rows into dest.
// Query runs a SELECT and scans results into dest.
// Includes panic recovery because the gorqlite stdlib driver can panic
// with "index out of range" when RQLite is temporarily unavailable.
func (c *client) Query(ctx context.Context, dest any, query string, args ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (QueryContext): %v", r)
		}
	}()
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// Exec runs a write statement (INSERT/UPDATE/DELETE).
// Includes panic recovery because the gorqlite stdlib driver can panic
// with "index out of range" when RQLite is temporarily unavailable.
func (c *client) Exec(ctx context.Context, query string, args ...any) (result sql.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (ExecContext): %v", r)
		}
	}()
	return c.db.ExecContext(ctx, query, args...)
}

// FindBy finds entities matching criteria using simple map-based filtering.
func (c *client) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetMany(ctx, dest)
}

// FindOneBy finds a single entity matching criteria.
func (c *client) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...FindOption) error {
	qb := c.CreateQueryBuilder(table)
	for k, v := range criteria {
		qb = qb.AndWhere(fmt.Sprintf("%s = ?", k), v)
	}
	for _, opt := range opts {
		opt(qb)
	}
	return qb.GetOne(ctx, dest)
}

// Save inserts or updates an entity based on primary key value.
func (c *client) Save(ctx context.Context, entity any) error {
	return saveEntity(ctx, c.db, entity)
}

// Remove deletes an entity by primary key.
func (c *client) Remove(ctx context.Context, entity any) error {
	return removeEntity(ctx, c.db, entity)
}

// Repository returns a typed repository for a table.
// Note: Returns untyped interface - users must type assert to Repository[T].
func (c *client) Repository(table string) any {
	return func() any {
		return &repository[any]{c: c, table: table}
	}()
}

// CreateQueryBuilder creates a fluent query builder for advanced querying.
func (c *client) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(c.db, table)
}

// Tx executes a function within a transaction.
func (c *client) Tx(ctx context.Context, fn func(tx Tx) error) error {
	sqlTx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txc := &txClient{tx: sqlTx}
	if err := fn(txc); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}
