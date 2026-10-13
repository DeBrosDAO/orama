package rqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func batchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExecBatch_appliesEveryStatementOrNone(t *testing.T) {
	db := batchDB(t)
	ok := []Statement{{Query: `INSERT INTO t (id, v) VALUES (?, ?)`, Arguments: []any{1, "a"}}, {Query: `INSERT INTO t (id, v) VALUES (?, ?)`, Arguments: []any{2, "b"}}}
	if failed, err := ExecBatch(context.Background(), db, ok); err != nil || failed != -1 {
		t.Fatalf("failed %d err %v", failed, err)
	}
	bad := []Statement{{Query: `INSERT INTO t (id, v) VALUES (3, 'c')`}, {Query: `INSERT INTO t (id, v) VALUES (3, 'dup')`}}
	failed, err := ExecBatch(context.Background(), db, bad)
	if err == nil || failed != 1 {
		t.Fatalf("failed %d err %v, want statement 1 to be blamed", failed, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("%d rows, %v: the rejected batch left a partial write", n, err)
	}
}

func TestExecBatch_emptyDoesNothing(t *testing.T) {
	if failed, err := ExecBatch(context.Background(), batchDB(t), nil); err != nil || failed != -1 {
		t.Fatalf("failed %d err %v", failed, err)
	}
}
