package tlsstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	holderA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	holderB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestTryLock_oneHolderAtATime(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	ok, err := s.TryLock(ctx, "issue_cert_a", holderA, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first TryLock = %v, %v", ok, err)
	}
	ok, err = s.TryLock(ctx, "issue_cert_a", holderB, time.Minute)
	if err != nil || ok {
		t.Fatalf("second holder took a held lock: %v, %v", ok, err)
	}
	// Another name is another lock.
	if ok, err := s.TryLock(ctx, "issue_cert_b", holderB, time.Minute); err != nil || !ok {
		t.Fatalf("TryLock of another name = %v, %v", ok, err)
	}
}

// A holder that stopped renewing loses the lock when its lease runs out.
func TestTryLock_takesAnExpiredLease(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	s.now = func() time.Time { return now }
	if ok, _ := s.TryLock(ctx, "l", holderA, time.Minute); !ok {
		t.Fatal("first TryLock failed")
	}
	now = now.Add(61 * time.Second)
	ok, err := s.TryLock(ctx, "l", holderB, time.Minute)
	if err != nil || !ok {
		t.Fatalf("an expired lease was not taken over: %v, %v", ok, err)
	}
	if err := s.Renew(ctx, "l", holderA, time.Minute); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("the old holder renewed a lock it lost: %v", err)
	}
}

func TestRenew_keepsTheLockPastItsFirstLease(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	s.now = func() time.Time { return now }
	s.TryLock(ctx, "l", holderA, time.Minute)
	now = now.Add(50 * time.Second)
	if err := s.Renew(ctx, "l", holderA, time.Minute); err != nil {
		t.Fatal(err)
	}
	now = now.Add(50 * time.Second)
	if ok, _ := s.TryLock(ctx, "l", holderB, time.Minute); ok {
		t.Fatal("a renewed lock was taken as expired")
	}
}

func TestUnlock_onlyByItsHolder(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.TryLock(ctx, "l", holderA, time.Minute)
	if err := s.Unlock(ctx, "l", holderB); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("another holder released the lock: %v", err)
	}
	if err := s.Unlock(ctx, "l", holderA); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.TryLock(ctx, "l", holderB, time.Minute); !ok {
		t.Fatal("a released lock could not be taken")
	}
	if err := s.Unlock(ctx, "never", holderA); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("Unlock of a lock never taken: %v", err)
	}
}

func TestLease_bounds(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, lease := range []time.Duration{0, -time.Second, MaxLease + time.Second} {
		if _, err := s.TryLock(ctx, "l", holderA, lease); err == nil {
			t.Errorf("TryLock accepted lease %s", lease)
		}
		if err := s.Renew(ctx, "l", holderA, lease); err == nil || errors.Is(err, ErrLockNotHeld) {
			t.Errorf("Renew accepted lease %s: %v", lease, err)
		}
	}
}
