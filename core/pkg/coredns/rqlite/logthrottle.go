package rqlite

import (
	"sync"
	"time"
)

// BackendErrorLogInterval is the least time between two logged backend
// failures. Every query that reaches a dead backend fails, and the queries
// come from the internet: one line per query would turn an rqlite outage into a
// flood that fills the journal (and the disk) of the nameservers the operator
// needs in order to fix it.
const BackendErrorLogInterval = 10 * time.Second

// logThrottle lets one event through per interval and counts the rest.
type logThrottle struct {
	mu         sync.Mutex
	interval   time.Duration
	now        func() time.Time
	last       time.Time
	suppressed int
}

// allow reports whether the caller should log now and, when it should, how
// many events were suppressed since the last line that was logged.
func (t *logThrottle) allow() (log bool, suppressed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	interval := t.interval
	if interval == 0 {
		interval = BackendErrorLogInterval
	}
	if at := now(); t.last.IsZero() || at.Sub(t.last) >= interval {
		t.last, suppressed, t.suppressed = at, t.suppressed, 0
		return true, suppressed
	}
	t.suppressed++
	return false, 0
}
