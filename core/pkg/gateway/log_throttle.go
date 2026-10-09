package gateway

import (
	"container/list"
	"sync"
	"time"
)

// logThrottleMaxKeys is how many keys a logThrottle remembers. At the cap the
// key that was let through longest ago is forgotten, and logs again at once if
// it comes back.
const logThrottleMaxKeys = 1024

// logThrottle lets one event per key through per interval, for a log line a
// request can trigger without limit. The zero value is ready to use.
type logThrottle struct {
	mu      sync.Mutex
	entries map[string]*list.Element // key -> element of order
	order   *list.List               // *throttleEntry, least recently let through first
}

type throttleEntry struct {
	key        string
	last       time.Time
	suppressed int
}

// allow reports whether the event for key may be logged at now, and how many
// events of that key were held back since the last one that was.
func (t *logThrottle) allow(key string, now time.Time, interval time.Duration) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]*list.Element)
		t.order = list.New()
	}
	if el, ok := t.entries[key]; ok {
		e := el.Value.(*throttleEntry)
		if now.Sub(e.last) < interval {
			e.suppressed++
			return false, 0
		}
		held := e.suppressed
		e.last, e.suppressed = now, 0
		t.order.MoveToBack(el)
		return true, held
	}
	if t.order.Len() >= logThrottleMaxKeys {
		oldest := t.order.Front()
		delete(t.entries, oldest.Value.(*throttleEntry).key)
		t.order.Remove(oldest)
	}
	t.entries[key] = t.order.PushBack(&throttleEntry{key: key, last: now})
	return true, 0
}
