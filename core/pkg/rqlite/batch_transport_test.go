package rqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/rqlite/gorqlite/stdlib"
)

// bugboard #267: a failure that never reached a statement belongs to the
// batch, not to op 0. gorqlite puts a transport error on a single first
// result, and reading that as "op 0 failed" both misattributes it and, before
// statement errors were typed, left it to be classified by its text.

// deadClient is a gateway-shaped client pointed at a port nothing listens on.
// With cluster discovery disabled, construction dials nothing.
func deadClient(t *testing.T) rqlite.Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("http://%s?disableClusterDiscovery=true&level=weak", addr)
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c, err := rqlite.NewClientWithDSN(db, dsn)
	if err != nil {
		t.Fatalf("NewClientWithDSN: %v", err)
	}
	return c
}

func TestBatch_transportFailureIsBatchLevel(t *testing.T) {
	res, err := deadClient(t).Batch(context.Background(), []rqlite.BatchOp{
		{Kind: rqlite.BatchOpExec, SQL: "INSERT INTO t VALUES (1)"},
	})
	if err == nil || res == nil {
		t.Fatalf("Batch against a dead node = %+v, %v; want a result and an error", res, err)
	}
	if res.Code != rqlite.BatchCodeUnavailable || res.Error == "" {
		t.Errorf("batch-level error/code = %q/%q, want the reason coded %s", res.Error, res.Code, rqlite.BatchCodeUnavailable)
	}
	if len(res.Results) != 1 || res.Results[0].Error != "" {
		t.Errorf("op 0 carries %q; a transport failure is not op 0's", res.Results[0].Error)
	}
	var stmt *rqlite.StatementError
	if errors.As(err, &stmt) {
		t.Error("a transport failure was typed as a statement error")
	}
}

func TestBatchQuery_transportFailureFailsTheBatch(t *testing.T) {
	results, err := deadClient(t).BatchQuery(context.Background(), []rqlite.BatchOp{
		{Kind: rqlite.BatchOpQuery, SQL: "SELECT 1"},
		{Kind: rqlite.BatchOpQuery, SQL: "SELECT 2"},
	})
	if err == nil || results != nil {
		t.Fatalf("BatchQuery against a dead node = %+v, %v; want (nil, err)", results, err)
	}
	if got := rqlite.ClassifyBatchError(err); got != rqlite.BatchCodeUnavailable {
		t.Errorf("code = %q, want %s", got, rqlite.BatchCodeUnavailable)
	}
}
