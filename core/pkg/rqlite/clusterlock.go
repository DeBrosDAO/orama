package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Cluster-wide mutual exclusion, held through raft.
//
// rqlite serialises every write through raft, so a conditional UPDATE is a
// linearizable compare-and-swap — which makes it a correct mutex across nodes
// that share nothing else. There is no other coordination primitive available
// here, and the alternative (each node assuming it is alone) is what this
// exists to stop.
//
// The lock is advisory and TTL-bounded. A holder that dies mid-work must not
// block every future attempt for ever, so the TTL is a liveness property, not
// an optimisation — but it does mean a caller has to finish inside it. Pick a
// TTL comfortably longer than the work, and treat losing the lock the way you
// would treat any other failure to finish.

// ClusterLock is a held lock. Release it.
type ClusterLock struct {
	db     *sql.DB
	name   string
	holder string
}

// lockPollInterval is how often AcquireClusterLock retries while another holder
// has it. A var so tests can drive contention without waiting out real polls.
var lockPollInterval = 2 * time.Second

// EnsureClusterLocksTable creates the lock table if it is absent.
//
// The table is declared by a migration, but the migration runner needs the lock
// BEFORE it can run migrations — so, like schema_migrations, it has to be able
// to bootstrap itself. The statement is idempotent DDL, which is safe for two
// nodes to issue at once.
func EnsureClusterLocksTable(ctx context.Context, db *sql.DB) error {
	_, err := SafeExecContext(db, ctx, `
CREATE TABLE IF NOT EXISTS cluster_locks (
	name        TEXT PRIMARY KEY,
	holder      TEXT NOT NULL DEFAULT '',
	acquired_at TIMESTAMP,
	expires_at  TIMESTAMP
)`)
	if err != nil {
		return fmt.Errorf("ensure cluster_locks: %w", err)
	}
	return nil
}

// AcquireClusterLock blocks until the named lock is held, ctx is done, or wait
// elapses.
//
// holder identifies who took it, for diagnosis only — correctness does not
// depend on it being unique, though it should be.
func AcquireClusterLock(ctx context.Context, db *sql.DB, name, holder string, ttl, wait time.Duration) (*ClusterLock, error) {
	if db == nil {
		return nil, fmt.Errorf("cluster lock %q: nil database handle", name)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("cluster lock %q: a TTL is required, or a dead holder blocks it for ever", name)
	}

	if err := EnsureClusterLocksTable(ctx, db); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(wait)
	for {
		ok, err := tryAcquireClusterLock(ctx, db, name, holder, ttl)
		if err != nil {
			return nil, err
		}
		if ok {
			return &ClusterLock{db: db, name: name, holder: holder}, nil
		}

		if time.Now().After(deadline) {
			current, _ := clusterLockHolder(ctx, db, name)
			return nil, fmt.Errorf("cluster lock %q is held by %q and did not free within %s", name, current, wait)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cluster lock %q: %w", name, ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}

// tryAcquireClusterLock makes one attempt.
//
// Two statements, in this order. The INSERT creates the row only if it is
// absent; it cannot steal a held lock, because a conflict does nothing. The
// UPDATE then takes it only if it is unheld or expired. Both are conditional,
// so two nodes racing produce exactly one winner: raft applies them in some
// order, and the loser's condition is false by the time it runs.
func tryAcquireClusterLock(ctx context.Context, db *sql.DB, name, holder string, ttl time.Duration) (bool, error) {
	if _, err := SafeExecContext(db, ctx,
		`INSERT OR IGNORE INTO cluster_locks (name, holder, acquired_at, expires_at)
		 VALUES (?, '', NULL, NULL)`, name); err != nil {
		return false, fmt.Errorf("cluster lock %q: ensure row: %w", name, err)
	}

	res, err := SafeExecContext(db, ctx,
		`UPDATE cluster_locks
		    SET holder = ?, acquired_at = CURRENT_TIMESTAMP, expires_at = datetime('now', ?)
		  WHERE name = ?
		    AND (holder = '' OR expires_at IS NULL OR expires_at < CURRENT_TIMESTAMP)`,
		holder, secondsFromNow(ttl), name)
	if err != nil {
		return false, fmt.Errorf("cluster lock %q: acquire: %w", name, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		// Cannot tell whether it was taken. Treating that as success would let
		// two holders run at once, which is the whole thing being prevented.
		return false, fmt.Errorf("cluster lock %q: cannot tell whether it was acquired: %w", name, err)
	}
	return n > 0, nil
}

// Release frees the lock, and only if this holder still has it: a lock whose
// TTL expired may already belong to someone else, and releasing it then would
// pull it out from under them.
func (l *ClusterLock) Release(ctx context.Context) error {
	if l == nil || l.db == nil {
		return nil
	}
	if _, err := SafeExecContext(l.db, ctx,
		`UPDATE cluster_locks SET holder = '', acquired_at = NULL, expires_at = NULL
		  WHERE name = ? AND holder = ?`, l.name, l.holder); err != nil {
		return fmt.Errorf("release cluster lock %q: %w", l.name, err)
	}
	return nil
}

// clusterLockHolder reports who currently holds the lock, for the error message
// when a wait times out.
func clusterLockHolder(ctx context.Context, db *sql.DB, name string) (string, error) {
	rows, err := SafeQueryContext(db, ctx, `SELECT holder FROM cluster_locks WHERE name = ?`, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if rows.Next() {
		var holder string
		if err := rows.Scan(&holder); err != nil {
			return "", err
		}
		return holder, nil
	}
	return "", rows.Err()
}

// secondsFromNow renders a SQLite datetime modifier.
//
// Duration.String() produces "1m0s", which SQLite's datetime() does not
// understand — it returns NULL rather than an error, so an expiry built that
// way would be permanently null and the lock would never expire.
func secondsFromNow(d time.Duration) string {
	return fmt.Sprintf("+%d seconds", int(d.Seconds()))
}
