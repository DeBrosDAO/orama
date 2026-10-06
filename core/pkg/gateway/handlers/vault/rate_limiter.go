package vault

import (
	"sync"
	"time"
)

// IdentityRateLimiter provides per-identity-hash rate limiting for vault operations.
// Push and pull have separate rate limits since push is more expensive.
type IdentityRateLimiter struct {
	pushBuckets sync.Map // identity -> *tokenBucket
	pullBuckets sync.Map // identity -> *tokenBucket
	pushRate    float64  // tokens per second
	pushBurst   int
	pullRate    float64 // tokens per second
	pullBurst   int
	stopCh      chan struct{}
}

type tokenBucket struct {
	mu        sync.Mutex
	tokens    float64
	lastCheck time.Time
}

// NewIdentityRateLimiter creates a per-identity rate limiter.
// pushPerHour and pullPerHour are sustained rates; burst is 1/6th of the hourly rate.
func NewIdentityRateLimiter(pushPerHour, pullPerHour int) *IdentityRateLimiter {
	pushBurst := pushPerHour / 6
	if pushBurst < 1 {
		pushBurst = 1
	}
	pullBurst := pullPerHour / 6
	if pullBurst < 1 {
		pullBurst = 1
	}
	return &IdentityRateLimiter{
		pushRate:  float64(pushPerHour) / 3600.0,
		pushBurst: pushBurst,
		pullRate:  float64(pullPerHour) / 3600.0,
		pullBurst: pullBurst,
	}
}

// AllowPush checks if a push for this identity is allowed.
func (rl *IdentityRateLimiter) AllowPush(identity string) bool {
	return rl.allow(&rl.pushBuckets, identity, rl.pushRate, rl.pushBurst)
}

// AllowPull checks if a pull for this identity is allowed.
func (rl *IdentityRateLimiter) AllowPull(identity string) bool {
	return rl.allow(&rl.pullBuckets, identity, rl.pullRate, rl.pullBurst)
}

func (rl *IdentityRateLimiter) allow(buckets *sync.Map, identity string, rate float64, burst int) bool {
	val, _ := buckets.LoadOrStore(identity, &tokenBucket{
		tokens:    float64(burst),
		lastCheck: time.Now(),
	})
	b := val.(*tokenBucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rate
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.lastCheck = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// StartCleanup runs periodic cleanup of stale identity entries.
func (rl *IdentityRateLimiter) StartCleanup(interval, maxAge time.Duration) {
	rl.stopCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rl.cleanup(maxAge)
			case <-rl.stopCh:
				return
			}
		}
	}()
}

// Stop terminates the background cleanup goroutine.
func (rl *IdentityRateLimiter) Stop() {
	if rl.stopCh != nil {
		close(rl.stopCh)
	}
}

func (rl *IdentityRateLimiter) cleanup(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	cleanMap := func(m *sync.Map) {
		m.Range(func(key, value interface{}) bool {
			b := value.(*tokenBucket)
			b.mu.Lock()
			stale := b.lastCheck.Before(cutoff)
			b.mu.Unlock()
			if stale {
				m.Delete(key)
			}
			return true
		})
	}
	cleanMap(&rl.pushBuckets)
	cleanMap(&rl.pullBuckets)
}
