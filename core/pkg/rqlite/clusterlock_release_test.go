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
