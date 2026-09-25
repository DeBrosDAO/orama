package rqlite

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/rqlite/gorqlite"
)

// Batch-level error codes (bugboard #175).
//
// A batched database call can fail for reasons that belong to no single
// statement: the request never left the gateway, the leader went away
// mid-commit, the deadline expired. Before these codes existed such a failure
// reached the guest as an empty buffer — literally no information — and a
// namespace outage had to be diagnosed by deploying probe functions and
// hand-counting statements, because the only record of the reason lived in the
// gateway's journal, which tenants cannot read.
//
// The code is a stable, machine-readable classification. The accompanying
// BatchResult.Error keeps the human-readable detail; callers branch on the code
// and log the message.
const (
	// BatchCodeTooManyStatements — the batch exceeded MaxBatchOps. Deterministic:
	// it fails at the same size every time, under any load. Split the work.
	BatchCodeTooManyStatements = "TOO_MANY_STATEMENTS"

	// BatchCodePayloadTooLarge — the batch's results exceeded the response caps
	// (MaxBatchQueryRowsPerOp / MaxBatchQueryTotalBytes). Paginate.
	BatchCodePayloadTooLarge = "PAYLOAD_TOO_LARGE"

	// BatchCodeDeadlineExceeded — the call ran past its deadline. Transient;
	// retrying a smaller batch, or the same batch later, may succeed.
	BatchCodeDeadlineExceeded = "DEADLINE_EXCEEDED"

	// BatchCodeUnavailable — the database could not be reached or lost its
	// leader mid-call. Transient.
	BatchCodeUnavailable = "UNAVAILABLE"

	// BatchCodeConstraintViolation — a statement violated a UNIQUE, PRIMARY
	// KEY, NOT NULL, CHECK or FOREIGN KEY constraint. Deterministic: the same
	// write fails the same way every time, so retrying it cannot succeed. For
	// an INSERT guarded by a unique key this is how "already recorded" reads.
	BatchCodeConstraintViolation = "CONSTRAINT_VIOLATION"

	// BatchCodeInvalidArgument — the request itself was malformed: unparseable
	// JSON, an unknown op kind, a missing required field, an illegal
	// consistency/freshness combination. Deterministic; fix the caller.
	BatchCodeInvalidArgument = "INVALID_ARGUMENT"

	// BatchCodeInternal — a failure that fits none of the above. Deliberately
	// last: a caller seeing this should read Error, not guess.
	BatchCodeInternal = "INTERNAL"
)

// ClassifyBatchError maps a failure to one of the codes above. It classifies
// both batch-level failures and the error of a single op (OpResult.Code).
//
// A statement error — one SQLite reported for a statement it ran, as opposed
// to a failure to reach or use the database — is recognised by its type, never
// by its text, and only ever gets a statement code. Its text contains the
// statement's own identifiers: matched against the transport patterns below,
// "no such column: timeout" would read as a deadline and "no such table:
// geofences" as an EOF, and the caller would retry a statement that can never
// succeed.
//
// Matching is by sentinel error first and message substring second. The
// substring arm exists because the failures worth distinguishing here come from
// rqlite, gorqlite and net/http, none of which expose typed errors for them;
// each pattern below is anchored on wording those libraries actually emit.
// Anything unrecognised is BatchCodeInternal rather than a guess — a wrong code
// is worse than an honest "unclassified", because callers branch on it.
func ClassifyBatchError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return BatchCodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		// A cancelled call is the caller's own deadline or an aborted request;
		// from the guest's side it is indistinguishable from a timeout and the
		// action is the same.
		return BatchCodeDeadlineExceeded
	case errors.Is(err, ErrFreshnessViolation):
		return BatchCodeUnavailable
	}

	if isStatementError(err) {
		return classifyStatementError(err)
	}
	if errors.Is(err, ErrNoNativeConnection) {
		// A gateway built without the connection stays that way; retrying
		// cannot help.
		return BatchCodeInternal
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "too many ops"):
		return BatchCodeTooManyStatements
	case strings.Contains(msg, "exceeds") && strings.Contains(msg, "bytes"),
		strings.Contains(msg, "too many rows"),
		strings.Contains(msg, "result too large"):
		return BatchCodePayloadTooLarge
	case strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "timeout"),
		strings.Contains(msg, "timed out"):
		return BatchCodeDeadlineExceeded
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no leader"),
		strings.Contains(msg, "leader not found"),
		strings.Contains(msg, "not configured"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "eof"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "unavailable"):
		return BatchCodeUnavailable
	case strings.Contains(msg, "invalid json"),
		strings.Contains(msg, "unknown kind"),
		strings.Contains(msg, "ops required"),
		strings.Contains(msg, "required"),
		strings.Contains(msg, "unmarshal"):
		return BatchCodeInvalidArgument
	}
	return BatchCodeInternal
}

// StatementError is a failure SQLite reported for one statement it ran — a
// constraint, a missing table, a syntax error — as opposed to a failure to
// reach or use the database. rqlite reports these per statement as plain
// text; wrapping them in this type is what lets ClassifyBatchError tell them
// from a transport fault.
type StatementError struct {
	Err error
}

func (e *StatementError) Error() string { return e.Err.Error() }

func (e *StatementError) Unwrap() error { return e.Err }

// isStatementError reports whether err is, or wraps, a statement failure:
// ours, or gorqlite's StatementErrors, which is what its database/sql driver
// returns for a statement rqlite rejected.
func isStatementError(err error) bool {
	var ours *StatementError
	if errors.As(err, &ours) {
		return true
	}
	var theirs gorqlite.StatementErrors
	return errors.As(err, &theirs)
}

// classifyStatementError codes a statement failure. SQLite words every
// constraint failure "<KIND> constraint failed: ..." (UNIQUE, PRIMARY KEY,
// NOT NULL, CHECK, FOREIGN KEY). Every other statement failure has no
// dedicated code.
func classifyStatementError(err error) string {
	if constraintFailure.MatchString(err.Error()) {
		return BatchCodeConstraintViolation
	}
	return BatchCodeInternal
}

// constraintFailure is SQLite's wording for each constraint kind. Anchored on
// the kind so a quoted identifier that merely contains "constraint failed" is
// not read as one.
var constraintFailure = regexp.MustCompile(`\b(UNIQUE|PRIMARY KEY|NOT NULL|CHECK|FOREIGN KEY) constraint failed\b`)

// ErrNoNativeConnection is returned by the batch APIs on a client built
// without a native gorqlite connection (NewClient rather than
// NewClientWithDSN). A configuration fault, not a transient one.
var ErrNoNativeConnection = errors.New("native gorqlite connection not configured (use NewClientWithDSN or NewClientWithConn)")
