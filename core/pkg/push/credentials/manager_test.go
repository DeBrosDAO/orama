package credentials

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store for unit tests. Tracks call counts so
// we can assert cache hits.
type fakeStore struct {
	mu        sync.Mutex
	rows      map[cacheKey]*Credential
	getCount  int
	getErrOn  cacheKey // if non-zero, Get returns errStub for this key
	errStub   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[cacheKey]*Credential{}}
}

func (f *fakeStore) Get(_ context.Context, ns, p string) (*Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCount++
	k := cacheKey{namespace: ns, provider: p}
	if f.errStub != nil && f.getErrOn == k {
		return nil, f.errStub
	}
	if c, ok := f.rows[k]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (f *fakeStore) Upsert(_ context.Context, c Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := c
	f.rows[cacheKey{namespace: c.Namespace, provider: c.Provider}] = &cp
	return nil
}

func (f *fakeStore) Delete(_ context.Context, ns, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, cacheKey{namespace: ns, provider: p})
	return nil
}

func (f *fakeStore) ListProviders(_ context.Context, ns string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.rows {
		if k.namespace == ns {
			out = append(out, k.provider)
		}
	}
	return out, nil
}

func TestManager_Get_cachesHit(t *testing.T) {
	store := newFakeStore()
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "apns", JSON: []byte(`{"k":"v"}`),
	})
	m := NewManager(store, nil)

	// First Get: store hit.
	c1, err := m.Get(context.Background(), "ns-a", "apns")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if c1 == nil || string(c1.JSON) != `{"k":"v"}` {
		t.Fatalf("first Get returned wrong credential: %+v", c1)
	}
	if store.getCount != 1 {
		t.Errorf("expected 1 store hit after first Get; got %d", store.getCount)
	}

	// Second Get: should be served from cache.
	if _, err := m.Get(context.Background(), "ns-a", "apns"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if store.getCount != 1 {
		t.Errorf("expected cache hit; store.getCount=%d (should still be 1)", store.getCount)
	}
}

func TestManager_Get_negativeCachePreservedUntilTTL(t *testing.T) {
	store := newFakeStore()
	m := NewManager(store, nil)
	m.SetCacheTTL(50 * time.Millisecond)

	// Namespace has no row — should cache the negative result.
	c1, err := m.Get(context.Background(), "ns-a", "apns")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c1 != nil {
		t.Errorf("expected nil credential for not-found; got %+v", c1)
	}
	if store.getCount != 1 {
		t.Errorf("expected 1 store hit; got %d", store.getCount)
	}

	// Second Get within TTL: cached negative, no store hit.
	c2, _ := m.Get(context.Background(), "ns-a", "apns")
	if c2 != nil {
		t.Errorf("expected nil cached credential; got %+v", c2)
	}
	if store.getCount != 1 {
		t.Errorf("negative cache should suppress store hit; getCount=%d", store.getCount)
	}
}

func TestManager_Get_ttlForcesRebuild(t *testing.T) {
	store := newFakeStore()
	m := NewManager(store, nil)
	m.SetCacheTTL(50 * time.Millisecond)

	// Initial: no row.
	if _, err := m.Get(context.Background(), "ns-a", "apns"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if store.getCount != 1 {
		t.Fatalf("expected 1; got %d", store.getCount)
	}

	// Another gateway "writes" a row to the store directly (simulating
	// the cross-gateway invalidation gap).
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "apns", JSON: []byte(`{"new":"value"}`),
	})

	// Within TTL: still cached negative.
	c, _ := m.Get(context.Background(), "ns-a", "apns")
	if c != nil {
		t.Errorf("within TTL: expected stale-nil cache; got %+v", c)
	}

	// Past TTL: rebuild reads the new row.
	time.Sleep(80 * time.Millisecond)
	c, err := m.Get(context.Background(), "ns-a", "apns")
	if err != nil {
		t.Fatalf("post-TTL Get: %v", err)
	}
	if c == nil || string(c.JSON) != `{"new":"value"}` {
		t.Errorf("expected fresh cred after TTL; got %+v", c)
	}
}

func TestManager_Get_storeErrorSurfaces(t *testing.T) {
	store := newFakeStore()
	store.errStub = errors.New("rqlite connection refused")
	store.getErrOn = cacheKey{namespace: "ns-a", provider: "apns"}
	m := NewManager(store, nil)

	_, err := m.Get(context.Background(), "ns-a", "apns")
	if err == nil {
		t.Fatal("expected store error to bubble up; got nil")
	}
	if err.Error() != "rqlite connection refused" {
		t.Errorf("wrong error wrapped/replaced: %v", err)
	}
}

func TestManager_Invalidate_evictsImmediately(t *testing.T) {
	store := newFakeStore()
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "apns", JSON: []byte(`{"v":1}`),
	})
	m := NewManager(store, nil)

	if _, err := m.Get(context.Background(), "ns-a", "apns"); err != nil {
		t.Fatalf("warm Get: %v", err)
	}
	if store.getCount != 1 {
		t.Fatalf("warm: %d", store.getCount)
	}

	m.Invalidate("ns-a", "apns")
	if _, err := m.Get(context.Background(), "ns-a", "apns"); err != nil {
		t.Fatalf("post-invalidate Get: %v", err)
	}
	if store.getCount != 2 {
		t.Errorf("expected store re-read after Invalidate; getCount=%d", store.getCount)
	}
}

func TestManager_InvalidateNamespace_evictsAllProviders(t *testing.T) {
	store := newFakeStore()
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "apns", JSON: []byte(`{}`),
	})
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "ntfy", JSON: []byte(`{}`),
	})
	m := NewManager(store, nil)

	_, _ = m.Get(context.Background(), "ns-a", "apns")
	_, _ = m.Get(context.Background(), "ns-a", "ntfy")
	if store.getCount != 2 {
		t.Fatalf("warm: %d", store.getCount)
	}

	m.InvalidateNamespace("ns-a")
	_, _ = m.Get(context.Background(), "ns-a", "apns")
	_, _ = m.Get(context.Background(), "ns-a", "ntfy")
	if store.getCount != 4 {
		t.Errorf("expected both providers re-read after namespace invalidate; getCount=%d", store.getCount)
	}
}

func TestManager_Get_rejectsEmptyInputs(t *testing.T) {
	m := NewManager(newFakeStore(), nil)
	if _, err := m.Get(context.Background(), "", "apns"); !errors.Is(err, ErrInvalidNamespace) {
		t.Errorf("empty namespace: got %v, want ErrInvalidNamespace", err)
	}
	if _, err := m.Get(context.Background(), "ns-a", ""); !errors.Is(err, ErrInvalidProvider) {
		t.Errorf("empty provider: got %v, want ErrInvalidProvider", err)
	}
}

func TestManager_Get_concurrentBuildsAreSafe(t *testing.T) {
	// This test asserts CORRECTNESS under concurrency, not maximum
	// store-hit reduction. The current implementation deliberately
	// doesn't single-flight cold loads (no per-key mutex) — under a
	// thundering herd, up to N goroutines can each hit the store
	// before the first one populates the cache. That's an acceptable
	// trade-off: the alternative (single-flight) adds complexity for
	// a workload (credential lookups) where store hits are cheap
	// (sub-ms) and contention is rare (cred changes are rare).
	//
	// What we verify here is:
	//   1. No goroutine returns an error
	//   2. Every goroutine sees the SAME credential (no torn reads)
	//   3. After settle, the cache is populated (subsequent lookup
	//      should be 0 additional store hits)
	store := newFakeStore()
	_ = store.Upsert(context.Background(), Credential{
		Namespace: "ns-a", Provider: "apns", JSON: []byte(`{"k":"v"}`),
	})
	m := NewManager(store, nil)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			c, err := m.Get(context.Background(), "ns-a", "apns")
			if err != nil {
				errs <- err
				return
			}
			results <- string(c.JSON)
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Errorf("concurrent Get failed: %v", err)
	}
	for got := range results {
		if got != `{"k":"v"}` {
			t.Errorf("torn read: got %q", got)
		}
	}

	// After settle, the cache MUST be populated — a fresh lookup hits
	// no additional store reads.
	before := store.getCount
	if _, err := m.Get(context.Background(), "ns-a", "apns"); err != nil {
		t.Fatalf("post-settle Get: %v", err)
	}
	if store.getCount != before {
		t.Errorf("post-settle Get should be cache hit; before=%d after=%d", before, store.getCount)
	}
}
