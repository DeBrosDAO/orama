package vault

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Per-source-IP rate limits for the public vault proxy endpoints. These bound
// online password/seed guessing at the source. Because a RootID identity is
// derived from the seed/password, a brute-forcer can mint a valid ownership
// signature for every guess — so each attempt is a *different* identity and the
// per-identity limiter (see rate_limiter.go) never trips. The per-IP limiter is
// what catches many-identities-from-one-IP; the per-identity limiter catches the
// complementary distributed case (one identity, many IPs). Whichever trips first
// wins.
//
// Values are per minute. The pull path is the guessing-sensitive one (a pull is
// what an attacker repeats to test a candidate seed), so it gets the larger but
// still bounded budget; pushes are rare debounced backup writes and are capped
// tighter.
const (
	pullPerMinutePerIP = 60
	pushPerMinutePerIP = 30

	// ipRetryAfterSeconds is advertised in the Retry-After header on a 429.
	// It matches the ~1 minute refill window of the token bucket.
	ipRetryAfterSeconds = 60

	// ipCleanupInterval / ipBucketMaxAge bound memory: idle IP buckets are swept
	// so the maps cannot grow without limit under a churn of source addresses.
	ipCleanupInterval = 5 * time.Minute
	ipBucketMaxAge    = 10 * time.Minute
)

// IPRateLimiter provides per-source-IP token-bucket rate limiting for vault
// operations. It mirrors IdentityRateLimiter but keys on client IP instead of
// identity hash. Push and pull have independent budgets. Thread-safe.
type IPRateLimiter struct {
	pushBuckets sync.Map // ip -> *tokenBucket
	pullBuckets sync.Map // ip -> *tokenBucket
	pushRate    float64  // tokens per second
	pushBurst   int
	pullRate    float64 // tokens per second
	pullBurst   int
	stopCh      chan struct{}
}

// NewIPRateLimiter creates a per-IP rate limiter using the package-level
// per-minute limits. Burst equals a full minute's budget, refilling steadily.
func NewIPRateLimiter() *IPRateLimiter {
	return &IPRateLimiter{
		pushRate:  float64(pushPerMinutePerIP) / 60.0,
		pushBurst: pushPerMinutePerIP,
		pullRate:  float64(pullPerMinutePerIP) / 60.0,
		pullBurst: pullPerMinutePerIP,
	}
}

// AllowPush reports whether a push from this IP is allowed.
func (rl *IPRateLimiter) AllowPush(ip string) bool {
	return takeToken(&rl.pushBuckets, ip, rl.pushRate, rl.pushBurst)
}

// AllowPull reports whether a pull from this IP is allowed.
func (rl *IPRateLimiter) AllowPull(ip string) bool {
	return takeToken(&rl.pullBuckets, ip, rl.pullRate, rl.pullBurst)
}

// takeToken applies token-bucket admission for one key in a bucket map. It is a
// shared helper so the IP and identity limiters use identical, tested logic.
func takeToken(buckets *sync.Map, key string, rate float64, burst int) bool {
	val, _ := buckets.LoadOrStore(key, &tokenBucket{
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

// StartCleanup runs periodic cleanup of stale IP entries so the maps do not grow
// unbounded under a churn of source addresses.
func (rl *IPRateLimiter) StartCleanup(interval, maxAge time.Duration) {
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
func (rl *IPRateLimiter) Stop() {
	if rl.stopCh != nil {
		close(rl.stopCh)
	}
}

func (rl *IPRateLimiter) cleanup(maxAge time.Duration) {
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

// clientIP extracts the client IP from reverse-proxy headers (X-Forwarded-For,
// then X-Real-IP) and falls back to the TCP peer address. It mirrors the gateway
// package's getClientIP, duplicated here because that helper is unexported.
func clientIP(r *http.Request) string {
	// X-Forwarded-For may be a comma-separated list; the original client is first.
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
