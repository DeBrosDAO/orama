package tlsstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// ErrLockNotHeld is returned when a renewal or release names a lock its caller
// does not hold: it expired and another node took it, or it was never taken.
var ErrLockNotHeld = errors.New("lock not held by this holder")

// TryLock takes the lock name for holder for lease, unless another holder's
// lease on it has not run out. It reports whether holder now holds it.
//
// The write goes through Raft, so two nodes racing for a free lock are ordered
// and only one of them changes the row. A lease that ran out is taken over:
// its holder stopped renewing it, which a live one does well before expiry.
func (s *Store) TryLock(ctx context.Context, name, holder string, lease time.Duration) (bool, error) {
	if err := checkLease(lease); err != nil {
		return false, err
	}
	now := s.now()
	res, err := rqlite.SafeExecContext(s.db, ctx,
		`INSERT INTO tls_locks (name, holder, expires_unix_ms) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET holder = excluded.holder, expires_unix_ms = excluded.expires_unix_ms
		 WHERE tls_locks.expires_unix_ms < ?`,
		name, holder, now.Add(lease).UnixMilli(), now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("take TLS store lock %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("take TLS store lock %s: %w", name, err)
	}
	return n == 1, nil
}

// Renew extends holder's lease on name to lease from now.
func (s *Store) Renew(ctx context.Context, name, holder string, lease time.Duration) error {
	if err := checkLease(lease); err != nil {
		return err
	}
	res, err := rqlite.SafeExecContext(s.db, ctx,
		`UPDATE tls_locks SET expires_unix_ms = ? WHERE name = ? AND holder = ?`,
		s.now().Add(lease).UnixMilli(), name, holder)
	if err != nil {
		return fmt.Errorf("renew TLS store lock %s: %w", name, err)
	}
	return requireOneRow(res, name)
}

// Unlock releases holder's lock on name.
func (s *Store) Unlock(ctx context.Context, name, holder string) error {
	res, err := rqlite.SafeExecContext(s.db, ctx,
		`DELETE FROM tls_locks WHERE name = ? AND holder = ?`, name, holder)
	if err != nil {
		return fmt.Errorf("release TLS store lock %s: %w", name, err)
	}
	return requireOneRow(res, name)
}

func requireOneRow(res interface{ RowsAffected() (int64, error) }, name string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("TLS store lock %s: %w", name, err)
	}
	if n != 1 {
		return fmt.Errorf("TLS store lock %s: %w", name, ErrLockNotHeld)
	}
	return nil
}

func checkLease(lease time.Duration) error {
	if lease <= 0 || lease > MaxLease {
		return fmt.Errorf("lease %s is outside (0, %s]", lease, MaxLease)
	}
	return nil
}
