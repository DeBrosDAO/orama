package rqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// flakyDriver answers the readiness probe with an error until failCount
// attempts have been made, then succeeds — modelling an rqlite that comes up
// without a leader and elects one shortly after.
type flakyDriver struct {
	failCount int32
	attempts  atomic.Int32
	failWith  error
	panicOnce atomic.Bool
	panics    bool
}

func (d *flakyDriver) Open(string) (driver.Conn, error) { return &flakyConn{d: d}, nil }

type flakyConn struct{ d *flakyDriver }

func (c *flakyConn) Prepare(string) (driver.Stmt, error) { return &flakyStmt{d: c.d}, nil }
func (c *flakyConn) Close() error                        { return nil }
func (c *flakyConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }

type flakyStmt struct{ d *flakyDriver }

func (s *flakyStmt) Close() error  { return nil }
func (s *flakyStmt) NumInput() int { return 0 }
func (s *flakyStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("not used")
}

func (s *flakyStmt) Query([]driver.Value) (driver.Rows, error) {
	n := s.d.attempts.Add(1)
	if s.d.panics && s.d.panicOnce.CompareAndSwap(false, true) {
		// Reproduces the gorqlite defect: index a result slice that is empty.
		var empty []int
		_ = empty[0]
	}
	if n <= s.d.failCount {
		return nil, s.d.failWith
	}
	return &emptyRows{}, nil
}

type emptyRows struct{}

func (r *emptyRows) Columns() []string              { return []string{"1"} }
func (r *emptyRows) Close() error                   { return nil }
func (r *emptyRows) Next(dest []driver.Value) error { return io.EOF }

func registerFlaky(t *testing.T, d *flakyDriver) *sql.DB {
	t.Helper()
	name := "flaky-" + t.Name()
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWaitForLeader_returnsOnceLeaderElected(t *testing.T) {
	d := &flakyDriver{failCount: 3, failWith: errors.New("leader address: leader not found")}
	db := registerFlaky(t, d)

	start := time.Now()
	if err := WaitForLeader(context.Background(), db, 10*time.Second); err != nil {
		t.Fatalf("WaitForLeader: %v", err)
	}
	if got := d.attempts.Load(); got < 4 {
		t.Errorf("attempts = %d; want at least 4 (3 failures then success)", got)
	}
	if elapsed := time.Since(start); elapsed < readinessPollInterval {
		t.Errorf("returned in %s without backing off between probes", elapsed)
	}
}

func TestWaitForLeader_succeedsImmediatelyWhenReady(t *testing.T) {
	d := &flakyDriver{failCount: 0}
	db := registerFlaky(t, d)

	start := time.Now()
	if err := WaitForLeader(context.Background(), db, 5*time.Second); err != nil {
		t.Fatalf("WaitForLeader: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a ready database took %s; startup must not pay a fixed delay", elapsed)
	}
}

func TestWaitForLeader_timesOutWithActionableError(t *testing.T) {
	d := &flakyDriver{failCount: 1 << 30, failWith: errors.New("leader address: leader not found")}
	db := registerFlaky(t, d)

	err := WaitForLeader(context.Background(), db, 1200*time.Millisecond)
	if err == nil {
		t.Fatal("want a timeout error when no leader ever appears")
	}
	for _, want := range []string{"no leader", "WireGuard", "leader not found"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so an operator knows where to look", err, want)
		}
	}
}

func TestWaitForLeader_honoursContextCancellation(t *testing.T) {
	d := &flakyDriver{failCount: 1 << 30, failWith: errors.New("leader address: leader not found")}
	db := registerFlaky(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if err := WaitForLeader(ctx, db, time.Hour); err == nil {
		t.Fatal("want an error when the context is cancelled")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %s to take effect", elapsed)
	}
}

func TestWaitForLeader_nilDB(t *testing.T) {
	if err := WaitForLeader(context.Background(), nil, time.Second); err == nil {
		t.Fatal("want an error for a nil handle")
	}
}

// The probe must survive the gorqlite panic rather than taking the process
// down — that panic at boot is what turned a slow database into a crash-loop.
func TestWaitForLeader_survivesDriverPanic(t *testing.T) {
	d := &flakyDriver{failCount: 0, panics: true}
	db := registerFlaky(t, d)

	if err := WaitForLeader(context.Background(), db, 5*time.Second); err != nil {
		t.Fatalf("WaitForLeader should recover the driver panic and retry, got: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
