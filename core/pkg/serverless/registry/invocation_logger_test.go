package registry

import (
	"context"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// fakeDB is a tiny rqlite.Client stub that lets tests script Query / Exec
// behavior. We only implement what InvocationLogger calls.
type fakeDB struct {
	rqlite.Client
	queries []recordedQuery
	// onQuery is called for every Query() invocation, in order. Each
	// callback fills in the destination slice and returns an optional
	// error. Tests pop callbacks; running out is a test bug.
	onQuery []func(dest interface{}) error
}

type recordedQuery struct {
	sql  string
	args []interface{}
}

func (f *fakeDB) Query(_ context.Context, dest interface{}, sql string, args ...interface{}) error {
	f.queries = append(f.queries, recordedQuery{sql: sql, args: args})
	if len(f.onQuery) == 0 {
		return nil
	}
	cb := f.onQuery[0]
	f.onQuery = f.onQuery[1:]
	return cb(dest)
}

func TestGetInvocations_no_invocations_returns_empty_slice(t *testing.T) {
	db := &fakeDB{}
	il := NewInvocationLogger(db, zap.NewNop())

	got, err := il.GetInvocations(context.Background(), "ns", "fn", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice; nil breaks JSON marshalling for clients")
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d records", len(got))
	}
}

func TestGetInvocations_populates_records_and_nests_logs(t *testing.T) {
	now := time.Now()
	// Arrange the fake to return two invocations on the first Query, then
	// two log rows on the second.
	db := &fakeDB{
		onQuery: []func(dest interface{}) error{
			// Invocation rows.
			func(dest interface{}) error {
				rows := dest.(*[]struct {
					ID           string    `db:"id"`
					RequestID    string    `db:"request_id"`
					TriggerType  string    `db:"trigger_type"`
					CallerWallet string    `db:"caller_wallet"`
					InputSize    int       `db:"input_size"`
					OutputSize   int       `db:"output_size"`
					StartedAt    time.Time `db:"started_at"`
					CompletedAt  time.Time `db:"completed_at"`
					DurationMS   int64     `db:"duration_ms"`
					Status       string    `db:"status"`
					ErrorMessage string    `db:"error_message"`
					MemoryUsedMB float64   `db:"memory_used_mb"`
				})
				*rows = append(*rows,
					struct {
						ID           string    `db:"id"`
						RequestID    string    `db:"request_id"`
						TriggerType  string    `db:"trigger_type"`
						CallerWallet string    `db:"caller_wallet"`
						InputSize    int       `db:"input_size"`
						OutputSize   int       `db:"output_size"`
						StartedAt    time.Time `db:"started_at"`
						CompletedAt  time.Time `db:"completed_at"`
						DurationMS   int64     `db:"duration_ms"`
						Status       string    `db:"status"`
						ErrorMessage string    `db:"error_message"`
						MemoryUsedMB float64   `db:"memory_used_mb"`
					}{
						ID: "inv-1", RequestID: "req-A", Status: "success",
						DurationMS: 12, StartedAt: now,
					},
					struct {
						ID           string    `db:"id"`
						RequestID    string    `db:"request_id"`
						TriggerType  string    `db:"trigger_type"`
						CallerWallet string    `db:"caller_wallet"`
						InputSize    int       `db:"input_size"`
						OutputSize   int       `db:"output_size"`
						StartedAt    time.Time `db:"started_at"`
						CompletedAt  time.Time `db:"completed_at"`
						DurationMS   int64     `db:"duration_ms"`
						Status       string    `db:"status"`
						ErrorMessage string    `db:"error_message"`
						MemoryUsedMB float64   `db:"memory_used_mb"`
					}{
						ID: "inv-2", RequestID: "req-B", Status: "error",
						ErrorMessage: "boom", StartedAt: now.Add(-1 * time.Minute),
					},
				)
				return nil
			},
			// Nested WASM log rows: one for inv-1, none for inv-2.
			func(dest interface{}) error {
				rows := dest.(*[]struct {
					InvocationID string    `db:"invocation_id"`
					Level        string    `db:"level"`
					Message      string    `db:"message"`
					Timestamp    time.Time `db:"timestamp"`
				})
				*rows = append(*rows, struct {
					InvocationID string    `db:"invocation_id"`
					Level        string    `db:"level"`
					Message      string    `db:"message"`
					Timestamp    time.Time `db:"timestamp"`
				}{
					InvocationID: "inv-1", Level: "info", Message: "hi", Timestamp: now,
				})
				return nil
			},
		},
	}
	il := NewInvocationLogger(db, zap.NewNop())

	got, err := il.GetInvocations(context.Background(), "ns", "fn", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(got))
	}

	if got[0].ID != "inv-1" || got[0].Status != "success" {
		t.Errorf("first invocation wrong: %+v", got[0])
	}
	if len(got[0].WASMLogs) != 1 || got[0].WASMLogs[0].Message != "hi" {
		t.Errorf("expected nested WASM log on inv-1, got %+v", got[0].WASMLogs)
	}
	if got[1].ID != "inv-2" || got[1].ErrorMessage != "boom" {
		t.Errorf("second invocation wrong: %+v", got[1])
	}
	if len(got[1].WASMLogs) != 0 {
		t.Errorf("expected no WASM logs on inv-2, got %+v", got[1].WASMLogs)
	}
}

func TestGetInvocations_log_query_failure_does_not_drop_records(t *testing.T) {
	// Even if the WASM-logs query fails, the invocation summary is still
	// useful — we degrade gracefully (log a warn) rather than failing the
	// whole call.
	db := &fakeDB{
		onQuery: []func(dest interface{}) error{
			// Invocation query: one row.
			func(dest interface{}) error {
				rows := dest.(*[]struct {
					ID           string    `db:"id"`
					RequestID    string    `db:"request_id"`
					TriggerType  string    `db:"trigger_type"`
					CallerWallet string    `db:"caller_wallet"`
					InputSize    int       `db:"input_size"`
					OutputSize   int       `db:"output_size"`
					StartedAt    time.Time `db:"started_at"`
					CompletedAt  time.Time `db:"completed_at"`
					DurationMS   int64     `db:"duration_ms"`
					Status       string    `db:"status"`
					ErrorMessage string    `db:"error_message"`
					MemoryUsedMB float64   `db:"memory_used_mb"`
				})
				*rows = append(*rows, struct {
					ID           string    `db:"id"`
					RequestID    string    `db:"request_id"`
					TriggerType  string    `db:"trigger_type"`
					CallerWallet string    `db:"caller_wallet"`
					InputSize    int       `db:"input_size"`
					OutputSize   int       `db:"output_size"`
					StartedAt    time.Time `db:"started_at"`
					CompletedAt  time.Time `db:"completed_at"`
					DurationMS   int64     `db:"duration_ms"`
					Status       string    `db:"status"`
					ErrorMessage string    `db:"error_message"`
					MemoryUsedMB float64   `db:"memory_used_mb"`
				}{ID: "inv-1", Status: "success"})
				return nil
			},
			// Nested-log query: simulate a transient DB error.
			func(_ interface{}) error {
				return errFake
			},
		},
	}
	il := NewInvocationLogger(db, zap.NewNop())

	got, err := il.GetInvocations(context.Background(), "ns", "fn", 50)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(got))
	}
	if len(got[0].WASMLogs) != 0 {
		t.Errorf("expected empty WASMLogs on log-fetch failure, got %+v", got[0].WASMLogs)
	}
}

// errFake is a sentinel for log-query failure tests.
var errFake = &fakeError{}

type fakeError struct{}

func (e *fakeError) Error() string { return "fake db error" }
