package rqlite

// batch.go provides atomic multi-statement transactions over RQLite using the
// native /db/execute?transaction endpoint.
//
// Why this exists: the database/sql Begin/Commit path against the gorqlite
// stdlib driver does NOT produce real RQLite transactions (BEGIN/COMMIT are
// effectively no-ops in that driver). The only path to true atomicity is the
// native gorqlite.Connection.WriteParameterizedContext, which posts all
// statements in one HTTP request to RQLite with ?transaction set — RQLite
// then wraps them in a server-side transaction with rollback on any failure.
//
// This file exposes that path through a stable Client.Batch interface that
// works with both writes (atomic) and follow-up reads (sequenced after the
// commit). See plan: core/plans/platform/07_DB_TRANSACTION.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rqlite/gorqlite"
)

// BatchOpKind enumerates the supported op kinds.
type BatchOpKind string

const (
	BatchOpExec  BatchOpKind = "exec"
	BatchOpQuery BatchOpKind = "query"
)

// BatchOp is a single statement in a transactional batch.
type BatchOp struct {
	Kind BatchOpKind   `json:"kind"`
	SQL  string        `json:"sql"`
	Args []interface{} `json:"args,omitempty"`
}

// OpResult holds per-op output. On rollback, OpResults for ops up to and
// including the failing one are populated; the failing op carries Error.
type OpResult struct {
	Kind         BatchOpKind              `json:"kind"`
	RowsAffected int64                    `json:"rows_affected,omitempty"`
	LastInsertID int64                    `json:"last_insert_id,omitempty"`
	Rows         []map[string]interface{} `json:"rows,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

// BatchResult is the response from a transactional batch.
type BatchResult struct {
	Results     []OpResult `json:"results"`
	Committed   bool       `json:"committed"`
	FailedIndex int        `json:"failed_index,omitempty"` // valid only when !Committed
}

// MaxBatchOps caps the number of ops in a single batch to prevent abuse.
// 100 is plenty for any realistic transactional unit of work.
const MaxBatchOps = 100

// BatchWithSeq executes the user's ops atomically AND, in the same atomic
// batch, increments the per-namespace publish sequence counter so the caller
// can attach the assigned seq to a follow-up wake-up message.
//
// On commit, the returned int64 is the seq assigned to this commit (and to
// any subscriber-visible side effects). On rollback (Committed=false), the
// returned int64 is 0 and the per-namespace counter is unchanged.
//
// Implementation note: the seq UPSERT runs first so that if the user's ops
// later in the batch fail, the increment also rolls back — keeping the
// counter consistent with what was actually published.
func (c *client) BatchWithSeq(ctx context.Context, namespace string, userOps []BatchOp) (*BatchResult, int64, error) {
	if namespace == "" {
		return nil, 0, fmt.Errorf("rqlite.BatchWithSeq: namespace required")
	}
	if c.conn == nil {
		return nil, 0, fmt.Errorf("rqlite.BatchWithSeq: native gorqlite connection not configured")
	}

	now := time.Now().Unix()

	// Prepend the seq UPSERT. RETURNING (SQLite 3.35+) gives us the new value
	// without a follow-up SELECT.
	seqOp := BatchOp{
		Kind: BatchOpExec,
		SQL: `INSERT INTO namespace_publish_seq (namespace, next_seq, updated_at)
		      VALUES (?, 2, ?)
		      ON CONFLICT(namespace) DO UPDATE SET
		          next_seq   = next_seq + 1,
		          updated_at = excluded.updated_at`,
		Args: []interface{}{namespace, now},
	}

	// We follow with a query of the just-incremented value. Sequenced after
	// commit on the same node — sees the just-applied write. The query is
	// per-namespace so under concurrent commits each call still gets its
	// own unique seq because the UPSERT itself is atomic.
	seqQuery := BatchOp{
		Kind: BatchOpQuery,
		SQL:  `SELECT next_seq - 1 AS assigned_seq FROM namespace_publish_seq WHERE namespace = ?`,
		Args: []interface{}{namespace},
	}

	combined := make([]BatchOp, 0, len(userOps)+2)
	combined = append(combined, seqOp)
	combined = append(combined, userOps...)
	combined = append(combined, seqQuery)

	res, err := c.Batch(ctx, combined)
	if err != nil || res == nil || !res.Committed {
		// Trim our seq op back out of the result so the caller sees only
		// their own ops in the response (preserve original indexing).
		trimmed := trimWrappedResults(res, len(userOps))
		return trimmed, 0, err
	}

	// Read the assigned seq from the trailing query result.
	queryResult := res.Results[len(res.Results)-1]
	if queryResult.Error != "" {
		// Writes committed but the query failed — caller still got their writes.
		// Return the trimmed result with seq=0 so the caller can detect "writes
		// landed but seq unknown."
		return trimWrappedResults(res, len(userOps)), 0, fmt.Errorf("rqlite.BatchWithSeq: seq lookup failed: %s", queryResult.Error)
	}
	if len(queryResult.Rows) == 0 {
		return trimWrappedResults(res, len(userOps)), 0, fmt.Errorf("rqlite.BatchWithSeq: seq lookup returned no rows")
	}
	rawSeq, ok := queryResult.Rows[0]["assigned_seq"]
	if !ok {
		return trimWrappedResults(res, len(userOps)), 0, fmt.Errorf("rqlite.BatchWithSeq: assigned_seq column missing")
	}
	seq, err := coerceInt64(rawSeq)
	if err != nil {
		return trimWrappedResults(res, len(userOps)), 0, fmt.Errorf("rqlite.BatchWithSeq: seq coerce: %w", err)
	}
	return trimWrappedResults(res, len(userOps)), seq, nil
}

// trimWrappedResults removes the leading seq UPSERT and trailing seq SELECT
// from a wrapped batch result so the caller sees only their original ops.
// Pass-through if res is nil.
func trimWrappedResults(res *BatchResult, userOpCount int) *BatchResult {
	if res == nil {
		return nil
	}
	if len(res.Results) < userOpCount+1 {
		// Failed before user ops ran; return as-is so caller can inspect.
		return res
	}
	out := &BatchResult{
		Committed: res.Committed,
	}
	// Drop the first (seq UPSERT) and trailing (seq SELECT) entries.
	end := len(res.Results) - 1
	if end > userOpCount+1 {
		end = userOpCount + 1
	}
	out.Results = make([]OpResult, 0, userOpCount)
	for i := 1; i < end; i++ {
		out.Results = append(out.Results, res.Results[i])
	}
	// Adjust FailedIndex if it pointed into the user's ops.
	if !res.Committed {
		switch {
		case res.FailedIndex == 0:
			// Failure was in our seq UPSERT — surface as "before user ops".
			out.FailedIndex = -1
		case res.FailedIndex > 0 && res.FailedIndex <= userOpCount:
			out.FailedIndex = res.FailedIndex - 1
		default:
			// Failure was in the trailing query (post-commit) — committed should be true; defensive.
			out.FailedIndex = userOpCount
		}
	}
	return out
}

// coerceInt64 normalizes a JSON-decoded number (which may arrive as float64,
// int64, or json.Number depending on the SQLite driver) to int64.
func coerceInt64(v interface{}) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case json.Number:
		return n.Int64()
	case string:
		// Some drivers return TEXT for INTEGER columns under strict mode.
		var i int64
		if _, err := fmt.Sscanf(n, "%d", &i); err != nil {
			return 0, fmt.Errorf("string %q is not an int64: %w", n, err)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

// Batch executes ops as a single atomic transaction.
//
// Semantics:
//   - All "exec" ops are sent in one transactional batch via RQLite's native
//     /db/execute?transaction endpoint. If any exec fails, the entire batch
//     rolls back; no exec is durable.
//   - Any "query" ops are sequenced AFTER the exec batch commits, on the same
//     node, and see the committed writes. Queries do NOT participate in the
//     rollback semantic — if a query fails after the writes commit, the writes
//     are still durable; that op's Error is set and Committed remains true.
//   - Order of OpResults preserved across the original input slice.
//
// Returns:
//   - (result, nil) when all execs commit. result.Committed is true.
//   - (result, err) when an exec fails. result.Committed is false and
//     result.FailedIndex points to the failing op. The error is nil-safe to
//     ignore if you only need the structured result.
//   - (nil, err) for setup failures (no native connection, validation, etc.).
func (c *client) Batch(ctx context.Context, ops []BatchOp) (*BatchResult, error) {
	if len(ops) == 0 {
		return &BatchResult{Committed: true, Results: []OpResult{}}, nil
	}
	if len(ops) > MaxBatchOps {
		return nil, fmt.Errorf("rqlite.Batch: too many ops (%d > max %d)", len(ops), MaxBatchOps)
	}
	if c.conn == nil {
		return nil, fmt.Errorf("rqlite.Batch: native gorqlite connection not configured (use NewClientWithDSN or NewClientWithConn)")
	}

	// Split exec vs. query, preserving original index for result ordering.
	type tagged struct {
		idx int
		op  BatchOp
	}
	var execs, queries []tagged
	for i, op := range ops {
		switch op.Kind {
		case BatchOpExec:
			execs = append(execs, tagged{i, op})
		case BatchOpQuery:
			queries = append(queries, tagged{i, op})
		default:
			return nil, fmt.Errorf("rqlite.Batch: op %d has unknown kind %q (want %q or %q)",
				i, op.Kind, BatchOpExec, BatchOpQuery)
		}
	}

	result := &BatchResult{
		Results:   make([]OpResult, len(ops)),
		Committed: false,
	}

	// Phase 1 — atomic exec batch via native API.
	if len(execs) > 0 {
		stmts := make([]gorqlite.ParameterizedStatement, len(execs))
		for i, t := range execs {
			stmts[i] = gorqlite.ParameterizedStatement{
				Query:     t.op.SQL,
				Arguments: t.op.Args,
			}
		}
		wrs, err := c.conn.WriteParameterizedContext(ctx, stmts)
		if err != nil {
			// gorqlite returns one WriteResult per statement, even on error.
			// Find the first failing one to populate FailedIndex.
			for i, wr := range wrs {
				if wr.Err != nil {
					result.FailedIndex = execs[i].idx
					result.Results[execs[i].idx] = OpResult{
						Kind:  BatchOpExec,
						Error: wr.Err.Error(),
					}
					return result, fmt.Errorf("rqlite.Batch: exec failed at op %d: %w",
						execs[i].idx, wr.Err)
				}
			}
			// No per-statement error reported, return the joined error.
			return result, fmt.Errorf("rqlite.Batch: %w", err)
		}
		// All execs succeeded; map results back into their original positions.
		for i, wr := range wrs {
			result.Results[execs[i].idx] = OpResult{
				Kind:         BatchOpExec,
				RowsAffected: wr.RowsAffected,
				LastInsertID: wr.LastInsertID,
			}
		}
	}
	result.Committed = true

	// Phase 2 — post-commit queries. Failures here do NOT trigger rollback
	// (the writes are already durable), but are surfaced per-op.
	for _, t := range queries {
		var rows []map[string]interface{}
		err := c.Query(ctx, &rows, t.op.SQL, t.op.Args...)
		if err != nil {
			result.Results[t.idx] = OpResult{
				Kind:  BatchOpQuery,
				Error: err.Error(),
			}
			continue
		}
		result.Results[t.idx] = OpResult{
			Kind: BatchOpQuery,
			Rows: rows,
		}
	}
	return result, nil
}
