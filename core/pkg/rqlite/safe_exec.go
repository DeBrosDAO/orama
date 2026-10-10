package rqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// The gorqlite stdlib driver reaches into the first element of the result slice
// without checking that the slice is non-empty:
//
//	func (conn *Connection) WriteOneParameterizedContext(...) (WriteResult, error) {
//	    wra, err := conn.WriteParameterizedContext(ctx, []ParameterizedStatement{statement})
//	    return wra[0], err   // panics when wra is empty
//	}
//
// When rqlite is reachable but cannot serve the request — no raft leader yet at
// boot, an election in flight, a connection reset mid-statement — the driver
// returns an empty slice alongside the error, and that indexing panics. An
// unrecovered panic in a request goroutine takes the whole process down, so a
// transient database condition becomes a crash-loop instead of a returned
// error. The same shape exists in all four WriteOne* variants.
//
// The wrappers below convert that panic into an ordinary error at every call
// site that touches the driver through database/sql. Every gateway and node
// path that runs SQL against rqlite MUST go through them rather than calling
// db.ExecContext / db.QueryContext directly.

// SafeExecContext wraps db.ExecContext with panic recovery, converting a
// gorqlite driver panic into an error.
func SafeExecContext(db *sql.DB, ctx context.Context, query string, args ...interface{}) (result sql.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (ExecContext): %v", r)
		}
	}()
	return db.ExecContext(ctx, query, args...)
}

// SafeQueryContext wraps db.QueryContext with panic recovery, converting a
// gorqlite driver panic into an error.
//
// On success the caller owns the returned *sql.Rows and must Close it, exactly
// as with db.QueryContext. On error rows is nil.
func SafeQueryContext(db *sql.DB, ctx context.Context, query string, args ...interface{}) (rows *sql.Rows, err error) {
	defer func() {
		if r := recover(); r != nil {
			rows = nil
			err = fmt.Errorf("gorqlite panic (QueryContext): %v", r)
		}
	}()
	return db.QueryContext(ctx, query, args...)
}
