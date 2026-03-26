package rqlite

// transaction.go implements transaction support for the rqlite ORM.

import (
	"context"
	"database/sql"
)

// txClient implements Tx over *sql.Tx.
type txClient struct {
	tx *sql.Tx
}

// Query executes a SELECT query within the transaction.
func (t *txClient) Query(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scanIntoDest(rows, dest)
}

// Exec executes a write statement within the transaction.
func (t *txClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

// CreateQueryBuilder creates a QueryBuilder that uses this transaction.
func (t *txClient) CreateQueryBuilder(table string) *QueryBuilder {
	return newQueryBuilder(t.tx, table)
}

// Save inserts or updates an entity within the transaction.
func (t *txClient) Save(ctx context.Context, entity any) error {
	return saveEntity(ctx, t.tx, entity)
}

// Remove deletes an entity within the transaction.
func (t *txClient) Remove(ctx context.Context, entity any) error {
	return removeEntity(ctx, t.tx, entity)
}
