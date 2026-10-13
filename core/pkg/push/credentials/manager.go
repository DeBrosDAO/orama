package credentials

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager is the read-side entry point for per-namespace, per-provider
// credentials. Provider packages call Manager.Get to load credentials
// at push-send time; the LRU+TTL cache eliminates per-call decryption
// for the (almost always) cache-hit path.
//
// Cache invalidation (defense in depth):
//
//   - Immediate (this-gateway): the HTTP handler calls Invalidate(ns,
//     provider) after PUT/DELETE so the next lookup on THIS gateway
//     rebuilds from store.
//   - Bounded staleness (cluster-wide): every cached entry expires
//     after cacheEntryTTL (30s) and is reloaded from the store on the
//     next call. Bounds the window during which a config change on
//     gateway A is invisible to gateway B without requiring a pub/sub
//     broadcast layer. Same model as pkg/ratelimit.
//
// Safe for concurrent use.
type Manager struct {
	store  Store
	logger *zap.Logger
	ttl    time.Duration // configurable for tests; defaults to cacheEntryTTL

	mu       sync.Mutex
	cache    map[cacheKey]*list.Element
	lru      *list.List
	cacheCap int
}

// cacheKey is (namespace, provider) — the natural primary key.
type cacheKey struct {
	namespace string
	provider  string
}

// cacheEntry is the LRU node payload.
type cacheEntry struct {
	key     cacheKey
	cred    *Credential // nil means "no row" (negative cache)
	builtAt time.Time
}

// NewManager constructs a Manager backed by the given store.
func NewManager(store Store, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		store:    store,
		logger:   logger,
		ttl:      cacheEntryTTL,
		cache:    make(map[cacheKey]*list.Element, defaultCacheCap),
		lru:      list.New(),
		cacheCap: defaultCacheCap,
	}
}

// SetCacheTTL overrides the default cache-entry TTL. Intended for tests
// (where 30s is too long to wait) and for operators who want a tighter
// propagation window across multi-gateway deployments. A non-positive
// argument is a no-op.
func (m *Manager) SetCacheTTL(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ttl = d
}

// Get returns the credential for (namespace, provider) or (nil, nil) if
// no credential is configured. A store error is returned to the caller
// — unlike rate limiting (where we fail open under a store error), a
// missing push credential MUST surface so the caller doesn't silently
// drop a message to a misconfigured provider.
func (m *Manager) Get(ctx context.Context, namespace, provider string) (*Credential, error) {
	if namespace == "" {
		return nil, ErrInvalidNamespace
	}
	if provider == "" {
		return nil, ErrInvalidProvider
	}
	key := cacheKey{namespace: namespace, provider: provider}

	m.mu.Lock()
	if el, ok := m.cache[key]; ok {
		entry := el.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(el)
			m.mu.Unlock()
			return entry.cred, nil
		}
		// Expired — drop and fall through to rebuild.
		m.lru.Remove(el)
		delete(m.cache, key)
	}
	m.mu.Unlock()

	cred, err := m.store.Get(ctx, namespace, provider)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Store ErrNotFound → cache a negative (nil cred) entry so we don't
	// hammer rqlite for "namespace doesn't use this provider" on the hot
	// send path. The TTL still expires the negative entry, so once a
	// tenant DOES configure the provider, latency to first-effective is
	// bounded by the TTL.

	m.mu.Lock()
	defer m.mu.Unlock()

	// Recheck under lock — another goroutine may have built one
	// concurrently. Use it if it's still fresh.
	if el, ok := m.cache[key]; ok {
		entry := el.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(el)
			return entry.cred, nil
		}
		m.lru.Remove(el)
		delete(m.cache, key)
	}

	entry := &cacheEntry{key: key, cred: cred, builtAt: time.Now()}
	el := m.lru.PushFront(entry)
	m.cache[key] = el
	for m.lru.Len() > m.cacheCap {
		tail := m.lru.Back()
		if tail == nil {
			break
		}
		m.lru.Remove(tail)
		delete(m.cache, tail.Value.(*cacheEntry).key)
	}
	return cred, nil
}

// Invalidate evicts the cached entry for (namespace, provider). Called
// by the HTTP handler after PUT/DELETE so the next Get reloads from
// the store.
func (m *Manager) Invalidate(namespace, provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := cacheKey{namespace: namespace, provider: provider}
	if el, ok := m.cache[key]; ok {
		m.lru.Remove(el)
		delete(m.cache, key)
	}
}

// InvalidateNamespace evicts every cached entry for the given namespace,
// regardless of provider. Used when a namespace is deleted wholesale or
// during an admin "rotate all credentials" operation.
func (m *Manager) InvalidateNamespace(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, el := range m.cache {
		if k.namespace == namespace {
			m.lru.Remove(el)
			delete(m.cache, k)
		}
	}
}

// Store returns the underlying store. Used by the HTTP handlers for
// write paths (PUT/DELETE) which go straight to the store and then
// Invalidate; reads of cached state remain on the Manager.
func (m *Manager) Store() Store {
	return m.store
}
