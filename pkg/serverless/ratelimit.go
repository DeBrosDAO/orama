package serverless

import (
	"context"
	"sync"
	"time"
)

// TokenBucketLimiter implements RateLimiter using a token bucket algorithm.
type TokenBucketLimiter struct {
	mu       sync.Mutex
	tokens   float64
	max      float64
	refill   float64 // tokens per second
	lastTime time.Time
}

// NewTokenBucketLimiter creates a rate limiter with the given per-minute limit.
func NewTokenBucketLimiter(perMinute int) *TokenBucketLimiter {
	perSecond := float64(perMinute) / 60.0
	return &TokenBucketLimiter{
		tokens:   float64(perMinute), // start full
		max:      float64(perMinute),
		refill:   perSecond,
		lastTime: time.Now(),
	}
}

// Allow checks if a request should be allowed. Returns true if allowed.
func (t *TokenBucketLimiter) Allow(_ context.Context, _ string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(t.lastTime).Seconds()
	t.lastTime = now

	// Refill tokens
	t.tokens += elapsed * t.refill
	if t.tokens > t.max {
		t.tokens = t.max
	}

	// Check if we have a token
	if t.tokens < 1.0 {
		return false, nil
	}

	t.tokens--
	return true, nil
}
