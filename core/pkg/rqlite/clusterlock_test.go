package rqlite

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func lockDB(t *testing.T) *sql.DB {
	t.Helper()
	// A shared-cache in-memory database, so the concurrent goroutines below
	// contend on one database rather than each getting their own.
	db, err := sql.Open("sqlite3", "file:locktest?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := EnsureClusterLocksTable(context.Background(), db); err != nil {
		t.Fatalf("EnsureClusterLocksTable: %v", err)
	}

	prev := lockPollInterval
	lockPollInterval = time.Millisecond
	t.Cleanup(func() { lockPollInterval = prev })
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM cluster_locks`) })
	return db
}

func TestClusterLock_onlyOneHolderAtATime(t *testing.T) {
	// The property the whole thing exists for. Without it, N gateways starting
	// together all replay the same non-idempotent migrations.
	db := lockDB(t)

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var acquired atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			lock, err := AcquireClusterLock(context.Background(), db, "migrations",
				string(rune('a'+n)), time.Minute, 10*time.Second)
			if err != nil {
				return
			}
			acquired.Add(1)

			now := concurrent.Add(1)
			for {
				peak := maxConcurrent.Load()
				if now <= peak || maxConcurrent.CompareAndSwap(peak, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			concurrent.Add(-1)

			if err := lock.Release(context.Background()); err != nil {
				t.Errorf("Release: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("%d holders ran at once; the lock is not mutually exclusive", got)
	}
	// Every one of them must get a turn. Mutual exclusion that starves callers
	// would pass the check above and still leave a node unable to migrate.
	if got := acquired.Load(); got != 8 {
		t.Fatalf("%d of 8 callers acquired the lock; the rest were starved", got)
	}
}

func TestClusterLock_heldLockBlocksAndTimesOut(t *testing.T) {
	db := lockDB(t)

	held, err := AcquireClusterLock(context.Background(), db, "migrations", "first", time.Minute, time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	_, err = AcquireClusterLock(context.Background(), db, "migrations", "second", time.Minute, 100*time.Millisecond)
	if err == nil {
		t.Fatal("a second holder took a lock that was already held")
	}

	if err := held.Release(context.Background()); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Freed, so it can be taken.
	after, err := AcquireClusterLock(context.Background(), db, "migrations", "second", time.Minute, time.Second)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = after.Release(context.Background())
}

func TestClusterLock_expiredLockIsReclaimed(t *testing.T) {
	// A holder that dies mid-migration must not block every future start. The
	// TTL is a liveness property, not an optimisation.
	db := lockDB(t)

	if _, err := AcquireClusterLock(context.Background(), db, "migrations", "dead-node", time.Millisecond, time.Second); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	lock, err := AcquireClusterLock(context.Background(), db, "migrations", "live-node", time.Minute, time.Second)
	if err != nil {
		t.Fatalf("an expired lock was not reclaimed: %v", err)
	}
	_ = lock.Release(context.Background())
}

func TestClusterLock_releaseDoesNotStealFromTheNextHolder(t *testing.T) {
	// A holder whose TTL expired may find the lock already belongs to someone
	// else. Releasing it then would pull it out from under them.
	db := lockDB(t)

	stale, err := AcquireClusterLock(context.Background(), db, "migrations", "stale", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	next, err := AcquireClusterLock(context.Background(), db, "migrations", "next", time.Minute, time.Second)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	if err := stale.Release(context.Background()); err != nil {
		t.Fatalf("stale release: %v", err)
	}

	// The current holder must still hold it.
	holder, err := clusterLockHolder(context.Background(), db, "migrations")
	if err != nil {
		t.Fatalf("read holder: %v", err)
	}
	if holder != "next" {
		t.Fatalf("holder is %q; the stale holder's release stole the lock", holder)
	}
	_ = next.Release(context.Background())
}

func TestClusterLock_requiresATTL(t *testing.T) {
	// Without one, a holder that dies blocks the lock for ever.
	db := lockDB(t)
	if _, err := AcquireClusterLock(context.Background(), db, "migrations", "x", 0, time.Second); err == nil {
		t.Fatal("a zero TTL was accepted")
	}
}

func TestSecondsFromNow(t *testing.T) {
	// Duration.String() gives "1m0s", which SQLite's datetime() does not
	// understand — it returns NULL rather than erroring, so an expiry built
	// that way is permanently null and the lock never expires.
	if got := secondsFromNow(90 * time.Second); got != "+90 seconds" {
		t.Fatalf("got %q", got)
	}
	if got := secondsFromNow(time.Minute); got != "+60 seconds" {
		t.Fatalf("got %q", got)
	}
}
