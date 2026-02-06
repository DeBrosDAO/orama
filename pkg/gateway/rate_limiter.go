package gateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a token-bucket rate limiter per client IP.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*bucket
	rate    float64 // tokens per second
	burst   int     // max tokens (burst capacity)
}

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewRateLimiter creates a rate limiter. ratePerMinute is the sustained rate;
// burst is the maximum number of requests that can be made in a short window.
func NewRateLimiter(ratePerMinute, burst int) *RateLimiter {
	return &RateLimiter{
		clients: make(map[string]*bucket),
		rate:    float64(ratePerMinute) / 60.0,
		burst:   burst,
	}
}

// Allow checks if a request from the given IP should be allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.clients[ip]
	if !exists {
		rl.clients[ip] = &bucket{tokens: float64(rl.burst) - 1, lastCheck: now}
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastCheck = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Cleanup removes stale entries older than the given duration.
func (rl *RateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, b := range rl.clients {
		if b.lastCheck.Before(cutoff) {
			delete(rl.clients, ip)
		}
	}
}

// StartCleanup runs periodic cleanup in a goroutine.
func (rl *RateLimiter) StartCleanup(interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			rl.Cleanup(maxAge)
		}
	}()
}

// rateLimitMiddleware returns 429 when a client exceeds the rate limit.
// Internal traffic from the WireGuard subnet (10.0.0.0/8) is exempt.
func (g *Gateway) rateLimitMiddleware(next http.Handler) http.Handler {
	if g.rateLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)

		// Exempt internal cluster traffic (WireGuard subnet)
		if isInternalIP(ip) {
			next.ServeHTTP(w, r)
			return
		}

		if !g.rateLimiter.Allow(ip) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isInternalIP returns true if the IP is in the WireGuard 10.0.0.0/8 subnet
// or is a loopback address.
func isInternalIP(ipStr string) bool {
	// Strip port if present
	if strings.Contains(ipStr, ":") {
		host, _, err := net.SplitHostPort(ipStr)
		if err == nil {
			ipStr = host
		}
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	// 10.0.0.0/8 — WireGuard mesh
	_, wgNet, _ := net.ParseCIDR("10.0.0.0/8")
	return wgNet.Contains(ip)
}
