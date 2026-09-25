package rqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rqlite/gorqlite"
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
		{"no native conn is a configuration fault", fmt.Errorf("rqlite.Batch: %w", ErrNoNativeConnection), BatchCodeInternal},
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

// bugboard #267. A statement error is recognised by type and only ever gets a
// statement code; its text is SQLite's, and carries the statement's own
// identifiers, so it must never be matched against the transport patterns.
func TestClassifyBatchError_statementErrors(t *testing.T) {
	stmt := func(msg string) error { return &StatementError{Err: errors.New(msg)} }
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unique", stmt("UNIQUE constraint failed: subscriptions.payment_tx_signature"), BatchCodeConstraintViolation},
		{"not null", stmt("NOT NULL constraint failed: t.email"), BatchCodeConstraintViolation},
		{"check", stmt("CHECK constraint failed: n > 0"), BatchCodeConstraintViolation},
		{"foreign key", stmt("FOREIGN KEY constraint failed"), BatchCodeConstraintViolation},
		{"wrapped by Batch", fmt.Errorf("rqlite.Batch: exec failed at op 0: %w", stmt("UNIQUE constraint failed: t.email")), BatchCodeConstraintViolation},
		{"gorqlite's own type", gorqlite.StatementErrors{errors.New("UNIQUE constraint failed: t.id")}, BatchCodeConstraintViolation},
		{"missing table", stmt("no such table: nosuch"), BatchCodeInternal},
		{"column named timeout is not a deadline", stmt("no such column: timeout"), BatchCodeInternal},
		{"table containing eof is not a transport fault", stmt("no such table: geofences"), BatchCodeInternal},
		{"column named unavailable_since", gorqlite.StatementErrors{errors.New("no such column: unavailable_since")}, BatchCodeInternal},
		{"untyped constraint text is not trusted", errors.New("UNIQUE constraint failed: t.email"), BatchCodeInternal},
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
