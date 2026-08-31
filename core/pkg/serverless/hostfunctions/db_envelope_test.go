package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Bugboard #175.
//
// A batched database call that fails at the transport/host level used to give
// the guest nothing: db_transaction returned an envelope whose only populated
// field was committed=false, and db_query_batch / exec_and_publish returned an
// empty buffer. A real namespace outage — a group write crossing the
// 100-statement cap — had to be diagnosed by deploying probe functions and
// hand-counting statements, because the reason existed only in the gateway
// journal, which tenants cannot read.
//
// Every test here asserts the same contract: a failing call always tells the
// caller WHY, in a form it can branch on.

// A whole-batch failure that reached no statement leaves every per-op result
// zero-valued. Without a batch-level reason the guest sees committed=false and
// failed_index=0, which reads as "op 0 failed" — the single most misleading
// thing it could conclude.
func TestDBTransaction_transportFailureCarriesReason(t *testing.T) {
	fake := &fakeBatchClient{
		respond: func(ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
			// Exactly the shape rqlite.Batch returns when the write never
			// reached a statement: a non-nil result with nothing filled in.
			return &rqlite.BatchResult{
				Results:   make([]rqlite.OpResult, len(ops)),
				Committed: false,
			}, errors.New("rqlite.Batch: dial tcp 10.0.0.2:5001: connect: connection refused")
		},
	}
	h := newHFWithDB(fake)

	out, err := h.DBTransaction(context.Background(),
		[]byte(`{"ops":[{"kind":"exec","sql":"INSERT INTO t (x) VALUES (?)","args":[1]}]}`))
	if err != nil {
		t.Fatalf("a batch-level failure must be reported in the envelope, not as a Go error: %v", err)
	}

	var res rqlite.BatchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if res.Committed {
		t.Error("committed must be false")
	}
	if res.Error == "" {
		t.Fatal("envelope carries no reason — this is the bug: the caller cannot tell a refused connection from a failing statement")
	}
	if res.Code != rqlite.BatchCodeUnavailable {
		t.Errorf("code = %q, want %q", res.Code, rqlite.BatchCodeUnavailable)
	}
}

// A rollback attributable to one statement is already fully described by that
// op's own error. The batch-level reason must not overwrite or duplicate it.
func TestDBTransaction_statementFailureKeepsPerOpDetail(t *testing.T) {
	fake := &fakeBatchClient{
		respond: func(ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
			results := make([]rqlite.OpResult, len(ops))
			results[1] = rqlite.OpResult{Kind: rqlite.BatchOpExec, Error: "UNIQUE constraint failed: t.x"}
			return &rqlite.BatchResult{Results: results, Committed: false, FailedIndex: 1},
				errors.New("rqlite.Batch: exec failed at op 1")
		},
	}
	h := newHFWithDB(fake)

	out, err := h.DBTransaction(context.Background(),
		[]byte(`{"ops":[{"kind":"exec","sql":"A"},{"kind":"exec","sql":"B"}]}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var res rqlite.BatchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if res.FailedIndex != 1 {
		t.Errorf("failed_index = %d, want 1", res.FailedIndex)
	}
	if !strings.Contains(res.Results[1].Error, "UNIQUE constraint") {
		t.Errorf("per-op SQL error lost: %+v", res.Results[1])
	}
	if res.Error != "" {
		t.Errorf("a statement failure is attributable to one op; batch-level error must stay empty, got %q", res.Error)
	}
}

// The limit that actually bit in production. It must be reported as a
// deterministic, named condition — not as something a retry might fix.
func TestDBTransaction_overStatementCapIsNamed(t *testing.T) {
	ops := make([]string, rqlite.MaxBatchOps+1)
	for i := range ops {
		ops[i] = `{"kind":"exec","sql":"INSERT INTO t (x) VALUES (1)"}`
	}
	payload := fmt.Sprintf(`{"ops":[%s]}`, strings.Join(ops, ","))

	_, err := newHFWithDB(&fakeBatchClient{}).DBTransaction(context.Background(), []byte(payload))
	if err == nil {
		t.Fatal("a batch over the statement cap must fail")
	}
	if code := rqlite.ClassifyBatchError(err); code != rqlite.BatchCodeTooManyStatements {
		t.Errorf("code = %q, want %q (error was %q)", code, rqlite.BatchCodeTooManyStatements, err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(rqlite.MaxBatchOps)) {
		t.Errorf("the error must name the limit so a caller can stay under it, got %q", err)
	}
}

// exec_and_publish shares db_transaction's failure modes and had the same hole:
// its result struct had no field able to carry a batch-level reason at all.
func TestExecAndPublish_transportFailureCarriesReason(t *testing.T) {
	fake := &fakeBatchClient{
		respondSeq: func(ns string, ops []rqlite.BatchOp) (*rqlite.BatchResult, int64, error) {
			return &rqlite.BatchResult{Results: make([]rqlite.OpResult, len(ops))}, 0,
				errors.New("rqlite.BatchWithSeq: no leader")
		},
	}
	h := &HostFunctions{db: fake, pubsub: &pubsub.ClientAdapter{}}

	// A namespace must be resolvable from the invocation context, and the
	// publish budget must be live — the failure under test happens at the
	// batch, well before either is exercised further.
	ctx := serverless.WithInvocationContext(
		serverless.WithPublishCounter(context.Background()),
		&serverless.InvocationContext{Namespace: "ns-test"},
	)
	out, err := h.ExecAndPublish(ctx,
		[]byte(`{"ops":[{"kind":"exec","sql":"INSERT INTO t (x) VALUES (1)"}]}`),
		"topic", []byte("{}"))
	if err != nil {
		t.Fatalf("a batch-level failure must be reported in the envelope: %v", err)
	}

	var res struct {
		Committed bool   `json:"committed"`
		Error     string `json:"error"`
		Code      string `json:"code"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if res.Committed {
		t.Error("committed must be false")
	}
	if res.Error == "" {
		t.Fatal("exec_and_publish envelope carries no reason")
	}
	if res.Code != rqlite.BatchCodeUnavailable {
		t.Errorf("code = %q, want %q", res.Code, rqlite.BatchCodeUnavailable)
	}
}

// A successful call must stay byte-compatible with what existing guests parse:
// no error, no code, and the fields they already read unchanged.
func TestBatchEnvelopes_successCarriesNoErrorFields(t *testing.T) {
	h := newHFWithDB(&fakeBatchClient{})

	out, err := h.DBTransaction(context.Background(),
		[]byte(`{"ops":[{"kind":"exec","sql":"INSERT INTO t (x) VALUES (1)"}]}`))
	if err != nil {
		t.Fatalf("DBTransaction: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["error"]; ok {
		t.Error("success envelope must omit error")
	}
	if _, ok := raw["code"]; ok {
		t.Error("success envelope must omit code")
	}
	if committed, _ := raw["committed"].(bool); !committed {
		t.Error("committed must be true on the success path")
	}
}
