package gateway

import (
	"sync"
	"time"
)

// logThrottleMaxKeys is how many keys a logThrottle remembers before it drops
// the ones whose interval has passed.
const logThrottleMaxKeys = 1024

// logThrottle lets one event per key through per interval, for a log line a
// request can trigger without limit. The zero value is ready to use.
type logThrottle struct {
	mu         sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}

// allow reports whether the event for key may be logged at now, and how many
// events of that key were held back since the last one that was.
func (t *logThrottle) allow(key string, now time.Time, interval time.Duration) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = make(map[string]time.Time)
		t.suppressed = make(map[string]int)
	}
	if prev, ok := t.last[key]; ok && now.Sub(prev) < interval {
		t.suppressed[key]++
		return false, 0
	}
	if len(t.last) >= logThrottleMaxKeys {
		for k, prev := range t.last {
			if now.Sub(prev) >= interval {
				delete(t.last, k)
				delete(t.suppressed, k)
			}
		}
	}
	held := t.suppressed[key]
	t.last[key] = now
	delete(t.suppressed, key)
	return true, held
}
