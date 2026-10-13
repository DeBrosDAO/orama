package rqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rqlite/gorqlite"
	"github.com/rqlite/gorqlite/stdlib"
)

// migrationsTracker is the version-bookkeeping table of the cluster (main)
// schema. A namespace RQLite uses namespaceMigrationsTracker instead.
const migrationsTracker = "schema_migrations"

// recordMigrationStmt is the statement that marks a migration applied in the
// given tracker. The tracker name is always one of the package constants.
func recordMigrationStmt(tracker string, version int) gorqlite.ParameterizedStatement {
	return gorqlite.ParameterizedStatement{
		Query:     fmt.Sprintf(`INSERT OR IGNORE INTO %s(version) VALUES (?)`, tracker),
		Arguments: []interface{}{version},
	}
}

// applyStatementsAtomically runs stmts, then the optional tracker record, as ONE
// transaction: every statement and the record commit together or none does.
//
// Why: a migration used to be N requests plus one more for its tracker row. Any
// failure between two of them (a lost raft leader is enough) left the migration
// half-applied, or applied but unrecorded, and the retry re-ran non-idempotent
// statements against a schema they had already changed — "no such table" on a
// table a later migration had dropped. One transaction has no in-between state.
//
// "Already applied" tolerance (isAlreadyAppliedError) survives, but a
// transaction aborts on the first error, so the offending statement is dropped
// and the whole transaction is re-sent without it. Each pass removes one
// statement, so the loop is bounded; the aborted pass changed nothing. This only
// ever triggers on a database a pre-atomic engine left half-migrated, or one
// whose tables were created by hand. Pre-filtering instead would need a schema
// introspection per statement kind (ALTER ADD COLUMN has no IF NOT EXISTS), and
// rewriting DDL to IF NOT EXISTS would change what the shipped files mean.
func applyStatementsAtomically(ctx context.Context, db *sql.DB, stmts []string, record *gorqlite.ParameterizedStatement) error {
	batch := make([]gorqlite.ParameterizedStatement, 0, len(stmts)+1)
	for _, s := range stmts {
		batch = append(batch, gorqlite.ParameterizedStatement{Query: s})
	}
	recordIdx := len(batch)
	if record != nil {
		batch = append(batch, *record)
	}

	for len(batch) > 0 {
		failed, err := execTransaction(ctx, db, batch)
		if err == nil {
			return nil
		}
		if failed < 0 {
			return fmt.Errorf("transaction of %d statements was not applied: %w", len(batch), err)
		}
		if failed < recordIdx && isAlreadyAppliedError(err) {
			batch = append(batch[:failed], batch[failed+1:]...)
			recordIdx--
			continue
		}
		return fmt.Errorf("exec stmt failed: %w (stmt: %s)", err, snippet(batch[failed].Query))
	}
	return nil
}

// execTransaction sends batch as one transaction. failed is the index of the
// statement the database rejected, or -1 when none was (transport fault, lost
// leader, expired deadline): nothing in the batch is blamed and, for a request
// that never reached rqlite, nothing was applied.
//
// Against rqlite the batch is one /db/execute?transaction request through the
// native connection; database/sql's Tx is a no-op in the gorqlite driver, so
// BeginTx would not be atomic there. Any other driver (the in-memory SQLite
// used by tests) gets a real sql transaction, which has the same semantics.
func execTransaction(ctx context.Context, db *sql.DB, batch []gorqlite.ParameterizedStatement) (failed int, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return -1, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Close()

	native := false
	rawErr := conn.Raw(func(driverConn any) error {
		sc, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return nil
		}
		native = true
		defer func() {
			if r := recover(); r != nil {
				failed, err = -1, fmt.Errorf("gorqlite panic (WriteParameterized): %v", r)
			}
		}()
		failed, err = writeNativeTransaction(ctx, sc.Connection, batch)
		return nil
	})
	if rawErr != nil {
		return -1, fmt.Errorf("access driver connection: %w", rawErr)
	}
	if native {
		return failed, err
	}
	return execSQLTransaction(ctx, conn, batch)
}

func writeNativeTransaction(ctx context.Context, c *gorqlite.Connection, batch []gorqlite.ParameterizedStatement) (int, error) {
	if err := c.SetExecutionWithTransaction(true); err != nil {
		return -1, fmt.Errorf("enable transactional execution: %w", err)
	}
	wrs, err := c.WriteParameterizedContext(ctx, batch)
	if err == nil {
		return -1, nil
	}
	// A transport failure is not StatementErrors; gorqlite reports it on one
	// first result, which must not be read as "statement 0 failed".
	var stmtErrs gorqlite.StatementErrors
	if errors.As(err, &stmtErrs) {
		for i, wr := range wrs {
			if wr.Err != nil {
				return i, wr.Err
			}
		}
	}
	return -1, err
}

func execSQLTransaction(ctx context.Context, conn *sql.Conn, batch []gorqlite.ParameterizedStatement) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return -1, fmt.Errorf("begin transaction: %w", err)
	}
	for i, st := range batch {
		if _, err := tx.ExecContext(ctx, st.Query, st.Arguments...); err != nil {
			_ = tx.Rollback()
			return i, err
		}
	}
	if err := tx.Commit(); err != nil {
		return -1, fmt.Errorf("commit transaction: %w", err)
	}
	return -1, nil
}
