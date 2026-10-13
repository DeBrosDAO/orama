package rqlite

import (
	"context"
	"database/sql"

	"github.com/rqlite/gorqlite"
)

// Statement is one parameterised statement of an ExecBatch.
type Statement = gorqlite.ParameterizedStatement

// ExecBatch sends stmts to a *sql.DB as ONE transaction, which against rqlite is one request and
// one Raft log entry however many statements it holds (Client.Batch is the same for a caller that
// holds a native connection). failed is the index of the statement the database rejected, in which
// case nothing in the batch was applied; it is -1 when no statement is to blame (a transport
// fault, a lost leader, an expired deadline), and err is nil on success. An empty batch does
// nothing.
func ExecBatch(ctx context.Context, db *sql.DB, stmts []Statement) (failed int, err error) {
	if len(stmts) == 0 {
		return -1, nil
	}
	return execTransaction(ctx, db, stmts)
}
