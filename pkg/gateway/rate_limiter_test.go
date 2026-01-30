package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(60, 10) // 1/sec, burst 10
	for i := 0; i < 10; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed (within burst)", i)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(60, 5) // 1/sec, burst 5
	// Exhaust burst
	for i := 0; i < 5; i++ {
		rl.Allow("1.2.3.4")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("request after burst should be blocked")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewRateLimiter(6000, 5) // 100/sec, burst 5
	// Exhaust burst
	for i := 0; i < 5; i++ {
		rl.Allow("1.2.3.4")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("should be blocked after burst")
	}
	// Wait for refill
	time.Sleep(100 * time.Millisecond)
	if !rl.Allow("1.2.3.4") {
		t.Fatal("should be allowed after refill")
	}
}

func TestRateLimiter_PerIPIsolation(t *testing.T) {
	rl := NewRateLimiter(60, 2)
	// Exhaust IP A
	rl.Allow("1.1.1.1")
	rl.Allow("1.1.1.1")
	if rl.Allow("1.1.1.1") {
		t.Fatal("IP A should be blocked")
	}
	// IP B should still be allowed
	if !rl.Allow("2.2.2.2") {
		t.Fatal("IP B should be allowed")
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rl := NewRateLimiter(60, 10)
	rl.Allow("old-ip")
	// Force the entry to be old
	rl.mu.Lock()
	rl.clients["old-ip"].lastCheck = time.Now().Add(-20 * time.Minute)
	rl.mu.Unlock()

	rl.Cleanup(10 * time.Minute)

	rl.mu.Lock()
	_, exists := rl.clients["old-ip"]
	rl.mu.Unlock()
	if exists {
		t.Fatal("stale entry should have been cleaned up")
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(60000, 100) // high limit to avoid false failures
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				rl.Allow("concurrent-ip")
			}
		}()
	}
	wg.Wait()
}

func TestIsInternalIP(t *testing.T) {
	tests := []struct {
		ip       string
		internal bool
	}{
		{"10.0.0.1", true},
		{"10.0.0.254", true},
		{"10.255.255.255", true},
		{"127.0.0.1", true},
		{"192.168.1.1", false},
		{"8.8.8.8", false},
		{"141.227.165.168", false},
	}
	for _, tt := range tests {
		if got := isInternalIP(tt.ip); got != tt.internal {
			t.Errorf("isInternalIP(%q) = %v, want %v", tt.ip, got, tt.internal)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	gw := &Gateway{}
	handler := gw.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	expected := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":          "DENY",
		"X-XSS-Protection":        "0",
		"Referrer-Policy":          "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}

	for header, want := range expected {
		if got := w.Header().Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_NoHSTS_WithoutTLS(t *testing.T) {
	gw := &Gateway{}
	handler := gw.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should not be set without TLS, got %q", got)
	}
}

func TestRateLimitMiddleware_Returns429(t *testing.T) {
	gw := &Gateway{rateLimiter: NewRateLimiter(60, 1)}
	handler := gw.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should pass
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request should be 200, got %d", w.Code)
	}

	// Second request should be rate limited
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("should have Retry-After header")
	}
}

func TestRateLimitMiddleware_ExemptsInternalTraffic(t *testing.T) {
	gw := &Gateway{rateLimiter: NewRateLimiter(60, 1)}
	handler := gw.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Internal IP should never be rate limited
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("internal request %d should be 200, got %d", i, w.Code)
		}
	}
}
