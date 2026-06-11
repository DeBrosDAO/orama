package ratelimit

import (
	"context"
	"sync"
	"testing"
)

// memStore is an in-memory ConfigStore for tests.
type memStore struct {
	mu    sync.Mutex
	rows  map[string]Config
	getErr error
}

func newMemStore() *memStore { return &memStore{rows: map[string]Config{}} }

func (m *memStore) Get(_ context.Context, namespace string) (*Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	if c, ok := m.rows[namespace]; ok {
		c2 := c
		return &c2, nil
	}
	return nil, nil
}
func (m *memStore) Upsert(_ context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[cfg.Namespace] = cfg
	return nil
}
func (m *memStore) Delete(_ context.Context, namespace string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, namespace)
	return nil
}

// ----------------------------------------------------------------------------
// Defaults.Sane
// ----------------------------------------------------------------------------

func TestDefaults_Sane(t *testing.T) {
	cases := []struct {
		name string
		in   Defaults
		want Defaults
	}{
		{
			"zero clamps to safe baseline",
			Defaults{},
			Defaults{RequestsPerMinute: 10_000, Burst: 5_000},
		},
		{
			"populated values pass through",
			Defaults{RequestsPerMinute: 500, Burst: 50, MaxRequestsPerMinute: 1000, MaxBurst: 100},
			Defaults{RequestsPerMinute: 500, Burst: 50, MaxRequestsPerMinute: 1000, MaxBurst: 100},
		},
		{
			"negative clamps to baseline",
			Defaults{RequestsPerMinute: -1, Burst: -1},
			Defaults{RequestsPerMinute: 10_000, Burst: 5_000},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Sane()
			if got != tc.want {
				t.Errorf("Sane() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Manager.Allow — base behaviour
// ----------------------------------------------------------------------------

func TestManager_Allow_emptyNamespaceAlwaysAllowed(t *testing.T) {
	m := NewManager(newMemStore(), Defaults{RequestsPerMinute: 1, Burst: 1}, nil)
	for i := 0; i < 10; i++ {
		if !m.Allow(context.Background(), "") {
			t.Fatal("empty namespace must always be allowed (per-IP limiter handles that layer)")
		}
	}
}

func TestManager_Allow_burstThenRefill(t *testing.T) {
	// Burst of 3 → first 3 requests pass, 4th fails.
	m := NewManager(newMemStore(), Defaults{RequestsPerMinute: 60, Burst: 3}, nil)
	ns := "test-ns"
	for i := 0; i < 3; i++ {
		if !m.Allow(context.Background(), ns) {
			t.Errorf("request %d should be allowed (within burst)", i+1)
		}
	}
	if m.Allow(context.Background(), ns) {
		t.Error("request 4 should be denied (burst exhausted)")
	}
}

// ----------------------------------------------------------------------------
// Manager — per-namespace config override
// ----------------------------------------------------------------------------

func TestManager_Allow_perNamespaceOverride(t *testing.T) {
	store := newMemStore()
	// One namespace gets a generous override; another uses defaults.
	store.rows["loud-tenant"] = Config{
		Namespace:         "loud-tenant",
		RequestsPerMinute: 60_000,
		Burst:             100,
	}
	m := NewManager(store, Defaults{RequestsPerMinute: 60, Burst: 1}, nil)

	// Default-namespace can fire only 1 request before being throttled.
	if !m.Allow(context.Background(), "quiet-tenant") {
		t.Error("first quiet-tenant request should pass")
	}
	if m.Allow(context.Background(), "quiet-tenant") {
		t.Error("second quiet-tenant request should be throttled (burst=1)")
	}

	// loud-tenant has the override, burst=100, so 50 in a row all pass.
	for i := 0; i < 50; i++ {
		if !m.Allow(context.Background(), "loud-tenant") {
			t.Fatalf("loud-tenant request %d should pass under override (burst=100)", i+1)
		}
	}
}

// ----------------------------------------------------------------------------
// Manager — store error degrades to defaults (fail-open is the safer mode)
// ----------------------------------------------------------------------------

func TestManager_Allow_storeErrorFallsBackToDefaults(t *testing.T) {
	store := newMemStore()
	store.getErr = errSentinel("boom")
	m := NewManager(store, Defaults{RequestsPerMinute: 60, Burst: 1}, nil)
	if !m.Allow(context.Background(), "any-ns") {
		t.Error("first request should pass under default burst even when store errs")
	}
	if m.Allow(context.Background(), "any-ns") {
		t.Error("second request should fail under default burst (store errored, defaults applied)")
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// ----------------------------------------------------------------------------
// Manager.Invalidate — cache miss after invalidate picks up new config
// ----------------------------------------------------------------------------

func TestManager_Invalidate_rebuildsWithNewConfig(t *testing.T) {
	store := newMemStore()
	// Initial: tight limit (burst=1).
	store.rows["tenant"] = Config{Namespace: "tenant", RequestsPerMinute: 60, Burst: 1}
	m := NewManager(store, Defaults{RequestsPerMinute: 60, Burst: 1}, nil)

	if !m.Allow(context.Background(), "tenant") {
		t.Fatal("first request should pass")
	}
	if m.Allow(context.Background(), "tenant") {
		t.Fatal("second request should be denied (burst=1)")
	}

	// Operator/tenant bumps the limit. Manager doesn't see it yet —
	// previous limiter is cached.
	store.rows["tenant"] = Config{Namespace: "tenant", RequestsPerMinute: 60, Burst: 100}
	if m.Allow(context.Background(), "tenant") {
		t.Error("without Invalidate, manager should still use the old cached limiter")
	}

	// Invalidate clears the cache → next request rebuilds with new burst.
	m.Invalidate("tenant")
	for i := 0; i < 50; i++ {
		if !m.Allow(context.Background(), "tenant") {
			t.Fatalf("post-invalidate request %d should pass under new config (burst=100)", i+1)
		}
	}
}

// ----------------------------------------------------------------------------
// Manager — concurrent access doesn't double-build limiters
// ----------------------------------------------------------------------------

func TestManager_concurrentBuilds_oneCanonicalLimiter(t *testing.T) {
	store := newMemStore()
	store.rows["tenant"] = Config{Namespace: "tenant", RequestsPerMinute: 60, Burst: 10}
	m := NewManager(store, Defaults{RequestsPerMinute: 60, Burst: 10}, nil)

	const goroutines = 50
	var allowedCount int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Allow(context.Background(), "tenant") {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// With burst=10 and 50 concurrent goroutines all hitting the same
	// namespace, exactly 10 should be allowed (or thereabouts — token
	// refill happens too fast for clock to matter at these intervals).
	// Most importantly: NOT 500 (which would happen if each goroutine
	// got its own freshly-built limiter due to a race).
	if allowedCount > 15 || allowedCount < 5 {
		t.Errorf("allowed = %d; expected ~10 (burst=10), got way off — suggests racy double-build", allowedCount)
	}
}

// ----------------------------------------------------------------------------
// Manager.Defaults — exposes operator ceiling for handler validation
// ----------------------------------------------------------------------------

func TestManager_Defaults_exposesOperatorCeiling(t *testing.T) {
	defs := Defaults{
		RequestsPerMinute:    1000,
		Burst:                100,
		MaxRequestsPerMinute: 5000,
		MaxBurst:             500,
	}
	m := NewManager(nil, defs, nil)
	got := m.Defaults()
	if got.MaxRequestsPerMinute != 5000 || got.MaxBurst != 500 {
		t.Errorf("Defaults().Max* = (%d,%d), want (5000,500)",
			got.MaxRequestsPerMinute, got.MaxBurst)
	}
}
