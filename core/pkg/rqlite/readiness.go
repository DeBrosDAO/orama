package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	// readinessPollInterval is how often WaitForLeader re-probes. Short enough
	// that a normal boot (leader already elected) proceeds immediately, long
	// enough not to hammer a node that is still replaying its raft log.
	readinessPollInterval = 500 * time.Millisecond

	// readinessProbe is the cheapest statement that still proves the node can
	// serve a leader-routed read: it needs a leader, but touches no table, so
	// it works before any migration has run.
	readinessProbe = "SELECT 1"
)

// WaitForLeader blocks until the rqlite behind db can serve a leader-routed
// read, or until timeout elapses.
//
// Why this exists: opening a database/sql handle to rqlite establishes nothing
// — sql.Open is lazy, and rqlite accepts connections long before it has
// elected a leader. Schema migrations issued into that window fail with
// "leader not found", and (before SafeExecContext was applied to the migration
// path) crashed the process outright. systemd's After=/Requires= does not close
// the gap: for Type=simple units it only orders process *start*, which says
// nothing about whether the database is usable.
//
// So the ordering is enforced here, against the real readiness signal, rather
// than assumed from unit ordering or slept on.
//
// A nil db is a programming error and returns immediately.
func WaitForLeader(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	if db == nil {
		return fmt.Errorf("rqlite.WaitForLeader: nil database handle")
	}

	deadline := time.Now().Add(timeout)
	attempts := 0
	var lastErr error

	for {
		attempts++
		// A per-probe budget keeps one hung request from eating the whole
		// window; the loop retries until the outer deadline instead.
		probeCtx, cancel := context.WithTimeout(ctx, readinessPollInterval*4)
		rows, err := SafeQueryContext(db, probeCtx, readinessProbe)
		if err == nil {
			rows.Close()
			cancel()
			return nil
		}
		cancel()
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("rqlite.WaitForLeader: cancelled after %d attempts (last error: %v): %w", attempts, lastErr, ctxErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rqlite.WaitForLeader: no leader after %s and %d attempts — "+
				"check that the local rqlited is running and that this node can reach its raft peers "+
				"over the WireGuard mesh (last error: %w)", timeout, attempts, lastErr)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("rqlite.WaitForLeader: cancelled after %d attempts (last error: %v): %w", attempts, lastErr, ctx.Err())
		case <-time.After(readinessPollInterval):
		}
	}
}
