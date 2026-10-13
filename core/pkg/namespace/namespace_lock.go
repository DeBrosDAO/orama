package namespace

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NamespaceLockWaitTimeout bounds how long a caller waits for another holder
// of a namespace's lock. A holder may legitimately keep it for minutes (a
// restore waiting on health checks, a teardown draining an SFU for up to 45s),
// but a holder wedged on a hung systemctl call must not stall every other
// operation on the namespace for ever: the waiter gives up with an error its
// caller can act on (a teardown is recorded as owed and replayed, a restore is
// retried by the next reconcile pass, a spawn request is failed).
const NamespaceLockWaitTimeout = 3 * time.Minute

// namespaceLockTable is the per-namespace locks of one node. A lock is a
// one-slot channel, so a waiter can leave when its context ends, and an entry
// exists only while somebody holds or waits for it: the table does not grow
// with the namespaces a node has ever hosted.
type namespaceLockTable struct {
	mu      sync.Mutex
	entries map[string]*namespaceLockEntry
}

type namespaceLockEntry struct {
	slot chan struct{}
	// users counts the holder and the waiters; the entry is dropped at zero.
	users int
}

// acquire waits for namespace's lock until ctx ends, and returns its release.
func (t *namespaceLockTable) acquire(ctx context.Context, namespace string) (func(), error) {
	e := t.enter(namespace)
	select {
	case e.slot <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-e.slot
				t.leave(namespace, e)
			})
		}, nil
	case <-ctx.Done():
		t.leave(namespace, e)
		return nil, ctx.Err()
	}
}

func (t *namespaceLockTable) enter(namespace string) *namespaceLockEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]*namespaceLockEntry)
	}
	e := t.entries[namespace]
	if e == nil {
		e = &namespaceLockEntry{slot: make(chan struct{}, 1)}
		t.entries[namespace] = e
	}
	e.users++
	return e
}

func (t *namespaceLockTable) leave(namespace string, e *namespaceLockEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e.users--
	if e.users == 0 {
		delete(t.entries, namespace)
	}
}

// size is the number of namespaces with a holder or a waiter.
func (t *namespaceLockTable) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// LockNamespace takes this node's lock on namespace and returns its release.
// A teardown holds it from the first unit stopped to the last file removed. A
// restore that decided from a registry read made before the namespace's delete
// began used to start its units again between those two, after which they
// held ports the registry had handed to the next namespace. Every path that
// starts a unit of a namespace takes it too, and decides under it.
//
// The wait ends when ctx does, or after NamespaceLockWaitTimeout, with an
// error naming the namespace; nothing is held then.
func (s *SystemdSpawner) LockNamespace(ctx context.Context, namespace string) (unlock func(), err error) {
	ctx, cancel := context.WithTimeout(ctx, NamespaceLockWaitTimeout)
	defer cancel()
	unlock, err = s.namespaceLocks.acquire(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("namespace %s is locked by another operation on this node (waited up to %s): %w", namespace, NamespaceLockWaitTimeout, err)
	}
	return unlock, nil
}
