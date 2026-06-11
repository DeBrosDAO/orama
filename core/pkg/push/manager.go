package push

// manager.go — top-level entry point for sending push notifications,
// with per-namespace provider configuration that tenants self-serve.
//
// The Manager wraps:
//   - a ConfigStore (per-namespace overrides in RQLite)
//   - a fallback Defaults (gateway YAML — the cluster-wide default if
//     a namespace hasn't set its own)
//   - an LRU cache of built dispatchers keyed by namespace
//
// Every send-path (HTTP, WASM hostfunc) goes through Manager.SendToUser
// or Manager.Send. The cache eliminates per-call config decryption +
// provider construction overhead — only the FIRST send for a namespace
// pays that cost; subsequent sends are zero-allocation lookups.
//
// Cache invalidation: HTTP config-change handlers call Invalidate after
// PUT/DELETE so the next send rebuilds with fresh config.
//
// See bug #220 follow-up. Generic by design — same pattern works for
// any future "tenant should self-serve this knob" feature.

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProviderFactory builds the set of providers for a resolved Config.
// Injected by the gateway so the push package itself doesn't import
// the provider sub-packages (avoids a circular dependency: provider
// sub-packages already depend on push for the PushProvider interface).
//
// The factory is called once per fresh dispatcher build (cache miss).
// Empty slice is allowed and means "this config produces no providers";
// Manager treats that as ErrPushNotConfigured.
//
// The ctx is the request context that triggered the (cold-path)
// dispatcher build. Factories that need to look up per-namespace
// credentials from the credentials manager (e.g. APNs) should use it
// so cancellation propagates correctly. ctx is never nil.
type ProviderFactory func(ctx context.Context, cfg Config) []PushProvider

// ErrPushNotConfigured is returned by Send when the namespace has no
// per-namespace config AND the gateway has no fallback defaults — i.e.
// nothing to send through. Distinguish from ErrNoDevices (different
// failure mode).
var ErrPushNotConfigured = errors.New("push not configured for namespace; set credentials via PUT /v1/namespace/push-credentials/{provider} or legacy /v1/push/config")

// Defaults are the gateway-YAML fallback when a namespace hasn't set its
// own config. Any field set here applies to every namespace that doesn't
// override it. Empty Defaults means "no global default — namespace must
// set their own".
type Defaults struct {
	NtfyBaseURL     string
	NtfyAuthToken   string
	ExpoAccessToken string
}

// IsEmpty returns true when no fallback provider is set.
func (d Defaults) IsEmpty() bool {
	return d.NtfyBaseURL == "" && d.ExpoAccessToken == ""
}

// Manager is the top-level push entry point. Build with NewManager and
// hand out via the gateway's dependencies. Safe for concurrent use.
//
// Cross-gateway invalidation: the per-namespace dispatcher is built
// from BOTH the per-namespace push config (legacy 026) AND any
// per-provider credentials (#72). If a tenant rotates an APNs p8 key
// on gateway A, gateway B's CACHED dispatcher still holds an APNs
// provider constructed from the OLD key — until either:
//
//   1. The dispatcher entry is evicted by LRU pressure (only when
//      activeCacheCap namespaces are also active), or
//   2. The entry's TTL elapses (cacheEntryTTL, default 30s).
//
// The TTL is the defense-in-depth bound — same model as pkg/ratelimit.
// Without it, low-traffic namespaces would never see rotated creds on
// gateway B without an explicit broadcast layer.
type Manager struct {
	store    ConfigStore
	devices  PushDeviceStore
	defaults Defaults
	factory  ProviderFactory
	logger   *zap.Logger
	ttl      time.Duration // configurable for tests

	// cache LRU of namespace → built dispatcher.
	mu       sync.Mutex
	cache    map[string]*list.Element
	lru      *list.List
	cacheCap int
}

// cacheEntry is the doubly-linked-list node + dispatcher payload.
type cacheEntry struct {
	namespace  string
	dispatcher *PushDispatcher
	builtAt    time.Time
}

// defaultCacheCap caps how many namespaces' dispatchers we hold in memory.
// Each entry is small (~few hundred bytes); 256 is generous and bounds
// memory under abuse.
const defaultCacheCap = 256

// cacheEntryTTL bounds how long a stale dispatcher can serve before the
// next dispatcherFor call rebuilds it from store + credentials. 30s
// matches pkg/ratelimit and pkg/push/credentials so config + creds
// changes propagate across the cluster within the same bounded window.
const cacheEntryTTL = 30 * time.Second

// NewManager constructs a Manager with the given device store, config
// store, fallback Defaults, and ProviderFactory.
//
// The ProviderFactory is REQUIRED — it tells the manager how to turn a
// resolved Config into PushProvider instances. The gateway's
// dependencies wires this up (it imports the provider sub-packages and
// returns the right providers for each config field that's populated).
func NewManager(devices PushDeviceStore, store ConfigStore, defaults Defaults, factory ProviderFactory, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		store:    store,
		devices:  devices,
		defaults: defaults,
		factory:  factory,
		logger:   logger,
		ttl:      cacheEntryTTL,
		cache:    make(map[string]*list.Element, defaultCacheCap),
		lru:      list.New(),
		cacheCap: defaultCacheCap,
	}
}

// SetCacheTTL overrides the default dispatcher cache TTL. Intended
// for tests (where 30s is too long) and for operators who want a
// tighter cross-gateway propagation window. Non-positive values are
// ignored.
func (m *Manager) SetCacheTTL(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ttl = d
}

// SendToUser dispatches a push to every device registered for the user
// in the given namespace. Looks up per-namespace config (or falls back
// to defaults), builds the appropriate dispatcher, and sends.
//
// Returns:
//   - nil on success or "no devices" (per existing PushDispatcher
//     contract — sending push to a user with zero devices is a no-op)
//   - ErrPushNotConfigured when neither namespace config nor gateway
//     defaults provide a working provider
func (m *Manager) SendToUser(ctx context.Context, namespace, userID string, msg PushMessage) error {
	d, err := m.dispatcherFor(ctx, namespace)
	if err != nil {
		return err
	}
	return d.SendToUser(ctx, namespace, userID, msg)
}

// SendToUserDetailed mirrors SendToUser but returns the per-device
// outcome shape. Used by the WASM `oh.PushSendV2` host fn so callers
// can react to per-device failures (bugboard #348).
func (m *Manager) SendToUserDetailed(ctx context.Context, namespace, userID string, msg PushMessage) (*SendDetailedResult, error) {
	d, err := m.dispatcherFor(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return d.SendToUserDetailed(ctx, namespace, userID, msg)
}

// DeviceStore exposes the underlying device store so HTTP handlers
// (register/list/delete) can use it directly without going through the
// dispatcher path.
func (m *Manager) DeviceStore() PushDeviceStore {
	return m.devices
}

// IsConfigured returns true when the namespace has per-namespace config
// OR usable fallback defaults — i.e. SendToUser would not fail with
// ErrPushNotConfigured. Cheap path-check used by HTTP 503 responses.
func (m *Manager) IsConfigured(ctx context.Context, namespace string) bool {
	if !m.defaults.IsEmpty() {
		return true
	}
	if m.store == nil {
		return false
	}
	cfg, err := m.store.Get(ctx, namespace)
	if err != nil || cfg == nil {
		return false
	}
	return !cfg.IsEmpty()
}

// Invalidate evicts the cached dispatcher for a namespace. Call after a
// successful PUT/DELETE on /v1/push/config so the next SendToUser uses
// fresh config.
func (m *Manager) Invalidate(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if elem, ok := m.cache[namespace]; ok {
		m.lru.Remove(elem)
		delete(m.cache, namespace)
	}
}

// dispatcherFor returns a (cached or freshly built) dispatcher with the
// providers configured for the given namespace. Entries older than
// `ttl` are evicted on access and rebuilt — this bounds the staleness
// of credential changes that happened on another gateway.
func (m *Manager) dispatcherFor(ctx context.Context, namespace string) (*PushDispatcher, error) {
	// Fast path — already cached AND not expired.
	m.mu.Lock()
	if elem, ok := m.cache[namespace]; ok {
		entry := elem.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(elem)
			m.mu.Unlock()
			return entry.dispatcher, nil
		}
		// Expired — drop the stale entry and fall through to rebuild.
		m.lru.Remove(elem)
		delete(m.cache, namespace)
	}
	m.mu.Unlock()

	// Slow path — load config + build dispatcher.
	d, err := m.buildDispatcher(ctx, namespace)
	if err != nil {
		return nil, err
	}

	// Insert into cache (eviction if at capacity).
	m.mu.Lock()
	defer m.mu.Unlock()
	// Recheck under lock — another goroutine may have built one. Use it
	// only if it's still fresh; otherwise our newly-built one replaces.
	if elem, ok := m.cache[namespace]; ok {
		entry := elem.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(elem)
			return entry.dispatcher, nil
		}
		m.lru.Remove(elem)
		delete(m.cache, namespace)
	}
	if m.lru.Len() >= m.cacheCap {
		oldest := m.lru.Back()
		if oldest != nil {
			old := oldest.Value.(*cacheEntry)
			m.lru.Remove(oldest)
			delete(m.cache, old.namespace)
		}
	}
	entry := &cacheEntry{namespace: namespace, dispatcher: d, builtAt: time.Now()}
	m.cache[namespace] = m.lru.PushFront(entry)
	return d, nil
}

// buildDispatcher resolves the effective config for the namespace and
// constructs a fresh PushDispatcher with the right providers registered.
//
// Effective-config resolution:
//  1. Start with gateway YAML defaults
//  2. If a per-namespace row exists, override field-by-field
//  3. If the result has zero providers, return ErrPushNotConfigured
func (m *Manager) buildDispatcher(ctx context.Context, namespace string) (*PushDispatcher, error) {
	eff := Config{Namespace: namespace}
	// Start from defaults.
	eff.NtfyBaseURL = m.defaults.NtfyBaseURL
	eff.NtfyAuthToken = m.defaults.NtfyAuthToken
	eff.ExpoAccessToken = m.defaults.ExpoAccessToken

	// Layer namespace overrides if any.
	if m.store != nil {
		nc, err := m.store.Get(ctx, namespace)
		if err != nil && !errors.Is(err, ErrConfigNotFound) {
			return nil, fmt.Errorf("manager: load namespace config: %w", err)
		}
		if nc != nil {
			// Each field overrides the default if non-empty. An explicit
			// empty value via PUT clears the namespace's row entirely
			// (DELETE) — there's no "set this field to empty to clear"
			// half-state, by design.
			if nc.NtfyBaseURL != "" {
				// Defense-in-depth: a base URL stored before the SSRF guard
				// existed (or via any path that skipped it) must not point at an
				// internal/reserved literal IP. Drop the override and fall back
				// to the gateway default if it does. Literal-only (no DNS, no
				// syntax re-validation) so this stays safe on the hot build path.
				if IsInternalBaseURL(nc.NtfyBaseURL) {
					m.logger.Warn("push: ignoring namespace ntfy_base_url override (internal address)",
						zap.String("namespace", namespace), zap.String("base_url", nc.NtfyBaseURL))
				} else {
					eff.NtfyBaseURL = nc.NtfyBaseURL
				}
			}
			if nc.NtfyAuthToken != "" {
				eff.NtfyAuthToken = nc.NtfyAuthToken
			}
			if nc.ExpoAccessToken != "" {
				eff.ExpoAccessToken = nc.ExpoAccessToken
			}
		}
	}

	if m.factory == nil {
		// Defensive: a Manager built without a factory can't produce
		// providers. Programmer error; surface explicitly.
		return nil, fmt.Errorf("manager: no provider factory configured")
	}

	// Authoritative provider-presence check is at the factory output —
	// not at the resolved flat-field config — because providers can
	// also be sourced from the per-namespace credentials store
	// (feature #72: APNs is fully credentialed and has no flat field
	// here). The factory returns an empty slice when nothing is
	// configured, which we translate to ErrPushNotConfigured.
	providers := m.factory(ctx, eff)
	if len(providers) == 0 {
		return nil, ErrPushNotConfigured
	}

	d := New(m.devices, m.logger)
	for _, p := range providers {
		d.Register(p)
	}
	return d, nil
}
