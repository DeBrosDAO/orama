package gateway

import "sync"

// holdLimiter bounds how many requests one client holds open at once, where a RateLimiter bounds how
// fast it makes them. A long-lived stream needs both: the bucket alone lets a client hold as many as
// the stream lasts times its refill rate.
type holdLimiter struct {
	mu   sync.Mutex
	max  int
	held map[string]int
}

func newHoldLimiter(max int) *holdLimiter {
	return &holdLimiter{max: max, held: map[string]int{}}
}

// acquire takes a hold for key and reports whether it had room. A hold that was refused is not taken,
// and must not be released.
func (l *holdLimiter) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] >= l.max {
		return false
	}
	l.held[key]++
	return true
}

// release gives a hold back. The entry goes with the last hold, so the map holds only clients with a
// request open.
func (l *holdLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] <= 1 {
		delete(l.held, key)
		return
	}
	l.held[key]--
}
