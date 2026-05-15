package hostfunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// DBQuery executes a SELECT query and returns JSON-encoded results.
func (h *HostFunctions) DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: serverless.ErrDatabaseUnavailable}
	}

	var results []map[string]interface{}
	if err := h.db.Query(ctx, &results, query, args...); err != nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: err}
	}

	data, err := json.Marshal(results)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "db_query", Cause: fmt.Errorf("failed to marshal results: %w", err)}
	}

	return data, nil
}

// DBExecute executes an INSERT/UPDATE/DELETE query and returns affected rows.
//
// IMPORTANT: this returns 0 for BOTH "0 rows affected" AND "SQL error"
// — callers can't distinguish. That ambiguity caused bug #218 (AnChat's
// migrate function silently dropped statements). For new code, prefer
// DBExecuteV2 which returns a typed envelope.
func (h *HostFunctions) DBExecute(ctx context.Context, query string, args []interface{}) (int64, error) {
	if h.db == nil {
		return 0, &serverless.HostFunctionError{Function: "db_execute", Cause: serverless.ErrDatabaseUnavailable}
	}

	result, err := h.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, &serverless.HostFunctionError{Function: "db_execute", Cause: err}
	}

	affected, _ := result.RowsAffected()
	return affected, nil
}

// dbExecuteV2Result is the JSON wire shape returned by DBExecuteV2.
type dbExecuteV2Result struct {
	RowsAffected int64  `json:"rows_affected"`
	LastInsertID int64  `json:"last_insert_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// DBExecuteV2 is the typed equivalent of DBExecute. Returns the same shape
// regardless of success/failure so callers can distinguish "0 rows affected"
// from "SQL error" — fixing the contract gap that caused bug #218.
//
// Returns a Go error only for host-side setup failures (no DB). SQL errors
// are encoded in the JSON envelope's "error" field.
func (h *HostFunctions) DBExecuteV2(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{
			Function: "db_execute_v2",
			Cause:    serverless.ErrDatabaseUnavailable,
		}
	}

	out := dbExecuteV2Result{}
	result, err := h.db.Exec(ctx, query, args...)
	if err != nil {
		out.Error = err.Error()
		buf, mErr := json.Marshal(out)
		if mErr != nil {
			return nil, &serverless.HostFunctionError{Function: "db_execute_v2", Cause: mErr}
		}
		return buf, nil
	}
	if result != nil {
		out.RowsAffected, _ = result.RowsAffected()
		out.LastInsertID, _ = result.LastInsertId()
	}
	buf, mErr := json.Marshal(out)
	if mErr != nil {
		return nil, &serverless.HostFunctionError{Function: "db_execute_v2", Cause: mErr}
	}
	return buf, nil
}

// dbQueryV2Result is the JSON wire shape returned by DBQueryV2.
type dbQueryV2Result struct {
	Rows  []map[string]interface{} `json:"rows"`
	Error string                   `json:"error,omitempty"`
}

// DBQueryV2 is the typed equivalent of DBQuery. Distinguishes "empty
// result set" from "query failed" via the "error" field.
func (h *HostFunctions) DBQueryV2(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{
			Function: "db_query_v2",
			Cause:    serverless.ErrDatabaseUnavailable,
		}
	}

	out := dbQueryV2Result{Rows: []map[string]interface{}{}}
	if err := h.db.Query(ctx, &out.Rows, query, args...); err != nil {
		out.Error = err.Error()
		// Reset rows to non-nil empty on error so callers get a stable shape.
		out.Rows = []map[string]interface{}{}
	}
	buf, mErr := json.Marshal(out)
	if mErr != nil {
		return nil, &serverless.HostFunctionError{Function: "db_query_v2", Cause: mErr}
	}
	return buf, nil
}

// dbTransactionRequest is the WASM-side shape for db_transaction input.
type dbTransactionRequest struct {
	Ops []rqlite.BatchOp `json:"ops"`
}

// DBTransaction executes ops as a single atomic batch.
// Input is JSON: {"ops": [{"kind":"exec"|"query","sql":"...","args":[...]}, ...]}
// Output is JSON: BatchResult — caller checks `committed` to know if writes landed.
//
// Returns an error only for setup/validation problems. A rolled-back batch is
// communicated via committed=false in the returned JSON; that's not a Go error.
func (h *HostFunctions) DBTransaction(ctx context.Context, opsJSON []byte) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{Function: "db_transaction", Cause: serverless.ErrDatabaseUnavailable}
	}
	var req dbTransactionRequest
	if err := json.Unmarshal(opsJSON, &req); err != nil {
		return nil, &serverless.HostFunctionError{
			Function: "db_transaction",
			Cause:    fmt.Errorf("invalid json: %w", err),
		}
	}
	if len(req.Ops) == 0 {
		return nil, &serverless.HostFunctionError{
			Function: "db_transaction",
			Cause:    fmt.Errorf("ops required"),
		}
	}
	if len(req.Ops) > rqlite.MaxBatchOps {
		return nil, &serverless.HostFunctionError{
			Function: "db_transaction",
			Cause:    fmt.Errorf("too many ops: max %d", rqlite.MaxBatchOps),
		}
	}

	res, err := h.db.Batch(ctx, req.Ops)
	// Always return the structured result, even on rollback — caller wants the
	// per-op detail to know which op failed.
	if res == nil {
		// Unrecoverable setup failure (no native conn). Surface as Go error.
		return nil, &serverless.HostFunctionError{Function: "db_transaction", Cause: err}
	}
	out, mErr := json.Marshal(res)
	if mErr != nil {
		return nil, &serverless.HostFunctionError{
			Function: "db_transaction",
			Cause:    fmt.Errorf("marshal result: %w", mErr),
		}
	}
	// Rollback errors are encoded in the JSON; do NOT propagate as Go error.
	// Only true setup/transport errors after the result was built warrant returning err.
	_ = err // intentionally swallowed — committed=false carries the signal
	return out, nil
}

// execAndPublishResult is the JSON wire shape returned to WASM callers.
type execAndPublishResult struct {
	Results      []rqlite.OpResult `json:"results"`
	Committed    bool              `json:"committed"`
	FailedIndex  int               `json:"failed_index,omitempty"`
	Seq          int64             `json:"seq,omitempty"`
	Published    bool              `json:"published,omitempty"`
	PublishError string            `json:"publish_error,omitempty"`
}

// ExecAndPublish runs ops atomically (with a seq increment in the same batch)
// and, if committed, publishes data with `{{seq}}` substituted for the
// assigned per-namespace sequence number.
//
// Failure modes (each communicated in the JSON, not as Go error):
//   - Rollback: committed=false, failed_index points to the failing user op
//   - Publish failed but commit succeeded: committed=true, published=false,
//     publish_error is set. Writes are durable; caller can retry the publish.
//   - Both succeeded: committed=true, published=true.
//
// Returns a Go error only on setup failures (no DB, bad JSON, no namespace).
func (h *HostFunctions) ExecAndPublish(
	ctx context.Context, opsJSON []byte, topic string, dataTemplate []byte,
) ([]byte, error) {
	if h.db == nil {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    serverless.ErrDatabaseUnavailable,
		}
	}
	if h.pubsub == nil {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    fmt.Errorf("pubsub not available"),
		}
	}
	if topic == "" {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    fmt.Errorf("topic required"),
		}
	}

	// Resolve namespace from invocation context — server-trusted.
	// ctx-attached invCtx wins over singleton; see invocation_context.go.
	ns := ""
	if cur := h.currentInvocationContext(ctx); cur != nil {
		ns = cur.Namespace
	}
	if ns == "" {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    fmt.Errorf("no namespace in invocation context"),
		}
	}

	var req dbTransactionRequest
	if err := json.Unmarshal(opsJSON, &req); err != nil {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    fmt.Errorf("invalid ops json: %w", err),
		}
	}
	if len(req.Ops) > rqlite.MaxBatchOps {
		return nil, &serverless.HostFunctionError{
			Function: "exec_and_publish",
			Cause:    fmt.Errorf("too many ops: max %d", rqlite.MaxBatchOps),
		}
	}

	batchRes, seq, batchErr := h.db.BatchWithSeq(ctx, ns, req.Ops)
	out := execAndPublishResult{}
	if batchRes != nil {
		out.Results = batchRes.Results
		out.Committed = batchRes.Committed
		out.FailedIndex = batchRes.FailedIndex
	}

	// On rollback or pre-publish error, return without publishing.
	if batchErr != nil || !out.Committed {
		// On a true rollback batchErr may be non-nil; that's already encoded
		// in the result. Don't surface as Go error — caller reads `committed`.
		_ = batchErr
		buf, mErr := json.Marshal(out)
		if mErr != nil {
			return nil, &serverless.HostFunctionError{Function: "exec_and_publish", Cause: mErr}
		}
		return buf, nil
	}

	out.Seq = seq

	// Substitute {{seq}} in the data template, then publish.
	finalData := bytes.ReplaceAll(
		dataTemplate,
		[]byte("{{seq}}"),
		[]byte(strconv.FormatInt(seq, 10)),
	)
	if err := h.pubsub.Publish(ctx, topic, finalData); err != nil {
		out.PublishError = err.Error()
	} else {
		out.Published = true
	}

	buf, mErr := json.Marshal(out)
	if mErr != nil {
		return nil, &serverless.HostFunctionError{Function: "exec_and_publish", Cause: mErr}
	}
	return buf, nil
}
