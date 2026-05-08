package triggers

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// markRunMockDB is a minimal rqlite.Client surface for MarkRun tests.
// Only Exec is exercised — anything else panics so accidental drift is loud.
type markRunMockDB struct {
	rqlite.Client // embedded interface — calling any unimplemented method panics

	lastQuery string
	lastArgs  []any
	rows      int64
	execErr   error
}

func (m *markRunMockDB) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	m.lastQuery = query
	m.lastArgs = args
	if m.execErr != nil {
		return nil, m.execErr
	}
	return &fakeResult{rows: m.rows}, nil
}

type fakeResult struct {
	rows int64
}

func (f *fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f *fakeResult) RowsAffected() (int64, error) { return f.rows, nil }

// TestMarkRun_compareAndSwapMisses guards bug #65 audit B6: when two gateways
// race the same tick, only the first MarkRun wins and the second must return
// ErrAlreadyRan so the caller can skip the duplicate invoke.
func TestMarkRun_compareAndSwapMisses(t *testing.T) {
	db := &markRunMockDB{rows: 0} // simulate "no row matched the WHERE"
	store := NewCronTriggerStore(db, zap.NewNop())

	expected := time.Date(2025, 5, 7, 12, 0, 0, 0, time.UTC)
	err := store.MarkRun(context.Background(),
		"trigger-id", expected, expected.Add(time.Second),
		expected.Add(time.Minute), "ok", "")
	if !errors.Is(err, ErrAlreadyRan) {
		t.Fatalf("MarkRun = %v, want ErrAlreadyRan", err)
	}
}

// TestMarkRun_compareAndSwapWins covers the happy path: 1 row affected, no
// error returned. This pairs with the misses-test to fully cover the lease
// branch.
func TestMarkRun_compareAndSwapWins(t *testing.T) {
	db := &markRunMockDB{rows: 1}
	store := NewCronTriggerStore(db, zap.NewNop())

	expected := time.Date(2025, 5, 7, 12, 0, 0, 0, time.UTC)
	err := store.MarkRun(context.Background(),
		"trigger-id", expected, expected.Add(time.Second),
		expected.Add(time.Minute), "ok", "")
	if err != nil {
		t.Fatalf("MarkRun (winning CAS) = %v, want nil", err)
	}
}

// TestMarkRun_dbErrorPropagates ensures a real database failure is wrapped
// and returned, NOT collapsed into ErrAlreadyRan.
func TestMarkRun_dbErrorPropagates(t *testing.T) {
	dbErr := errors.New("rqlite unavailable")
	db := &markRunMockDB{execErr: dbErr}
	store := NewCronTriggerStore(db, zap.NewNop())

	err := store.MarkRun(context.Background(),
		"trigger-id", time.Now(), time.Now(), time.Now(), "ok", "")
	if err == nil {
		t.Fatal("MarkRun with DB error returned nil")
	}
	if errors.Is(err, ErrAlreadyRan) {
		t.Fatal("DB error should not surface as ErrAlreadyRan")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("error chain missing wrapped DB error: %v", err)
	}
}

// TestMarkRun_compareAndSwapClauseInQuery verifies the SQL actually contains
// the `next_run_at = ?` predicate — without it the lease semantics are
// silently broken even though tests with a 1-row mock would still "pass".
func TestMarkRun_compareAndSwapClauseInQuery(t *testing.T) {
	db := &markRunMockDB{rows: 1}
	store := NewCronTriggerStore(db, zap.NewNop())

	_ = store.MarkRun(context.Background(), "id",
		time.Now(), time.Now(), time.Now(), "ok", "")

	if db.lastQuery == "" {
		t.Fatal("Exec was not called")
	}
	// The exact SQL is whitespace-formatted; just check the lease clause is
	// present.
	if !contains(db.lastQuery, "next_run_at = ?") {
		t.Errorf("MarkRun SQL missing CAS clause:\n%s", db.lastQuery)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
