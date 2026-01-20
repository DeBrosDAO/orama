package rqlite

// client.go provides the main ORM-like client that coordinates all components.
// It builds on the rqlite stdlib driver to behave like a regular SQL-backed ORM.

import (
	"context"
	"database/sql"
	"fmt"
)

// NewClient wires the ORM client to a *sql.DB (from your RQLiteAdapter).
func NewClient(db *sql.DB) Client {
	return &client{db: db}
}

// NewClientFromAdapter is convenient if you already created the adapter.
func NewClientFromAdapter(adapter *RQLiteAdapter) Client {
	return NewClient(adapter.GetSQLDB())
}

// client implements Client over *sql.DB.
type client struct {
	db *sql.DB
}

// Query runs an arbitrary SELECT and scans rows into dest.
func (c *client) Query(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// Exec runs a write statement (INSERT/UPDATE/DELETE).
func (c *client) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
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
