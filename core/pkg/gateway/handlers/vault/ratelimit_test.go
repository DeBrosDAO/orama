package vault

import (
	"fmt"
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

// clientIP resolves the client like the cluster gateway's limits do: the peer address, X-Forwarded-For
// only from the local reverse proxy and then only its last entry, and an IPv6 client as its /64.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		xreal      string
		want       string
	}{
		{"remote_addr", "192.0.2.5:54321", "", "", "192.0.2.5"},
		{"a forged xff from a direct caller is ignored", "192.0.2.5:54321", "203.0.113.1", "", "192.0.2.5"},
		{"a forged x-real-ip is ignored", "192.0.2.5:54321", "", "198.51.100.2", "192.0.2.5"},
		{"through the proxy the last entry counts, not the first", "127.0.0.1:54321", "6.6.6.6, 203.0.113.7", "", "203.0.113.7"},
		{"a forged first entry cannot pick the bucket", "127.0.0.1:54321", "9.9.9.9, 203.0.113.7", "", "203.0.113.7"},
		{"an ipv6 client is its /64", "[2001:db8:1:2:aaaa::1]:443", "", "", "2001:db8:1:2::/64"},
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

// Two addresses in one /64 share a pull bucket, and a forged header does not buy another.
func TestIPRateLimiter_aSlash64SharesOneBucketAndAForgedHeaderIsIgnored(t *testing.T) {
	rl := NewIPRateLimiter()
	served := 0
	for i := 0; i < pullPerMinutePerIP*2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/vault/pull", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:7:7:%x::1]:443", i+1)
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i%200+1))
		if rl.AllowPull(clientIP(r)) {
			served++
		}
	}
	if served != pullPerMinutePerIP {
		t.Fatalf("served %d pulls from one /64 with rotating headers, want the %d burst", served, pullPerMinutePerIP)
	}
}
