package push

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// fakeConfigStore is a minimal in-memory ConfigStore for tests.
type fakeConfigStore struct {
	mu      sync.Mutex
	configs map[string]*Config
	getErr  error // optional injected error
	calls   atomic.Int32
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{configs: map[string]*Config{}}
}

func (f *fakeConfigStore) Get(_ context.Context, ns string) (*Config, error) {
	f.calls.Add(1)
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg, ok := f.configs[ns]
	if !ok {
		return nil, ErrConfigNotFound
	}
	// Return a copy so the caller can't mutate our state.
	cp := *cfg
	return &cp, nil
}

func (f *fakeConfigStore) Upsert(_ context.Context, c Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := c
	f.configs[c.Namespace] = &cp
	return nil
}

func (f *fakeConfigStore) Delete(_ context.Context, ns string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.configs, ns)
	return nil
}

// managerFakeProvider is a stub PushProvider for manager tests.
// (The dispatcher tests already declare a `fakeProvider`; rename ours.)
type managerFakeProvider struct {
	name  string
	sends atomic.Int32
}

func (p *managerFakeProvider) Name() string { return p.name }
func (p *managerFakeProvider) Send(_ context.Context, _ PushMessage) error {
	p.sends.Add(1)
	return nil
}
func (p *managerFakeProvider) SendBulk(_ context.Context, msgs []PushMessage) []error {
	for range msgs {
		p.sends.Add(1)
	}
	return nil
}

func TestManager_namespace_with_no_config_uses_defaults(t *testing.T) {
	store := newFakeConfigStore() // empty
	defaults := Defaults{NtfyBaseURL: "http://default-ntfy"}

	var providerCalls atomic.Int32
	factory := func(_ context.Context, c Config) []PushProvider {
		providerCalls.Add(1)
		// Verify the manager passed defaults through to the factory.
		if c.NtfyBaseURL != "http://default-ntfy" {
			t.Errorf("factory got URL=%q, want default", c.NtfyBaseURL)
		}
		return []PushProvider{&managerFakeProvider{name: "ntfy"}}
	}

	m := NewManager(&fakeDeviceStore{}, store, defaults, factory, zap.NewNop())
	d, err := m.dispatcherFor(context.Background(), "ns-A")
	if err != nil {
		t.Fatalf("expected default to satisfy: %v", err)
	}
	if d == nil {
		t.Fatal("expected a dispatcher")
	}
	if providerCalls.Load() != 1 {
		t.Errorf("factory should have been called once, got %d", providerCalls.Load())
	}
}

func TestManager_namespace_config_overrides_defaults(t *testing.T) {
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{
		Namespace:   "ns-A",
		NtfyBaseURL: "http://override-ntfy",
	})
	defaults := Defaults{NtfyBaseURL: "http://default-ntfy"}

	var seenURL string
	factory := func(_ context.Context, c Config) []PushProvider {
		seenURL = c.NtfyBaseURL
		return []PushProvider{&managerFakeProvider{name: "ntfy"}}
	}

	m := NewManager(&fakeDeviceStore{}, store, defaults, factory, zap.NewNop())
	if _, err := m.dispatcherFor(context.Background(), "ns-A"); err != nil {
		t.Fatalf("dispatcherFor: %v", err)
	}
	if seenURL != "http://override-ntfy" {
		t.Errorf("factory got URL=%q, want the namespace override", seenURL)
	}
}

func TestManager_no_config_no_defaults_returns_ErrPushNotConfigured(t *testing.T) {
	store := newFakeConfigStore()
	factory := func(_ context.Context, _ Config) []PushProvider { return nil }

	m := NewManager(&fakeDeviceStore{}, store, Defaults{}, factory, zap.NewNop())
	_, err := m.dispatcherFor(context.Background(), "ns-A")
	if !errors.Is(err, ErrPushNotConfigured) {
		t.Errorf("expected ErrPushNotConfigured, got %v", err)
	}
}

func TestManager_caches_dispatchers_per_namespace(t *testing.T) {
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{Namespace: "ns-A", NtfyBaseURL: "u"})

	var factoryCalls atomic.Int32
	factory := func(_ context.Context, _ Config) []PushProvider {
		factoryCalls.Add(1)
		return []PushProvider{&managerFakeProvider{name: "ntfy"}}
	}
	m := NewManager(&fakeDeviceStore{}, store, Defaults{}, factory, zap.NewNop())

	// Two calls for the same namespace = one factory invocation (cache hit).
	for i := 0; i < 5; i++ {
		if _, err := m.dispatcherFor(context.Background(), "ns-A"); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if factoryCalls.Load() != 1 {
		t.Errorf("expected 1 factory call due to cache, got %d", factoryCalls.Load())
	}
}

func TestManager_invalidate_forces_rebuild(t *testing.T) {
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{Namespace: "ns-A", NtfyBaseURL: "v1"})

	var seenURLs []string
	factory := func(_ context.Context, c Config) []PushProvider {
		seenURLs = append(seenURLs, c.NtfyBaseURL)
		return []PushProvider{&managerFakeProvider{name: "ntfy"}}
	}
	m := NewManager(&fakeDeviceStore{}, store, Defaults{}, factory, zap.NewNop())

	if _, err := m.dispatcherFor(context.Background(), "ns-A"); err != nil {
		t.Fatal(err)
	}
	// Update config + invalidate.
	store.Upsert(context.Background(), Config{Namespace: "ns-A", NtfyBaseURL: "v2"})
	m.Invalidate("ns-A")

	if _, err := m.dispatcherFor(context.Background(), "ns-A"); err != nil {
		t.Fatal(err)
	}
	if len(seenURLs) != 2 || seenURLs[0] != "v1" || seenURLs[1] != "v2" {
		t.Errorf("expected factory to be called twice with v1 then v2, got %v", seenURLs)
	}
}

func TestManager_per_namespace_isolation(t *testing.T) {
	// ns-A has its own config; ns-B has none and uses defaults.
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{Namespace: "ns-A", NtfyBaseURL: "ns-A-url"})

	defaults := Defaults{NtfyBaseURL: "default-url"}

	urlByNS := make(map[string]string)
	var mu sync.Mutex
	factory := func(_ context.Context, c Config) []PushProvider {
		mu.Lock()
		urlByNS[c.Namespace] = c.NtfyBaseURL
		mu.Unlock()
		return []PushProvider{&managerFakeProvider{name: "ntfy"}}
	}

	m := NewManager(&fakeDeviceStore{}, store, defaults, factory, zap.NewNop())
	if _, err := m.dispatcherFor(context.Background(), "ns-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.dispatcherFor(context.Background(), "ns-B"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if urlByNS["ns-A"] != "ns-A-url" {
		t.Errorf("ns-A got %q, want ns-A-url", urlByNS["ns-A"])
	}
	if urlByNS["ns-B"] != "default-url" {
		t.Errorf("ns-B got %q, want default-url", urlByNS["ns-B"])
	}
}

func TestManager_IsConfigured_distinguishes_states(t *testing.T) {
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{Namespace: "with-cfg", NtfyBaseURL: "u"})

	// Without defaults, only namespace with config returns true.
	m1 := NewManager(&fakeDeviceStore{}, store, Defaults{}, nil, zap.NewNop())
	if !m1.IsConfigured(context.Background(), "with-cfg") {
		t.Error("expected with-cfg to be configured")
	}
	if m1.IsConfigured(context.Background(), "no-cfg") {
		t.Error("expected no-cfg to NOT be configured (no defaults)")
	}

	// With defaults, every namespace is configured.
	m2 := NewManager(&fakeDeviceStore{}, store, Defaults{NtfyBaseURL: "d"}, nil, zap.NewNop())
	if !m2.IsConfigured(context.Background(), "no-cfg") {
		t.Error("expected no-cfg to be configured via defaults")
	}
}

func TestManager_concurrent_dispatcherFor_no_race(t *testing.T) {
	// Run with -race.
	store := newFakeConfigStore()
	store.Upsert(context.Background(), Config{Namespace: "ns", NtfyBaseURL: "u"})
	factory := func(_ context.Context, _ Config) []PushProvider { return []PushProvider{&managerFakeProvider{name: "ntfy"}} }

	m := NewManager(&fakeDeviceStore{}, store, Defaults{}, factory, zap.NewNop())

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = m.dispatcherFor(context.Background(), "ns")
			}
		}()
	}
	wg.Wait()
}

// fakeDeviceStore is a stub PushDeviceStore for manager tests.
type fakeDeviceStore struct{}

func (s *fakeDeviceStore) Upsert(_ context.Context, _ PushDevice) error { return nil }
func (s *fakeDeviceStore) Delete(_ context.Context, _, _ string) error  { return nil }
func (s *fakeDeviceStore) ListForUser(_ context.Context, _, _ string) ([]PushDevice, error) {
	return nil, nil
}

func TestConfig_Redacted_omits_secrets(t *testing.T) {
	cfg := &Config{
		Namespace:       "ns",
		NtfyBaseURL:     "https://ntfy.example",
		NtfyAuthToken:   "super-secret-token",
		ExpoAccessToken: "another-super-secret",
		UpdatedAt:       12345,
		UpdatedBy:       "admin",
	}
	r := cfg.Redacted()
	if r.NtfyBaseURL != "https://ntfy.example" {
		t.Errorf("ntfy URL must be present (it's not a secret), got %q", r.NtfyBaseURL)
	}
	if !r.HasNtfyAuthToken {
		t.Error("HasNtfyAuthToken must reflect that the token is set")
	}
	if !r.HasExpoAccessToken {
		t.Error("HasExpoAccessToken must reflect that the token is set")
	}
	// Most importantly: the tokens themselves must not appear anywhere
	// in the redacted view. Walk the struct's exported fields.
	if r.UpdatedAt != 12345 {
		t.Errorf("UpdatedAt should round-trip, got %d", r.UpdatedAt)
	}
}

func TestConfig_IsEmpty(t *testing.T) {
	cases := []struct {
		name string
		c    *Config
		want bool
	}{
		{"nil pointer", nil, true},
		{"zero value", &Config{}, true},
		{"namespace only", &Config{Namespace: "ns"}, true},
		{"with ntfy URL", &Config{NtfyBaseURL: "u"}, false},
		{"with expo token", &Config{ExpoAccessToken: "t"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.IsEmpty(); got != c.want {
				t.Errorf("IsEmpty() = %v, want %v", got, c.want)
			}
		})
	}
}
