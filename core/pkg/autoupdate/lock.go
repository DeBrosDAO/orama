package autoupdate

import (
	"sync"
	"time"
)

// LockName is the cluster_locks row a rollout takes. Production code passes
// it to rqlite.AcquireClusterLock. The in-memory Lock is the same rule — one
// holder, a lease, a crash frees it — without a database.
const LockName = "autoupdate"

// Lock is a single-holder lease. Try is safe for concurrent callers.
type Lock struct {
	mu     sync.Mutex
	holder string
	until  time.Time
}

// Try takes the lock for holder until now+ttl. A holder whose lease has
// expired has crashed; the next caller takes it. The same holder may refresh.
func (l *Lock) Try(now time.Time, holder string, ttl time.Duration) bool {
	if holder == "" || ttl <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder != "" && l.holder != holder && now.Before(l.until) {
		return false
	}
	l.holder = holder
	l.until = now.Add(ttl)
	return true
}

// Holder reports who holds the lock at now. An expired lease reports nobody.
func (l *Lock) Holder(now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder == "" || !now.Before(l.until) {
		return ""
	}
	return l.holder
}
