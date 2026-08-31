package rqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Bugboard #175. A batched database call that fails for a reason belonging to
// no single statement must be classifiable by the guest, so it can tell a
// deterministic limit violation (fix the caller) from a transient one (retry).
func TestClassifyBatchError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil is unclassified", nil, ""},
		{"deadline sentinel", context.DeadlineExceeded, BatchCodeDeadlineExceeded},
		{"wrapped deadline sentinel", fmt.Errorf("batch: %w", context.DeadlineExceeded), BatchCodeDeadlineExceeded},
		{"cancelled", context.Canceled, BatchCodeDeadlineExceeded},
		{"over the statement cap", fmt.Errorf("too many ops: max %d", MaxBatchOps), BatchCodeTooManyStatements},
		{"rqlite-side statement cap", errors.New("rqlite.Batch: too many ops (105 > max 100)"), BatchCodeTooManyStatements},
		{"result byte cap", errors.New("batch query result exceeds 33554432 bytes"), BatchCodePayloadTooLarge},
		{"row cap", errors.New("op 2 returned too many rows"), BatchCodePayloadTooLarge},
		{"timeout wording", errors.New("Post \"http://leader:5001\": context deadline exceeded"), BatchCodeDeadlineExceeded},
		{"leader gone", errors.New("rqlite.Batch: no leader"), BatchCodeUnavailable},
		{"connection refused", errors.New("dial tcp 10.0.0.2:5001: connect: connection refused"), BatchCodeUnavailable},
		{"no native conn", errors.New("rqlite.Batch: native gorqlite connection not configured"), BatchCodeUnavailable},
		{"malformed input", errors.New("invalid json: unexpected end of JSON input"), BatchCodeInvalidArgument},
		{"unknown op kind", errors.New("op 3 has unknown kind \"upsert\""), BatchCodeInvalidArgument},
		{"unrecognised is never guessed", errors.New("something nobody anticipated"), BatchCodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyBatchError(tc.err); got != tc.want {
				t.Errorf("ClassifyBatchError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// The 100-statement cap is the limit that bit in production: a write whose size
// scales with group fan-out crossed it and failed deterministically at 101.
// Pin it so a change is a deliberate, documented decision — docs/SERVERLESS.md
// publishes this number to tenants.
func TestMaxBatchOps_isThePublishedLimit(t *testing.T) {
	if MaxBatchOps != 100 {
		t.Errorf("MaxBatchOps = %d, want 100 (docs/SERVERLESS.md publishes this to tenants)", MaxBatchOps)
	}
}

// Error and Code must always be set together: a caller that branches on Code
// and logs Error would otherwise get one without the other.
func TestSetBatchError_setsBothOrNeither(t *testing.T) {
	var r BatchResult
	r.setBatchError(nil)
	if r.Error != "" || r.Code != "" {
		t.Errorf("nil error must set nothing, got error=%q code=%q", r.Error, r.Code)
	}

	r.setBatchError(fmt.Errorf("rqlite.Batch: %w", context.DeadlineExceeded))
	if r.Error == "" {
		t.Error("Error must carry the human-readable detail")
	}
	if r.Code != BatchCodeDeadlineExceeded {
		t.Errorf("Code = %q, want %q", r.Code, BatchCodeDeadlineExceeded)
	}
}
