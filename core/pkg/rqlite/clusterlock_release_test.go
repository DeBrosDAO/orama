package rqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A holder that lost its handle, or restarted its own database, frees the lock
// through another one.
func TestReleaseClusterLock_freesALockThroughAnotherHandle(t *testing.T) {
	db := lockDB(t)
	if _, err := AcquireClusterLock(context.Background(), db, "rollout", "n1", time.Hour, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseClusterLock(context.Background(), db, "rollout", "n1"); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireClusterLock(context.Background(), db, "rollout", "n2", time.Hour, time.Millisecond)
	if err != nil {
		t.Fatalf("the lock was not freed: %v", err)
	}
	_ = lock.Release(context.Background())
}

func TestReleaseClusterLock_neverFreesAnotherHoldersLock(t *testing.T) {
	db := lockDB(t)
	if _, err := AcquireClusterLock(context.Background(), db, "rollout", "n2", time.Hour, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseClusterLock(context.Background(), db, "rollout", "n1"); err != nil {
		t.Fatal(err)
	}
	_, err := AcquireClusterLock(context.Background(), db, "rollout", "n3", time.Hour, time.Millisecond)
	if !errors.Is(err, ErrClusterLockHeld) {
		t.Fatalf("n1 freed n2's lock: %v", err)
	}
}

func TestAcquireOwnClusterLock_takesBackItsOwnLeaseAtOnceAndNotAnothers(t *testing.T) {
	db := lockDB(t)
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n1", time.Hour); err != nil {
		t.Fatalf("a lock nobody holds (and a table nobody made): %v", err)
	}
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n1", time.Hour); err != nil {
		t.Fatalf("n1 could not take back its own lease: %v", err)
	}
	_, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n2", time.Hour)
	if !errors.Is(err, ErrClusterLockHeld) {
		t.Fatalf("n2 took n1's lease: %v", err)
	}
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n3", 0); err == nil {
		t.Fatal("a lock with no TTL was taken")
	}
}

func TestAcquireOwnClusterLock_anEmptyHolderIsRefusedAndTakesNothing(t *testing.T) {
	db := lockDB(t)
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "", time.Hour); err == nil {
		t.Fatal("a lock was taken in no one's name")
	}
	// The refusal must not have left the lock looking held, or free for a
	// second empty holder to "take back".
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n1", time.Hour); err != nil {
		t.Fatalf("n1 could not take the lock after an empty holder was refused: %v", err)
	}
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "", time.Hour); err == nil {
		t.Fatal("an empty holder took n1's lease")
	}
	if _, err := AcquireOwnClusterLock(context.Background(), db, "rollout", "n2", time.Hour); !errors.Is(err, ErrClusterLockHeld) {
		t.Fatalf("n2 took n1's lease: %v", err)
	}
}
