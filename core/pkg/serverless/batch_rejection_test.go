package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Bugboard #175. The rejection envelope is the ONLY thing a WASM guest receives
// when a batched database call fails at the host level. One shape is written
// for db_transaction, db_query_batch and exec_and_publish, so it must decode
// into all three of their success structs — a guest that gets an envelope it
// cannot parse is no better off than one that got an empty buffer.

func TestBatchRejectionPayload_carriesReasonAndCode(t *testing.T) {
	payload, err := batchRejectionPayload(fmt.Errorf("too many ops: max %d", rqlite.MaxBatchOps))
	if err != nil {
		t.Fatalf("batchRejectionPayload: %v", err)
	}

	var res rqlite.BatchResult
	if err := json.Unmarshal(payload, &res); err != nil {
		t.Fatalf("decode as BatchResult: %v", err)
	}
	if res.Committed {
		t.Error("a rejection must never report committed=true")
	}
	if res.Error == "" {
		t.Error("envelope must carry the human-readable reason")
	}
	if res.Code != rqlite.BatchCodeTooManyStatements {
		t.Errorf("code = %q, want %q", res.Code, rqlite.BatchCodeTooManyStatements)
	}
	if res.Results == nil {
		t.Error("results must be an empty array, not null — guests iterate it without a nil check")
	}
}

// db_query_batch and exec_and_publish declare their own success structs. The
// shared envelope must land in both without a decode error, or the fix would
// swap one unreadable signal for another.
func TestBatchRejectionPayload_decodesIntoEveryCallerShape(t *testing.T) {
	payload, err := batchRejectionPayload(errors.New("dial tcp 10.0.0.2:5001: connect: connection refused"))
	if err != nil {
		t.Fatalf("batchRejectionPayload: %v", err)
	}

	t.Run("db_query_batch shape", func(t *testing.T) {
		var res struct {
			Results       []rqlite.OpResult `json:"results"`
			StaleRejected bool              `json:"stale_rejected"`
			Error         string            `json:"error"`
			Code          string            `json:"code"`
		}
		if err := json.Unmarshal(payload, &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if res.Error == "" || res.Code != rqlite.BatchCodeUnavailable {
			t.Errorf("error=%q code=%q, want a populated error and %q", res.Error, res.Code, rqlite.BatchCodeUnavailable)
		}
	})

	t.Run("exec_and_publish shape", func(t *testing.T) {
		var res struct {
			Results   []rqlite.OpResult `json:"results"`
			Committed bool              `json:"committed"`
			Seq       int64             `json:"seq"`
			Published bool              `json:"published"`
			Error     string            `json:"error"`
			Code      string            `json:"code"`
		}
		if err := json.Unmarshal(payload, &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if res.Committed || res.Published {
			t.Error("a rejection must not look like a committed, published write")
		}
		if res.Code != rqlite.BatchCodeUnavailable {
			t.Errorf("code = %q, want %q", res.Code, rqlite.BatchCodeUnavailable)
		}
	})
}

// Classification must survive the context sentinel being wrapped on its way up
// through the host function, which is how a real deadline arrives.
func TestBatchRejectionPayload_classifiesWrappedDeadline(t *testing.T) {
	payload, err := batchRejectionPayload(fmt.Errorf("db_transaction: %w", context.DeadlineExceeded))
	if err != nil {
		t.Fatalf("batchRejectionPayload: %v", err)
	}
	var res rqlite.BatchResult
	if err := json.Unmarshal(payload, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Code != rqlite.BatchCodeDeadlineExceeded {
		t.Errorf("code = %q, want %q — a caller must be able to tell a timeout from a limit violation", res.Code, rqlite.BatchCodeDeadlineExceeded)
	}
}
