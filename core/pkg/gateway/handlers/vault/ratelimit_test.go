package vault

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIPRateLimiter_PullBudget verifies the per-IP pull limiter admits exactly
// the burst budget then rejects further requests from the same IP, while a
// different IP is unaffected.
func TestIPRateLimiter_PullBudget(t *testing.T) {
	rl := NewIPRateLimiter()

	const ip = "203.0.113.7"
	allowed := 0
	for i := 0; i < pullPerMinutePerIP+5; i++ {
		if rl.AllowPull(ip) {
			allowed++
		}
	}
	if allowed != pullPerMinutePerIP {
		t.Fatalf("expected %d pulls admitted, got %d", pullPerMinutePerIP, allowed)
	}
	if rl.AllowPull(ip) {
		t.Fatal("expected further pull from same IP to be rate limited")
	}

	// A different IP has its own independent budget.
	if !rl.AllowPull("198.51.100.9") {
		t.Fatal("expected a distinct IP to be admitted")
	}
}

// TestIPRateLimiter_PushIndependentOfPull verifies push and pull budgets are
// tracked separately for the same IP.
func TestIPRateLimiter_PushIndependentOfPull(t *testing.T) {
	rl := NewIPRateLimiter()
	const ip = "203.0.113.7"

	for i := 0; i < pullPerMinutePerIP; i++ {
		rl.AllowPull(ip)
	}
	if rl.AllowPull(ip) {
		t.Fatal("pull budget should be exhausted")
	}
	if !rl.AllowPush(ip) {
		t.Fatal("push budget should be independent of pull budget")
	}
}

// TestClientIP checks precedence: X-Forwarded-For first, then X-Real-IP, then
// the TCP peer address.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		xreal      string
		want       string
	}{
		{"remote_addr", "192.0.2.5:54321", "", "", "192.0.2.5"},
		{"x_real_ip", "192.0.2.5:54321", "", "198.51.100.2", "198.51.100.2"},
		{"xff_first", "192.0.2.5:54321", "203.0.113.1, 70.41.3.18", "198.51.100.2", "203.0.113.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/vault/pull", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xreal != "" {
				r.Header.Set("X-Real-IP", tc.xreal)
			}
			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
