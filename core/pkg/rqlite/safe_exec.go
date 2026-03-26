package rqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// SafeExecContext wraps db.ExecContext with panic recovery.
// The gorqlite stdlib driver can panic with "index out of range" when
// RQLite is temporarily unavailable. This converts the panic to an error.
func SafeExecContext(db *sql.DB, ctx context.Context, query string, args ...interface{}) (result sql.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("gorqlite panic (ExecContext): %v", r)
		}
	}()
	return db.ExecContext(ctx, query, args...)
}
