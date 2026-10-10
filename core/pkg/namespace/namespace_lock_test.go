package namespace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mustLockNamespace takes namespace's lock for a test, failing it when the lock
// is not free within NamespaceLockWaitTimeout.
func mustLockNamespace(t *testing.T, s *SystemdSpawner, namespace string) func() {
	t.Helper()
	unlock, err := s.LockNamespace(context.Background(), namespace)
	if err != nil {
		t.Errorf("lock namespace %s: %v", namespace, err)
		return func() {}
	}
	return unlock
}

// A waiter whose context ends leaves the queue with the context's error; the
// plain mutex it replaces held it for as long as the holder lived.
func TestLockNamespace_cancellationUnblocksAWaiter(t *testing.T) {
	s := &SystemdSpawner{}
	unlock := mustLockNamespace(t, s, "acme")
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.LockNamespace(ctx, "acme")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the lock was granted while it was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v; want context.Canceled wrapped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not unblock the waiter")
	}
}

// A holder that never lets go (a wedged systemctl call) costs its waiters their
// deadline, not the namespace for ever.
func TestLockNamespace_aWedgedHolderCostsWaitersTheirDeadline(t *testing.T) {
	s := &SystemdSpawner{}
	unlock := mustLockNamespace(t, s, "acme")
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	release, err := s.LockNamespace(ctx, "acme")
	if release != nil {
		t.Fatal("got the lock while it was held")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v; want context.DeadlineExceeded wrapped", err)
	}
}

// A waiter that gave up must not have taken the lock with it: the holder's
// release hands it to the next caller.
func TestLockNamespace_aWaiterThatLeftDoesNotHoldTheLock(t *testing.T) {
	s := &SystemdSpawner{}
	unlock := mustLockNamespace(t, s, "acme")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.LockNamespace(ctx, "acme"); err == nil {
		t.Fatal("the lock was granted while it was held")
	}
	unlock()

	got := make(chan struct{})
	go func() { mustLockNamespace(t, s, "acme")(); close(got) }()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not free after the holder released it")
	}
}

// The table holds an entry only while somebody holds or waits for the lock, so
// it does not grow with every namespace the node has hosted.
func TestLockNamespace_entriesAreEvictedWhenIdle(t *testing.T) {
	s := &SystemdSpawner{}
	for _, ns := range []string{"a", "b", "c"} {
		mustLockNamespace(t, s, ns)()
	}
	if n := s.namespaceLocks.size(); n != 0 {
		t.Fatalf("%d entries after every lock was released; want 0", n)
	}

	unlock := mustLockNamespace(t, s, "a")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _ = s.LockNamespace(ctx, "a")
	if n := s.namespaceLocks.size(); n != 1 {
		t.Fatalf("%d entries while one is held; want 1", n)
	}
	unlock()
	if n := s.namespaceLocks.size(); n != 0 {
		t.Fatalf("%d entries after the holder and the departed waiter were gone; want 0", n)
	}
}

// Releasing twice must not release a lock the next holder owns.
func TestLockNamespace_releasingTwiceIsHarmless(t *testing.T) {
	s := &SystemdSpawner{}
	first := mustLockNamespace(t, s, "acme")
	first()
	second := mustLockNamespace(t, s, "acme")
	defer second()
	first()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.LockNamespace(ctx, "acme"); err == nil {
		t.Fatal("a stale second release freed the lock the next holder owns")
	}
}

func TestLockNamespace_excludesConcurrentHolders(t *testing.T) {
	s := &SystemdSpawner{}
	var wg sync.WaitGroup
	inside, maxInside := 0, 0
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := mustLockNamespace(t, s, "acme")
			defer unlock()
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d holders at once; want 1", maxInside)
	}
}
