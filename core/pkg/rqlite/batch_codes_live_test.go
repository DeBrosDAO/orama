package rqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
)

// bugboard #267: per-op codes, checked against what a real rqlite returns.
// SQLite's wording reaches us through rqlite's HTTP API and gorqlite, and that
// is the only place it can be verified.

func createConstrainedTable(t *testing.T, c rqlite.Client) {
	t.Helper()
	res, err := c.Batch(context.Background(), []rqlite.BatchOp{{
		Kind: rqlite.BatchOpExec,
		SQL:  `CREATE TABLE t (id INTEGER PRIMARY KEY, email TEXT UNIQUE NOT NULL, n INTEGER CHECK (n > 0), timeout TEXT UNIQUE)`,
	}})
	if err != nil || !res.Committed {
		t.Fatalf("create table: res=%+v err=%v", res, err)
	}
}

func TestBatch_constraintViolationsAreCoded(t *testing.T) {
	c := rqlitetest.Start(t)
	createConstrainedTable(t, c)
	ctx := context.Background()

	seed, err := c.Batch(ctx, []rqlite.BatchOp{{Kind: rqlite.BatchOpExec,
		SQL: "INSERT INTO t(id, email, n, timeout) VALUES (1, 'a', 1, 'x')"}})
	if err != nil || !seed.Committed {
		t.Fatalf("seed insert: res=%+v err=%v", seed, err)
	}

	for _, tc := range []struct{ name, sql string }{
		{"unique", "INSERT INTO t(id, email, n) VALUES (2, 'a', 1)"},
		{"primary key", "INSERT INTO t(id, email, n) VALUES (1, 'b', 1)"},
		{"not null", "INSERT INTO t(id, email, n) VALUES (3, NULL, 1)"},
		{"check", "INSERT INTO t(id, email, n) VALUES (4, 'c', 0)"},
		{"unique on a column named timeout", "INSERT INTO t(id, email, n, timeout) VALUES (5, 'd', 1, 'x')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.Batch(ctx, []rqlite.BatchOp{{Kind: rqlite.BatchOpExec, SQL: tc.sql}})
			if res == nil || res.Committed || len(res.Results) != 1 || res.FailedIndex != 0 {
				t.Fatalf("want a rolled-back one-op result failing at op 0, got %+v", res)
			}
			var stmt *rqlite.StatementError
			if !errors.As(err, &stmt) {
				t.Errorf("err = %v, want it to wrap a *rqlite.StatementError", err)
			}
			if res.Error != "" || res.Code != "" {
				t.Errorf("a statement failure set the batch-level error/code %q/%q; it belongs to the op", res.Error, res.Code)
			}
			op := res.Results[0]
			if op.Code != rqlite.BatchCodeConstraintViolation {
				t.Fatalf("code = %q (error %q), want %q", op.Code, op.Error, rqlite.BatchCodeConstraintViolation)
			}
			if !strings.Contains(op.Error, "constraint failed") {
				t.Errorf("error %q lost SQLite's reason", op.Error)
			}
		})
	}
}

func TestBatch_otherStatementErrorsAreInternal(t *testing.T) {
	// Each of these names an identifier that matches a transport pattern
	// ("timeout", "eof", "unavailable"). Read as text they would tell the
	// caller to retry a statement that can never succeed.
	c := rqlitetest.Start(t)
	createConstrainedTable(t, c)

	for _, sql := range []string{
		"INSERT INTO nosuch VALUES (1)",
		"INSERT INTO geofences VALUES (1)",
		"UPDATE t SET unavailable_since = 1",
		"SELEC 1",
	} {
		res, _ := c.Batch(context.Background(), []rqlite.BatchOp{{Kind: rqlite.BatchOpExec, SQL: sql}})
		if res == nil || len(res.Results) != 1 {
			t.Fatalf("%s: want a one-op result, got %+v", sql, res)
		}
		if got := res.Results[0].Code; got != rqlite.BatchCodeInternal {
			t.Errorf("%s: code = %q (error %q), want %q", sql, got, res.Results[0].Error, rqlite.BatchCodeInternal)
		}
	}
}

func TestBatchQuery_perOpErrorIsCoded(t *testing.T) {
	c := rqlitetest.Start(t)
	results, err := c.BatchQuery(context.Background(), []rqlite.BatchOp{
		{Kind: rqlite.BatchOpQuery, SQL: "SELECT 1 AS one"},
		{Kind: rqlite.BatchOpQuery, SQL: "SELECT * FROM nosuch"},
	})
	if err != nil {
		t.Fatalf("BatchQuery: %v", err)
	}
	if results[0].Error != "" || results[0].Code != "" {
		t.Errorf("successful op carries error %q / code %q", results[0].Error, results[0].Code)
	}
	if results[1].Error == "" || results[1].Code != rqlite.BatchCodeInternal {
		t.Errorf("failed op: error %q code %q; want an error coded %q", results[1].Error, results[1].Code, rqlite.BatchCodeInternal)
	}
}
