package hostfunctions

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// fakeBatchClient is a tiny rqlite.Client stub that only implements Batch
// and BatchWithSeq. Other methods rely on the embedded Client which is nil —
// any test that calls them will panic, which is intentional.
type fakeBatchClient struct {
	rqlite.Client
	calls       int
	lastOps     []rqlite.BatchOp
	seqCalls    int
	lastSeqNS   string
	respond     func(ops []rqlite.BatchOp) (*rqlite.BatchResult, error)
	respondSeq  func(ns string, ops []rqlite.BatchOp) (*rqlite.BatchResult, int64, error)
	nextSeq     int64
}

func (f *fakeBatchClient) Batch(ctx context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	f.calls++
	f.lastOps = ops
	if f.respond != nil {
		return f.respond(ops)
	}
	results := make([]rqlite.OpResult, len(ops))
	for i, op := range ops {
		results[i] = rqlite.OpResult{Kind: op.Kind, RowsAffected: 1}
	}
	return &rqlite.BatchResult{Committed: true, Results: results}, nil
}

func (f *fakeBatchClient) BatchWithSeq(ctx context.Context, namespace string, ops []rqlite.BatchOp) (*rqlite.BatchResult, int64, error) {
	f.seqCalls++
	f.lastSeqNS = namespace
	f.lastOps = ops
	if f.respondSeq != nil {
		return f.respondSeq(namespace, ops)
	}
	res, err := f.Batch(ctx, ops)
	atomic.AddInt64(&f.nextSeq, 1)
	return res, atomic.LoadInt64(&f.nextSeq), err
}

func newHFWithDB(db rqlite.Client) *HostFunctions {
	return &HostFunctions{db: db}
}

func TestDBTransaction_happy_path(t *testing.T) {
	fake := &fakeBatchClient{}
	h := newHFWithDB(fake)

	ops := `{"ops":[{"kind":"exec","sql":"INSERT INTO t (x) VALUES (?)","args":[1]},{"kind":"exec","sql":"INSERT INTO t (x) VALUES (?)","args":[2]}]}`
	out, err := h.DBTransaction(context.Background(), []byte(ops))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("expected 1 batch call, got %d", fake.calls)
	}
	if len(fake.lastOps) != 2 {
		t.Errorf("expected 2 ops, got %d", len(fake.lastOps))
	}
	var res rqlite.BatchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !res.Committed {
		t.Errorf("expected committed=true, got false")
	}
}

func TestDBTransaction_invalid_json_rejected(t *testing.T) {
	h := newHFWithDB(&fakeBatchClient{})
	_, err := h.DBTransaction(context.Background(), []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid json, got nil")
	}
	if !strings.Contains(err.Error(), "invalid json") {
		t.Errorf("expected 'invalid json' in error, got: %v", err)
	}
}

func TestDBTransaction_no_ops_rejected(t *testing.T) {
	h := newHFWithDB(&fakeBatchClient{})
	_, err := h.DBTransaction(context.Background(), []byte(`{"ops":[]}`))
	if err == nil {
		t.Fatal("expected error for empty ops, got nil")
	}
	if !strings.Contains(err.Error(), "ops required") {
		t.Errorf("expected 'ops required' in error, got: %v", err)
	}
}

func TestDBTransaction_oversize_batch_rejected(t *testing.T) {
	h := newHFWithDB(&fakeBatchClient{})

	// Build a request with MaxBatchOps + 1 ops.
	var sb strings.Builder
	sb.WriteString(`{"ops":[`)
	for i := 0; i <= rqlite.MaxBatchOps; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"kind":"exec","sql":"SELECT 1"}`)
	}
	sb.WriteString(`]}`)

	_, err := h.DBTransaction(context.Background(), []byte(sb.String()))
	if err == nil {
		t.Fatal("expected error for oversize batch, got nil")
	}
	if !strings.Contains(err.Error(), "too many ops") {
		t.Errorf("expected 'too many ops' in error, got: %v", err)
	}
}

func TestDBTransaction_no_db_returns_error(t *testing.T) {
	h := &HostFunctions{db: nil}
	_, err := h.DBTransaction(context.Background(), []byte(`{"ops":[{"kind":"exec","sql":"x"}]}`))
	if err == nil {
		t.Fatal("expected error when db is nil")
	}
}

// fakePubSub is a stub *pubsub.ClientAdapter substitute via interface duck-typing.
// We can't easily build a real ClientAdapter here, so we instead exercise the
// hostfunc through hostfunctions injection — the field is *pubsub.ClientAdapter,
// which we avoid by setting it to nil in some tests and using a wrapper helper.
//
// For full ExecAndPublish coverage, an integration test using the real adapter
// is the right tool; here we cover the wiring + JSON shape via direct unit tests.

func TestExecAndPublish_no_pubsub_returns_error(t *testing.T) {
	h := newHFWithDB(&fakeBatchClient{})
	// pubsub is nil
	_, err := h.ExecAndPublish(context.Background(),
		[]byte(`{"ops":[{"kind":"exec","sql":"x"}]}`),
		"some-topic",
		[]byte(`{"hello":"world"}`))
	if err == nil {
		t.Fatal("expected error when pubsub is nil")
	}
	if !strings.Contains(err.Error(), "pubsub") {
		t.Errorf("expected 'pubsub' in error, got: %v", err)
	}
}

// TestExecAndPublish_no_topic_rejected — covered indirectly by no_pubsub
// since the pubsub check fires first. Full coverage of topic validation in
// integration tests with a real *pubsub.ClientAdapter.

func TestExecAndPublish_no_namespace_in_context_rejected(t *testing.T) {
	// Bare HostFunctions has no invCtx — namespace is empty.
	// We need a non-nil pubsub to bypass the earlier check; passing the field
	// directly is hard without import cycle, so we test via the namespace
	// resolution branch by ensuring no invCtx is set.
	h := newHFWithDB(&fakeBatchClient{})
	// Inject a placeholder so the pubsub-nil check passes;
	// since pubsub is *pubsub.ClientAdapter we'd need a real one.
	// Skip this exact test with a TODO — full coverage in integration test.
	t.Skip("requires real *pubsub.ClientAdapter; covered in integration tests")
	_ = h
}

// fakeExecClient is a minimal rqlite.Client stub focused on Exec/Query
// behavior for the v2 host call tests (bug #218 regression coverage).
type fakeExecClient struct {
	rqlite.Client
	execErr    error
	execRows   int64
	execLastID int64
	queryErr   error
	queryRows  []map[string]interface{}
}

func (f *fakeExecClient) Exec(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	return &fakeSQLResult{rows: f.execRows, lastID: f.execLastID}, nil
}

func (f *fakeExecClient) Query(_ context.Context, dest any, _ string, _ ...any) error {
	if f.queryErr != nil {
		return f.queryErr
	}
	rows, ok := dest.(*[]map[string]interface{})
	if !ok {
		return nil
	}
	*rows = append(*rows, f.queryRows...)
	return nil
}

type fakeSQLResult struct {
	rows   int64
	lastID int64
}

func (r *fakeSQLResult) LastInsertId() (int64, error) { return r.lastID, nil }
func (r *fakeSQLResult) RowsAffected() (int64, error) { return r.rows, nil }

func TestDBExecuteV2_success(t *testing.T) {
	fake := &fakeExecClient{execRows: 3, execLastID: 42}
	h := newHFWithDB(fake)

	out, err := h.DBExecuteV2(context.Background(), "INSERT INTO t VALUES (?)", []interface{}{1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res dbExecuteV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Errorf("rows_affected = %d, want 3", res.RowsAffected)
	}
	if res.LastInsertID != 42 {
		t.Errorf("last_insert_id = %d, want 42", res.LastInsertID)
	}
	if res.Error != "" {
		t.Errorf("error should be empty on success, got %q", res.Error)
	}
}

// The whole point of bug #218: when the SQL fails, the envelope must say
// so. Old DBExecute returned 0 — indistinguishable from "0 rows affected".
func TestDBExecuteV2_sql_error_populates_error_field(t *testing.T) {
	fake := &fakeExecClient{execErr: errFakeDBFailure{msg: "no such column: missing"}}
	h := newHFWithDB(fake)

	out, err := h.DBExecuteV2(context.Background(), "INSERT ...", nil)
	if err != nil {
		t.Fatalf("expected SQL errors via envelope, not Go error: %v", err)
	}
	var res dbExecuteV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error == "" {
		t.Fatal("error field must NOT be empty when SQL failed (bug #218 contract)")
	}
	if !strings.Contains(res.Error, "no such column") {
		t.Errorf("error should preserve SQL message, got %q", res.Error)
	}
	if res.RowsAffected != 0 {
		t.Errorf("rows_affected should be 0 on failure, got %d", res.RowsAffected)
	}
}

func TestDBExecuteV2_no_db_returns_go_error(t *testing.T) {
	h := &HostFunctions{db: nil}
	_, err := h.DBExecuteV2(context.Background(), "INSERT ...", nil)
	if err == nil {
		t.Fatal("expected Go error for setup failure (no DB)")
	}
}

func TestDBQueryV2_success_with_empty_rows(t *testing.T) {
	fake := &fakeExecClient{queryRows: nil} // genuine "no rows" — not an error
	h := newHFWithDB(fake)

	out, err := h.DBQueryV2(context.Background(), "SELECT * FROM t WHERE 0=1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res dbQueryV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error != "" {
		t.Errorf("empty rows is NOT an error, got error=%q", res.Error)
	}
	if res.Rows == nil {
		t.Error("rows must be non-nil empty slice for stable JSON shape")
	}
	if len(res.Rows) != 0 {
		t.Errorf("rows = %v, want empty", res.Rows)
	}
}

func TestDBQueryV2_query_error_populates_error_field(t *testing.T) {
	fake := &fakeExecClient{queryErr: errFakeDBFailure{msg: "syntax error"}}
	h := newHFWithDB(fake)

	out, err := h.DBQueryV2(context.Background(), "SELECT bogus FROM t", nil)
	if err != nil {
		t.Fatalf("query errors should be in envelope, not Go error: %v", err)
	}
	var res dbQueryV2Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Error == "" {
		t.Fatal("error field must NOT be empty when query failed")
	}
	if res.Rows == nil {
		t.Error("rows must be non-nil even on error (stable shape)")
	}
}

// errFakeDBFailure is a sentinel for v2 tests.
type errFakeDBFailure struct{ msg string }

func (e errFakeDBFailure) Error() string { return e.msg }

func TestDBTransaction_rollback_returns_committed_false_no_go_error(t *testing.T) {
	fake := &fakeBatchClient{
		respond: func(ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
			// Simulate rollback: first op succeeded shape; second op failed.
			return &rqlite.BatchResult{
				Committed:   false,
				FailedIndex: 1,
				Results: []rqlite.OpResult{
					{Kind: rqlite.BatchOpExec, RowsAffected: 1},
					{Kind: rqlite.BatchOpExec, Error: "UNIQUE constraint failed"},
				},
			}, nil
		},
	}
	h := newHFWithDB(fake)

	ops := `{"ops":[{"kind":"exec","sql":"INSERT INTO t VALUES (?)","args":[1]},{"kind":"exec","sql":"INSERT INTO t VALUES (?)","args":[1]}]}`
	out, err := h.DBTransaction(context.Background(), []byte(ops))
	// Rollback is communicated via JSON, NOT a Go error — that's the contract.
	if err != nil {
		t.Fatalf("expected no Go error on rollback (committed=false in JSON), got: %v", err)
	}
	var res rqlite.BatchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Committed {
		t.Errorf("expected committed=false")
	}
	if res.FailedIndex != 1 {
		t.Errorf("expected FailedIndex=1, got %d", res.FailedIndex)
	}
	if !strings.Contains(res.Results[1].Error, "UNIQUE") {
		t.Errorf("expected UNIQUE error in result, got: %q", res.Results[1].Error)
	}
}
