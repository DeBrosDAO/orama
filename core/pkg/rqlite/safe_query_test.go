package rqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// panicDriver reproduces the gorqlite defect: WriteOne*/query helpers index the
// first element of a result slice without checking that the slice is non-empty,
// so a transient database condition panics instead of returning an error.
type panicDriver struct{ mode string }

func (d *panicDriver) Open(string) (driver.Conn, error) { return &panicConn{d: d}, nil }

type panicConn struct{ d *panicDriver }

func (c *panicConn) Prepare(string) (driver.Stmt, error) { return &panicStmt{d: c.d}, nil }
func (c *panicConn) Close() error                        { return nil }
func (c *panicConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }

type panicStmt struct{ d *panicDriver }

func (s *panicStmt) Close() error  { return nil }
func (s *panicStmt) NumInput() int { return 0 }

func (s *panicStmt) Exec([]driver.Value) (driver.Result, error) {
	if s.d.mode == "exec" || s.d.mode == "both" {
		var empty []int
		_ = empty[0] // index out of range [0] with length 0
	}
	return driver.RowsAffected(0), nil
}

func (s *panicStmt) Query([]driver.Value) (driver.Rows, error) {
	if s.d.mode == "query" || s.d.mode == "both" {
		var empty []int
		_ = empty[0]
	}
	return &noRows{}, nil
}

type noRows struct{}

func (r *noRows) Columns() []string              { return []string{"x"} }
func (r *noRows) Close() error                   { return nil }
func (r *noRows) Next(dest []driver.Value) error { return io.EOF }

func openPanicDB(t *testing.T, mode string) *sql.DB {
	t.Helper()
	name := "panic-" + t.Name()
	sql.Register(name, &panicDriver{mode: mode})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSafeQueryContext_convertsDriverPanicToError(t *testing.T) {
	db := openPanicDB(t, "query")

	rows, err := SafeQueryContext(db, context.Background(), "SELECT 1")
	if err == nil {
		if rows != nil {
			rows.Close()
		}
		t.Fatal("want an error, not a panic escaping to the caller")
	}
	if rows != nil {
		t.Error("rows must be nil when the driver panicked")
	}
	if !strings.Contains(err.Error(), "gorqlite panic") {
		t.Errorf("error = %q; want it to name the gorqlite panic", err)
	}
}

func TestSafeExecContext_convertsDriverPanicToError(t *testing.T) {
	db := openPanicDB(t, "exec")

	if _, err := SafeExecContext(db, context.Background(), "CREATE TABLE t (id INTEGER)"); err == nil {
		t.Fatal("want an error, not a panic escaping to the caller")
	} else if !strings.Contains(err.Error(), "gorqlite panic") {
		t.Errorf("error = %q; want it to name the gorqlite panic", err)
	}
}

func TestSafeQueryContext_passesThroughSuccess(t *testing.T) {
	db := openPanicDB(t, "none")

	rows, err := SafeQueryContext(db, context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("SafeQueryContext: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("expected no rows from the stub")
	}
}

func TestSafeExecContext_passesThroughSuccess(t *testing.T) {
	db := openPanicDB(t, "none")

	if _, err := SafeExecContext(db, context.Background(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatalf("SafeExecContext: %v", err)
	}
}
