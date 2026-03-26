package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractAPIKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer ak_foo:ns")
	if got := extractAPIKey(r); got != "ak_foo:ns" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set("Authorization", "ApiKey ak2")
	if got := extractAPIKey(r); got != "ak2" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set("Authorization", "ak3raw")
	if got := extractAPIKey(r); got != "ak3raw" {
		t.Fatalf("got %q", got)
	}
	r.Header = http.Header{}
	r.Header.Set("X-API-Key", "xkey")
	if got := extractAPIKey(r); got != "xkey" {
		t.Fatalf("got %q", got)
	}
}

// TestDomainRoutingMiddleware_NonDebrosNetwork tests that non-orama domains pass through
func TestDomainRoutingMiddleware_NonDebrosNetwork(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for non-orama domain")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_APIPathBypass tests that /v1/ paths bypass routing
func TestDomainRoutingMiddleware_APIPathBypass(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/v1/deployments/list", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for /v1/ path")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_WellKnownBypass tests that /.well-known/ paths bypass routing
func TestDomainRoutingMiddleware_WellKnownBypass(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called for /.well-known/ path")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// TestDomainRoutingMiddleware_NoDeploymentService tests graceful handling when deployment service is nil
func TestDomainRoutingMiddleware_NoDeploymentService(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	g := &Gateway{
		// deploymentService is nil
		staticHandler: nil,
	}
	middleware := g.domainRoutingMiddleware(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "myapp.orama.network"

	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("Expected next handler to be called when deployment service is nil")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// TestIsPublicPath
// ---------------------------------------------------------------------------

func TestIsPublicPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		// Exact public paths
		{"health", "/health", true},
		{"v1 health", "/v1/health", true},
		{"status", "/status", true},
		{"v1 status", "/v1/status", true},
		{"auth challenge", "/v1/auth/challenge", true},
		{"auth verify", "/v1/auth/verify", true},
		{"auth register", "/v1/auth/register", true},
		{"auth refresh", "/v1/auth/refresh", true},
		{"auth logout", "/v1/auth/logout", true},
		{"auth api-key", "/v1/auth/api-key", true},
		{"auth jwks", "/v1/auth/jwks", true},
		{"well-known jwks", "/.well-known/jwks.json", true},
		{"version", "/v1/version", true},
		{"network status", "/v1/network/status", true},
		{"network peers", "/v1/network/peers", true},

		// Prefix-matched public paths
		{"acme challenge", "/.well-known/acme-challenge/abc", true},
		{"invoke function", "/v1/invoke/func1", true},
		{"functions invoke", "/v1/functions/myfn/invoke", true},
		{"internal replica", "/v1/internal/deployments/replica/xyz", true},
		{"internal wg peers", "/v1/internal/wg/peers", true},
		{"internal join", "/v1/internal/join", true},
		{"internal namespace spawn", "/v1/internal/namespace/spawn", true},
		{"internal namespace repair", "/v1/internal/namespace/repair", true},
		{"phantom session", "/v1/auth/phantom/session", true},
		{"phantom complete", "/v1/auth/phantom/complete", true},

		// Namespace status
		{"namespace status", "/v1/namespace/status", true},
		{"namespace status with id", "/v1/namespace/status/xyz", true},

		// NON-public paths
		{"deployments list", "/v1/deployments/list", false},
		{"storage upload", "/v1/storage/upload", false},
		{"pubsub publish", "/v1/pubsub/publish", false},
		{"db query", "/v1/db/query", false},
		{"auth whoami", "/v1/auth/whoami", false},
		{"auth simple-key", "/v1/auth/simple-key", false},
		{"functions without invoke", "/v1/functions/myfn", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isPublicPath(tc.path)
			if got != tc.want {
				t.Errorf("isPublicPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestIsWebSocketUpgrade
// ---------------------------------------------------------------------------

func TestIsWebSocketUpgrade(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		upgrade    string
		setHeaders bool
		want       bool
	}{
		{
			name:       "standard websocket upgrade",
			connection: "upgrade",
			upgrade:    "websocket",
			setHeaders: true,
			want:       true,
		},
		{
			name:       "case insensitive",
			connection: "Upgrade",
			upgrade:    "WebSocket",
			setHeaders: true,
			want:       true,
		},
		{
			name:       "connection contains upgrade among others",
			connection: "keep-alive, upgrade",
			upgrade:    "websocket",
			setHeaders: true,
			want:       true,
		},
		{
			name:       "connection keep-alive without upgrade",
			connection: "keep-alive",
			upgrade:    "websocket",
			setHeaders: true,
			want:       false,
		},
		{
			name:       "upgrade not websocket",
			connection: "upgrade",
			upgrade:    "h2c",
			setHeaders: true,
			want:       false,
		},
		{
			name:       "no headers set",
			setHeaders: false,
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.setHeaders {
				r.Header.Set("Connection", tc.connection)
				r.Header.Set("Upgrade", tc.upgrade)
			}
			got := isWebSocketUpgrade(r)
			if got != tc.want {
				t.Errorf("isWebSocketUpgrade() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetClientIP
// ---------------------------------------------------------------------------

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xRealIP    string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For single IP",
			xff:        "1.2.3.4",
			remoteAddr: "9.9.9.9:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For multiple IPs",
			xff:        "1.2.3.4, 5.6.7.8",
			remoteAddr: "9.9.9.9:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Real-IP fallback",
			xRealIP:    "1.2.3.4",
			remoteAddr: "9.9.9.9:1234",
			want:       "1.2.3.4",
		},
		{
			name:       "RemoteAddr fallback",
			remoteAddr: "9.8.7.6:1234",
			want:       "9.8.7.6",
		},
		{
			name:       "X-Forwarded-For takes priority over X-Real-IP",
			xff:        "1.2.3.4",
			xRealIP:    "5.6.7.8",
			remoteAddr: "9.9.9.9:1234",
			want:       "1.2.3.4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				r.Header.Set("X-Real-IP", tc.xRealIP)
			}
			got := getClientIP(r)
			if got != tc.want {
				t.Errorf("getClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRemoteAddrIP
// ---------------------------------------------------------------------------

func TestRemoteAddrIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"ipv4 with port", "192.168.1.1:5000", "192.168.1.1"},
		{"ipv4 different port", "10.0.0.1:6001", "10.0.0.1"},
		{"ipv6 with port", "[::1]:5000", "::1"},
		{"ip without port", "192.168.1.1", "192.168.1.1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			got := remoteAddrIP(r)
			if got != tc.want {
				t.Errorf("remoteAddrIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestSecurityHeadersMiddleware
// ---------------------------------------------------------------------------

func TestSecurityHeadersMiddleware(t *testing.T) {
	g := &Gateway{
		cfg: &Config{},
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := g.securityHeadersMiddleware(next)

	t.Run("sets standard security headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		expected := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"X-Xss-Protection":       "0",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
			"Permissions-Policy":     "camera=(self), microphone=(self), geolocation=()",
		}
		for header, want := range expected {
			got := rr.Header().Get(header)
			if got != want {
				t.Errorf("header %q = %q, want %q", header, got, want)
			}
		}
	})

	t.Run("no HSTS when no TLS and no X-Forwarded-Proto", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if hsts := rr.Header().Get("Strict-Transport-Security"); hsts != "" {
			t.Errorf("expected no HSTS header, got %q", hsts)
		}
	})

	t.Run("HSTS set when X-Forwarded-Proto is https", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		hsts := rr.Header().Get("Strict-Transport-Security")
		if hsts == "" {
			t.Error("expected HSTS header to be set when X-Forwarded-Proto is https")
		}
		want := "max-age=31536000; includeSubDomains"
		if hsts != want {
			t.Errorf("HSTS = %q, want %q", hsts, want)
		}
	})
}

// ---------------------------------------------------------------------------
// TestGetAllowedOrigin
// ---------------------------------------------------------------------------

func TestGetAllowedOrigin(t *testing.T) {
	tests := []struct {
		name       string
		baseDomain string
		origin     string
		want       string
	}{
		{
			name:       "no base domain returns wildcard",
			baseDomain: "",
			origin:     "https://anything.com",
			want:       "*",
		},
		{
			name:       "matching subdomain returns origin",
			baseDomain: "dbrs.space",
			origin:     "https://app.dbrs.space",
			want:       "https://app.dbrs.space",
		},
		{
			name:       "localhost returns origin",
			baseDomain: "dbrs.space",
			origin:     "http://localhost:3000",
			want:       "http://localhost:3000",
		},
		{
			name:       "non-matching origin returns base domain",
			baseDomain: "dbrs.space",
			origin:     "https://evil.com",
			want:       "https://dbrs.space",
		},
		{
			name:       "empty origin with base domain returns base domain",
			baseDomain: "dbrs.space",
			origin:     "",
			want:       "https://dbrs.space",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &Gateway{
				cfg: &Config{BaseDomain: tc.baseDomain},
			}
			got := g.getAllowedOrigin(tc.origin)
			if got != tc.want {
				t.Errorf("getAllowedOrigin(%q) = %q, want %q", tc.origin, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRequiresNamespaceOwnership
// ---------------------------------------------------------------------------

func TestRequiresNamespaceOwnership(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		// Paths that require ownership
		{"rqlite root", "/rqlite", true},
		{"v1 rqlite", "/v1/rqlite", true},
		{"v1 rqlite query", "/v1/rqlite/query", true},
		{"pubsub", "/v1/pubsub", true},
		{"pubsub publish", "/v1/pubsub/publish", true},
		{"proxy something", "/v1/proxy/something", true},
		{"functions root", "/v1/functions", true},
		{"functions specific", "/v1/functions/myfn", true},

		// Paths that do NOT require ownership
		{"auth challenge", "/v1/auth/challenge", false},
		{"deployments list", "/v1/deployments/list", false},
		{"health", "/health", false},
		{"storage upload", "/v1/storage/upload", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := requiresNamespaceOwnership(tc.path)
			if got != tc.want {
				t.Errorf("requiresNamespaceOwnership(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetString and TestGetInt
// ---------------------------------------------------------------------------

func TestGetString(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"string value", "hello", "hello"},
		{"int value", 42, ""},
		{"nil value", nil, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getString(tc.input)
			if got != tc.want {
				t.Errorf("getString(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  int
	}{
		{"int value", 42, 42},
		{"int64 value", int64(100), 100},
		{"float64 value", float64(3.7), 3},
		{"string value", "nope", 0},
		{"nil value", nil, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getInt(tc.input)
			if got != tc.want {
				t.Errorf("getInt(%v) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCircuitBreaker
// ---------------------------------------------------------------------------

func TestCircuitBreaker(t *testing.T) {
	t.Run("starts closed and allows requests", func(t *testing.T) {
		cb := NewCircuitBreaker()
		if !cb.Allow() {
			t.Fatal("expected Allow() = true for new circuit breaker")
		}
	})

	t.Run("opens after threshold failures", func(t *testing.T) {
		cb := NewCircuitBreaker()
		for i := 0; i < 5; i++ {
			cb.RecordFailure()
		}
		if cb.Allow() {
			t.Fatal("expected Allow() = false after 5 failures (circuit should be open)")
		}
	})

	t.Run("transitions to half-open after open duration", func(t *testing.T) {
		cb := NewCircuitBreaker()
		cb.openDuration = 1 * time.Millisecond // Use short duration for testing

		// Open the circuit
		for i := 0; i < 5; i++ {
			cb.RecordFailure()
		}
		if cb.Allow() {
			t.Fatal("expected Allow() = false when circuit is open")
		}

		// Wait for open duration to elapse
		time.Sleep(5 * time.Millisecond)

		// Should transition to half-open and allow one probe
		if !cb.Allow() {
			t.Fatal("expected Allow() = true after open duration (should be half-open)")
		}

		// Second call in half-open should be blocked (only one probe allowed)
		if cb.Allow() {
			t.Fatal("expected Allow() = false in half-open state (probe already in flight)")
		}
	})

	t.Run("RecordSuccess resets to closed", func(t *testing.T) {
		cb := NewCircuitBreaker()
		cb.openDuration = 1 * time.Millisecond

		// Open the circuit
		for i := 0; i < 5; i++ {
			cb.RecordFailure()
		}

		// Wait for half-open transition
		time.Sleep(5 * time.Millisecond)
		cb.Allow() // transition to half-open

		// Record success to close circuit
		cb.RecordSuccess()

		// Should be closed now and allow requests
		if !cb.Allow() {
			t.Fatal("expected Allow() = true after RecordSuccess (circuit should be closed)")
		}
		if !cb.Allow() {
			t.Fatal("expected Allow() = true again (circuit should remain closed)")
		}
	})
}

// ---------------------------------------------------------------------------
// TestCircuitBreakerRegistry
// ---------------------------------------------------------------------------

func TestCircuitBreakerRegistry(t *testing.T) {
	t.Run("creates new breaker if not exists", func(t *testing.T) {
		reg := NewCircuitBreakerRegistry()
		cb := reg.Get("target-a")
		if cb == nil {
			t.Fatal("expected non-nil circuit breaker")
		}
		if !cb.Allow() {
			t.Fatal("expected new breaker to allow requests")
		}
	})

	t.Run("returns same breaker for same key", func(t *testing.T) {
		reg := NewCircuitBreakerRegistry()
		cb1 := reg.Get("target-a")
		cb2 := reg.Get("target-a")
		if cb1 != cb2 {
			t.Fatal("expected same circuit breaker instance for same key")
		}
	})

	t.Run("different keys get different breakers", func(t *testing.T) {
		reg := NewCircuitBreakerRegistry()
		cb1 := reg.Get("target-a")
		cb2 := reg.Get("target-b")
		if cb1 == cb2 {
			t.Fatal("expected different circuit breaker instances for different keys")
		}
	})
}

// ---------------------------------------------------------------------------
// TestIsResponseFailure
// ---------------------------------------------------------------------------

func TestIsResponseFailure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"502 Bad Gateway", 502, true},
		{"503 Service Unavailable", 503, true},
		{"504 Gateway Timeout", 504, true},
		{"200 OK", 200, false},
		{"201 Created", 201, false},
		{"400 Bad Request", 400, false},
		{"401 Unauthorized", 401, false},
		{"403 Forbidden", 403, false},
		{"404 Not Found", 404, false},
		{"500 Internal Server Error", 500, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsResponseFailure(tc.statusCode)
			if got != tc.want {
				t.Errorf("IsResponseFailure(%d) = %v, want %v", tc.statusCode, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestExtractAPIKey_Extended
// ---------------------------------------------------------------------------

func TestExtractAPIKey_Extended(t *testing.T) {
	t.Run("JWT Bearer token with 2 dots returns empty", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.c2lnbmF0dXJl")
		got := extractAPIKey(r)
		if got != "" {
			t.Errorf("expected empty for JWT Bearer, got %q", got)
		}
	})

	t.Run("WebSocket upgrade with api_key query param", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?api_key=ws_key_123", nil)
		r.Header.Set("Connection", "upgrade")
		r.Header.Set("Upgrade", "websocket")
		got := extractAPIKey(r)
		if got != "ws_key_123" {
			t.Errorf("expected %q, got %q", "ws_key_123", got)
		}
	})

	t.Run("WebSocket upgrade with token query param", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?token=ws_tok_456", nil)
		r.Header.Set("Connection", "upgrade")
		r.Header.Set("Upgrade", "websocket")
		got := extractAPIKey(r)
		if got != "ws_tok_456" {
			t.Errorf("expected %q, got %q", "ws_tok_456", got)
		}
	})

	t.Run("non-WebSocket with query params should NOT extract", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?api_key=should_not_extract", nil)
		got := extractAPIKey(r)
		if got != "" {
			t.Errorf("expected empty for non-WebSocket request with query param, got %q", got)
		}
	})

	t.Run("empty X-API-Key header", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-API-Key", "")
		got := extractAPIKey(r)
		if got != "" {
			t.Errorf("expected empty for blank X-API-Key, got %q", got)
		}
	})

	t.Run("Authorization with no scheme and no dots (raw token)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "rawtoken123")
		got := extractAPIKey(r)
		if got != "rawtoken123" {
			t.Errorf("expected %q, got %q", "rawtoken123", got)
		}
	})

	t.Run("Authorization with no scheme but looks like JWT (2 dots) returns empty", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "part1.part2.part3")
		got := extractAPIKey(r)
		if got != "" {
			t.Errorf("expected empty for JWT-like raw token, got %q", got)
		}
	})
}
