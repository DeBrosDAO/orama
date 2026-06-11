package ratelimit

import (
	"container/list"
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager is the entry point for per-namespace rate limiting. Every
// request goes through Allow(namespace), which:
//
//  1. Returns from the LRU cache if we've already built a limiter for
//     this namespace AND the entry hasn't aged past `cacheEntryTTL`.
//  2. On cache miss (or expired entry), asks the ConfigStore for an
//     override. If present, uses (override.RequestsPerMinute,
//     override.Burst). If absent, uses Defaults.RequestsPerMinute /
//     Defaults.Burst.
//  3. Builds a token-bucket limiter from those values, inserts into the
//     LRU, and consults it.
//
// Cache invalidation strategies (defense in depth):
//
//   - Immediate (this-gateway): the config handler calls Invalidate(ns)
//     after PUT/DELETE so the next request on THIS gateway rebuilds.
//   - Bounded staleness (cluster-wide): every cached entry expires after
//     `cacheEntryTTL` (default 30s) and is rebuilt from the latest store
//     value. This bounds how long a config change can be invisible on
//     gateways that didn't handle the PUT — without requiring a
//     pub-sub broadcast layer.
//
// Per-gateway-bucket semantics (KNOWN BEHAVIOUR):
//
// Each gateway runs its own Manager and therefore its own per-namespace
// token bucket. In an N-gateway deployment, the effective cluster-wide
// rate cap for a namespace is N × the configured limit, since the
// buckets don't share state. This is intentional for v1 (no shared
// bucket store; per-gateway buckets are simple, fast, and survive
// gateway-to-gateway partitions). Callers that need a cluster-wide cap
// should either set the per-gateway limit to (cluster-cap / N) or
// implement a shared-bucket backend in a follow-up.
//
// Safe for concurrent use.
type Manager struct {
	store    ConfigStore
	defaults Defaults
	logger   *zap.Logger
	ttl      time.Duration // configurable for tests; defaults to cacheEntryTTL

	mu       sync.Mutex
	cache    map[string]*list.Element
	lru      *list.List
	cacheCap int
}

// cacheEntry tracks ONE namespace's compiled limiter plus the time it
// was built. Once `age > Manager.ttl`, the next Allow rebuilds from the
// store — covers the "config changed on gateway A, gateway B still
// cached" multi-gateway gap with a bounded propagation window.
type cacheEntry struct {
	namespace string
	limiter   *bucketLimiter
	builtAt   time.Time
}

// defaultCacheCap caps how many namespaces' limiters we hold in memory.
// Each is small (~few hundred bytes); 1024 is generous and bounds memory
// under abuse.
const defaultCacheCap = 1024

// cacheEntryTTL bounds how long a stale entry can serve before the next
// Allow re-reads the config store. 30s is short enough that operator
// config changes propagate quickly across the cluster, and long enough
// that the store isn't hit on every request for a busy namespace.
const cacheEntryTTL = 30 * time.Second

// NewManager constructs a Manager. Defaults provides both the fallback
// values (when a namespace has no override) AND the operator-imposed
// ceiling on tenant PUT requests (handled by the config handler, not
// here).
func NewManager(store ConfigStore, defaults Defaults, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		store:    store,
		defaults: defaults.Sane(),
		logger:   logger,
		ttl:      cacheEntryTTL,
		cache:    make(map[string]*list.Element, defaultCacheCap),
		lru:      list.New(),
		cacheCap: defaultCacheCap,
	}
}

// SetCacheTTL overrides the default cache-entry TTL. Intended for tests
// (where 30 s is too long to wait) and for operators who want a tighter
// propagation window across multi-gateway deployments at the cost of
// extra store reads. Passing a non-positive value is a no-op.
func (m *Manager) SetCacheTTL(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ttl = d
}

// Allow returns true if a request for the given namespace should be
// allowed under that namespace's rate limit. The empty namespace is
// always allowed (interpreted as "no namespace context — skip the check
// at this layer; per-IP rate limiter still applies upstream").
//
// A store lookup error degrades to the gateway-wide defaults — we
// prefer "let the request through under the safe default" over "deny
// the request because the config store is briefly unavailable."
func (m *Manager) Allow(ctx context.Context, namespace string) bool {
	if namespace == "" {
		return true
	}
	limiter := m.getOrBuild(ctx, namespace)
	return limiter.allow()
}

// Invalidate evicts the cached limiter for a namespace. Called by the
// config handler after a successful PUT or DELETE so the next request
// rebuilds with current config.
func (m *Manager) Invalidate(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.cache[namespace]; ok {
		m.lru.Remove(el)
		delete(m.cache, namespace)
	}
}

// Defaults returns the manager's effective defaults. Used by the config
// handler to surface the operator ceiling in GET responses and validate
// PUT requests.
func (m *Manager) Defaults() Defaults {
	return m.defaults
}

// getOrBuild reads or constructs the limiter for the given namespace.
// On cache miss OR expired entry (age > ttl), reads the store, builds
// a fresh limiter, and replaces the cache slot. The TTL is what bounds
// cross-gateway config staleness — see Manager doc.
func (m *Manager) getOrBuild(ctx context.Context, namespace string) *bucketLimiter {
	m.mu.Lock()
	if el, ok := m.cache[namespace]; ok {
		entry := el.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(el)
			m.mu.Unlock()
			return entry.limiter
		}
		// Expired — drop the stale entry, fall through to rebuild.
		m.lru.Remove(el)
		delete(m.cache, namespace)
	}
	m.mu.Unlock()

	// Cache miss (or expired): look up override, fall back to defaults,
	// build limiter.
	rpm, burst := m.defaults.RequestsPerMinute, m.defaults.Burst
	if m.store != nil {
		cfg, err := m.store.Get(ctx, namespace)
		if err != nil {
			// Store error: log and fall through to defaults. Refusing
			// the request because the DB is briefly unreachable is the
			// wrong failure mode for a rate limiter.
			m.logger.Warn("rate-limit config Get failed; using defaults",
				zap.String("namespace", namespace),
				zap.Error(err))
		} else if cfg != nil {
			if cfg.RequestsPerMinute > 0 {
				rpm = cfg.RequestsPerMinute
			}
			if cfg.Burst > 0 {
				burst = cfg.Burst
			}
		}
	}

	limiter := newBucketLimiter(rpm, burst)

	// Insert into cache under lock; evict LRU tail if over cap.
	m.mu.Lock()
	defer m.mu.Unlock()
	// Another goroutine may have built it concurrently — return their
	// copy if so to keep one limiter per namespace. A concurrent rebuild
	// that already replaced an expired entry is also handled here.
	if el, ok := m.cache[namespace]; ok {
		entry := el.Value.(*cacheEntry)
		if time.Since(entry.builtAt) < m.ttl {
			m.lru.MoveToFront(el)
			return entry.limiter
		}
		// Concurrent build also expired — replace.
		m.lru.Remove(el)
		delete(m.cache, namespace)
	}
	entry := &cacheEntry{
		namespace: namespace,
		limiter:   limiter,
		builtAt:   time.Now(),
	}
	el := m.lru.PushFront(entry)
	m.cache[namespace] = el
	for m.lru.Len() > m.cacheCap {
		tail := m.lru.Back()
		if tail == nil {
			break
		}
		m.lru.Remove(tail)
		delete(m.cache, tail.Value.(*cacheEntry).namespace)
	}
	return limiter
}

// bucketLimiter is a token-bucket rate limiter. Local to this package so
// the package's behaviour is self-contained and the legacy gateway
// RateLimiter in pkg/gateway can be retired once the wiring switches
// over. Tokens-per-second is the sustained rate; burst is the cap.
type bucketLimiter struct {
	mu        sync.Mutex
	rate      float64 // tokens per second
	burst     float64
	tokens    float64
	lastCheck time.Time
}

func newBucketLimiter(ratePerMinute, burst int) *bucketLimiter {
	return &bucketLimiter{
		rate:      float64(ratePerMinute) / 60.0,
		burst:     float64(burst),
		tokens:    float64(burst),
		lastCheck: time.Now(),
	}
}

func (b *bucketLimiter) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.lastCheck = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
