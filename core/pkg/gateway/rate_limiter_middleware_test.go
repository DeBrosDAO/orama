package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/ratelimit"
)

// Feature #69: the namespaceRateLimitMiddleware must emit the canonical
// RPC error envelope on 429 (not plain text) so SDK clients see a
// structured error code instead of a bare HTTP body. Also: when the
// Manager is wired, it must take precedence over the legacy
// NamespaceRateLimiter.

// helper: build a Gateway with only the rate-limit fields we care about.
func newRateLimitTestGateway(t *testing.T, mgr *ratelimit.Manager, legacy *NamespaceRateLimiter) *Gateway {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	return &Gateway{
		rateLimitManager:     mgr,
		namespaceRateLimiter: legacy,
		logger:               logger,
	}
}

// requestWithNamespace returns a request with the namespace context key
// set, as the auth middleware would have done upstream.
func requestWithNamespace(ns string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/anything", nil)
	if ns != "" {
		r = r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, ns))
	}
	return r
}

func TestNamespaceRateLimitMiddleware_managerPath_emitsCanonicalEnvelopeOn429(t *testing.T) {
	// burst=1 → first request passes, second 429s.
	mgr := ratelimit.NewManager(nil, ratelimit.Defaults{RequestsPerMinute: 60, Burst: 1}, nil)
	g := newRateLimitTestGateway(t, mgr, nil)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := g.namespaceRateLimitMiddleware(next)

	// 1st request passes.
	r1 := requestWithNamespace("anchat-test")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}

	// 2nd request rate-limited.
	r2 := requestWithNamespace("anchat-test")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", w2.Code)
	}

	// The response MUST be the canonical RPC error envelope, not plain text.
	if got := w2.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (envelope, not plain text)", got)
	}
	if got := w2.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header missing on 429")
	}

	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code       string  `json:"code"`
			Message    string  `json:"message"`
			Retryable  bool    `json:"retryable"`
			RetryAfter float64 `json:"retry_after"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.OK {
		t.Error("envelope.ok = true, want false")
	}
	if envelope.Error.Code != "RATE_LIMITED" {
		t.Errorf("error.code = %q, want %q (per httputil.ErrCodeRateLimited)", envelope.Error.Code, "RATE_LIMITED")
	}
	if !envelope.Error.Retryable {
		t.Error("error.retryable = false, want true for rate-limit responses")
	}
	if envelope.Error.RetryAfter <= 0 {
		t.Error("error.retry_after = 0, want positive hint")
	}
}

func TestNamespaceRateLimitMiddleware_emptyNamespacePassesThrough(t *testing.T) {
	// No namespace in context (e.g., the auth middleware didn't set one
	// because the path is public) — middleware must let the request through.
	mgr := ratelimit.NewManager(nil, ratelimit.Defaults{RequestsPerMinute: 1, Burst: 0}, nil)
	g := newRateLimitTestGateway(t, mgr, nil)

	nextCalled := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { nextCalled = true })
	mw := g.namespaceRateLimitMiddleware(next)

	r := httptest.NewRequest(http.MethodGet, "/", nil) // no namespace context
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if !nextCalled {
		t.Error("next handler not called for empty-namespace request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no namespace = no limit)", w.Code)
	}
}

func TestNamespaceRateLimitMiddleware_managerPrefersOverLegacy(t *testing.T) {
	// Both manager AND legacy limiter present. Manager has burst=10 (lots
	// of headroom); legacy has burst=1 (would 429 immediately). If the
	// middleware uses manager, the first 5 requests should all pass. If
	// it accidentally falls back to legacy, the 2nd would 429.
	mgr := ratelimit.NewManager(nil, ratelimit.Defaults{RequestsPerMinute: 600, Burst: 10}, nil)
	legacy := NewNamespaceRateLimiter(60, 1)
	g := newRateLimitTestGateway(t, mgr, legacy)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := g.namespaceRateLimitMiddleware(next)

	for i := 0; i < 5; i++ {
		r := requestWithNamespace("anchat-test")
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (manager should win over legacy)", i+1, w.Code)
		}
	}
}

func TestNamespaceRateLimitMiddleware_legacyFallbackWhenManagerNil(t *testing.T) {
	// No manager wired, only legacy. burst=1, second request must 429.
	legacy := NewNamespaceRateLimiter(60, 1)
	g := newRateLimitTestGateway(t, nil, legacy)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := g.namespaceRateLimitMiddleware(next)

	r1 := requestWithNamespace("anchat-test")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}

	r2 := requestWithNamespace("anchat-test")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("legacy-path second request status = %d, want 429", w2.Code)
	}
	// Legacy path uses the same canonical envelope now — verify.
	if got := w2.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("legacy path Content-Type = %q, want application/json", got)
	}
}

func TestNamespaceRateLimitMiddleware_bothNilPassesThrough(t *testing.T) {
	// No rate limiter wired at all (test/dev modes). Middleware is a
	// no-op — every request passes.
	g := newRateLimitTestGateway(t, nil, nil)
	nextCalled := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { nextCalled = true })
	mw := g.namespaceRateLimitMiddleware(next)

	r := requestWithNamespace("anchat-test")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if !nextCalled {
		t.Error("next handler not called when no rate limiters wired")
	}
}

// TestNamespaceRateLimitMiddleware_cacheTTLPropagation — config change on
// a different gateway is picked up after the cache TTL elapses, without
// an explicit Invalidate call. This is the bounded-staleness guarantee
// that closes the cross-gateway cache-invalidation gap.
func TestNamespaceRateLimitMiddleware_cacheTTLPropagation(t *testing.T) {
	// Use a mutable store to simulate a config change happening on
	// another gateway between calls.
	store := &mutableStore{}
	mgr := ratelimit.NewManager(store, ratelimit.Defaults{RequestsPerMinute: 60, Burst: 1}, nil)
	// 100ms TTL + 150ms sleep keeps the test deterministic on loaded CI
	// runners. Over-sleeping is safe (cache stays expired longer, test
	// still passes); we just need to be sure we DON'T under-sleep.
	mgr.SetCacheTTL(100 * time.Millisecond)
	g := newRateLimitTestGateway(t, mgr, nil)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := g.namespaceRateLimitMiddleware(next)

	// Round 1: tight default (burst=1). One pass, one 429.
	r1 := requestWithNamespace("anchat-test")
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("R1 first: status %d", w1.Code)
	}
	r2 := requestWithNamespace("anchat-test")
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("R1 second: status %d, want 429", w2.Code)
	}

	// Simulate: another gateway's PUT lands; this gateway's store can
	// now read the new value, but the cached limiter still has burst=1.
	store.cfg = &ratelimit.Config{
		Namespace:         "anchat-test",
		RequestsPerMinute: 6000,
		Burst:             100,
	}

	// Wait past the TTL so the cache entry expires and the next Allow
	// re-reads the store.
	time.Sleep(150 * time.Millisecond)

	// Round 2: burst=100 now in effect. 50 rapid-fire passes.
	for i := 0; i < 50; i++ {
		r := requestWithNamespace("anchat-test")
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("R2 request %d: status %d, want 200 (cache TTL should have propagated config)", i+1, w.Code)
		}
	}
}

// mutableStore is a tiny in-memory ConfigStore for the TTL test that lets
// us swap the returned config between calls.
type mutableStore struct {
	cfg *ratelimit.Config
}

func (m *mutableStore) Get(_ context.Context, _ string) (*ratelimit.Config, error) {
	if m.cfg == nil {
		return nil, nil
	}
	c := *m.cfg
	return &c, nil
}
func (m *mutableStore) Upsert(_ context.Context, cfg ratelimit.Config) error {
	m.cfg = &cfg
	return nil
}
func (m *mutableStore) Delete(_ context.Context, _ string) error { m.cfg = nil; return nil }
