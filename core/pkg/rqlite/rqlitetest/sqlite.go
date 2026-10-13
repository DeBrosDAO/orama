package rqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3" // in-memory SQLite driver
)

// SQLite returns an rqlite.Client over a fresh in-memory SQLite database, with
// each ddl statement applied, plus the database itself for assertions.
//
// rqlite.NewClient over *sql.DB has no Batch (Batch needs rqlite's native
// connection), so the returned client answers Batch itself: the exec ops run
// in one SQLite transaction and roll back together, which is the atomicity
// rqlite's /db/execute?transaction gives. Query ops are refused. For what only
// a real node shows — rqlite's own errors and types — use Start.
func SQLite(t *testing.T, ddl ...string) (rqlite.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("rqlitetest: open sqlite: %v", err)
	}
	// One connection: every handle must see the same in-memory database.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("rqlitetest: apply ddl: %v", err)
		}
	}
	return sqliteBatchClient{Client: rqlite.NewClient(db), db: db}, db
}

type sqliteBatchClient struct {
	rqlite.Client
	db *sql.DB
}

// Batch runs the exec ops in one transaction. A failing op rolls back every op
// and is reported the way rqlite reports it: an uncommitted result naming the
// failing op, and an error.
func (c sqliteBatchClient) Batch(ctx context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("rqlitetest: begin: %w", err)
	}
	res := &rqlite.BatchResult{Results: make([]rqlite.OpResult, len(ops))}
	for i, op := range ops {
		if op.Kind != rqlite.BatchOpExec {
			_ = tx.Rollback()
			return nil, fmt.Errorf("rqlitetest: op %d: SQLite Batch runs exec ops only, got %q", i, op.Kind)
		}
		r, err := tx.ExecContext(ctx, op.SQL, op.Args...)
		if err != nil {
			_ = tx.Rollback()
			res.FailedIndex = i
			res.Results[i] = rqlite.OpResult{Kind: op.Kind, Error: err.Error()}
			return res, fmt.Errorf("rqlitetest: exec failed at op %d: %w", i, err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("rqlitetest: op %d rows affected: %w", i, err)
		}
		res.Results[i] = rqlite.OpResult{Kind: op.Kind, RowsAffected: n}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("rqlitetest: commit: %w", err)
	}
	res.Committed = true
	return res, nil
}
