package serverless

// ratelimit.go provides a multi-tier token-bucket rate limiter for serverless
// function invocations. Three tiers, applied in order; first rejection wins:
//
//   1. Per-(namespace, function, wallet) — only when a function declares an override
//   2. Per-(namespace, wallet) — gateway-wide default per-user limit
//   3. Per-namespace total — protects against single-namespace exhaustion
//
// Anonymous callers (no wallet) fall back to per-IP buckets at tier 2.
//
// Per-bucket state is held in a sharded LRU to bound memory: configurable
// MaxBucketsPerScope (default 100k per tier) — buckets evicted are
// effectively "limit reset for that key" which is acceptable.
//
// See plan: core/plans/platform/09_PER_WALLET_RATE_LIMIT.md.

import (
	"container/list"
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// RateLimitRequest holds the inputs for a single Allow check.
type RateLimitRequest struct {
	Namespace string
	Function  string
	Wallet    string                  // empty = anonymous; per-wallet tier falls back to per-IP
	IP        string                  // remote IP, used when Wallet is empty
	Override  *PerFunctionRateLimit  // optional per-function tightening
}

// PerFunctionRateLimit overrides the default per-wallet limits for one function.
// Set zero values to inherit defaults.
type PerFunctionRateLimit struct {
	PerWalletPerMinute int
	PerWalletBurst     int
}

// LimiterConfig holds gateway-wide rate-limiter defaults. Zero values mean
// "use the built-in default" — see DefaultLimiterConfig.
type LimiterConfig struct {
	// Per-(namespace, wallet) defaults
	PerWalletPerMinute int
	PerWalletBurst     int

	// Per-(namespace) ceiling
	PerNamespacePerMinute int
	PerNamespaceBurst     int

	// Per-IP bucket for anonymous callers
	PerIPPerMinute int
	PerIPBurst     int

	// LRU cap for tracked buckets per tier
	MaxBucketsPerScope int
}

// DefaultLimiterConfig returns a sensible default config. Tuned for typical
// per-user app load with headroom for short bursts.
func DefaultLimiterConfig() LimiterConfig {
	return LimiterConfig{
		PerWalletPerMinute:    600,    // 10/sec sustained
		PerWalletBurst:        60,     // 60-token burst window
		PerNamespacePerMinute: 60_000, // 1k/sec sustained per namespace
		PerNamespaceBurst:     6000,
		PerIPPerMinute:        120,    // 2/sec for anonymous
		PerIPBurst:            30,
		MaxBucketsPerScope:    100_000,
	}
}

// Decision is the result of an Allow check.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Scope      string // "per_function_wallet" | "per_wallet" | "per_namespace" | "per_ip"
}

// RateLimitedError is returned by the engine when a request is rejected.
// Carries the retry-after for the gateway HTTP layer to serve as Retry-After.
type RateLimitedError struct {
	Scope      string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limit exceeded (scope=%s, retry_after=%s)", e.Scope, e.RetryAfter)
}

// MultiTierLimiter implements RateLimiter as three layered token-bucket
// scopes with sharded LRU bucket tracking.
type MultiTierLimiter struct {
	cfg LimiterConfig

	// Tier buckets — each scope owns its own LRU.
	fnWalletBuckets *lruBuckets // (ns, fn, wallet) — only populated when override exists
	walletBuckets   *lruBuckets // (ns, wallet)
	namespaceBuckets *lruBuckets // (ns)
	ipBuckets       *lruBuckets // (ns, ip) — for anonymous callers
}

// NewMultiTierLimiter constructs a limiter with the given config. Pass
// DefaultLimiterConfig() for sensible defaults; override fields as needed.
func NewMultiTierLimiter(cfg LimiterConfig) *MultiTierLimiter {
	if cfg.PerWalletPerMinute <= 0 {
		cfg.PerWalletPerMinute = 600
	}
	if cfg.PerWalletBurst <= 0 {
		cfg.PerWalletBurst = 60
	}
	if cfg.PerNamespacePerMinute <= 0 {
		cfg.PerNamespacePerMinute = 60_000
	}
	if cfg.PerNamespaceBurst <= 0 {
		cfg.PerNamespaceBurst = 6000
	}
	if cfg.PerIPPerMinute <= 0 {
		cfg.PerIPPerMinute = 120
	}
	if cfg.PerIPBurst <= 0 {
		cfg.PerIPBurst = 30
	}
	if cfg.MaxBucketsPerScope <= 0 {
		cfg.MaxBucketsPerScope = 100_000
	}
	return &MultiTierLimiter{
		cfg:              cfg,
		fnWalletBuckets:  newLRUBuckets(cfg.MaxBucketsPerScope),
		walletBuckets:    newLRUBuckets(cfg.MaxBucketsPerScope),
		namespaceBuckets: newLRUBuckets(cfg.MaxBucketsPerScope),
		ipBuckets:        newLRUBuckets(cfg.MaxBucketsPerScope),
	}
}

// AllowRequest returns the layered decision. The first tier to reject wins;
// on rejection, RetryAfter is the wait until that tier could accept again.
//
// This is the rich path; the engine prefers it via type assertion. The
// legacy `Allow(ctx, key)` method below remains for back-compat with the
// simple RateLimiter interface.
func (l *MultiTierLimiter) AllowRequest(ctx context.Context, req RateLimitRequest) (Decision, error) {
	// Tier 1: per-(ns, fn, wallet) override — only when the function declared one.
	if req.Override != nil && req.Override.PerWalletPerMinute > 0 && req.Wallet != "" {
		key := req.Namespace + "/" + req.Function + "/" + req.Wallet
		burst := req.Override.PerWalletBurst
		if burst <= 0 {
			burst = req.Override.PerWalletPerMinute / 10
			if burst <= 0 {
				burst = 1
			}
		}
		if d := l.fnWalletBuckets.tryConsume(key,
			float64(req.Override.PerWalletPerMinute)/60.0,
			float64(burst)); !d.Allowed {
			d.Scope = "per_function_wallet"
			return d, nil
		}
	}

	// Tier 2: per-(ns, wallet) OR per-(ns, ip) for anonymous.
	if req.Wallet != "" {
		key := req.Namespace + "/" + req.Wallet
		if d := l.walletBuckets.tryConsume(key,
			float64(l.cfg.PerWalletPerMinute)/60.0,
			float64(l.cfg.PerWalletBurst)); !d.Allowed {
			d.Scope = "per_wallet"
			return d, nil
		}
	} else if req.IP != "" {
		key := req.Namespace + "/" + req.IP
		if d := l.ipBuckets.tryConsume(key,
			float64(l.cfg.PerIPPerMinute)/60.0,
			float64(l.cfg.PerIPBurst)); !d.Allowed {
			d.Scope = "per_ip"
			return d, nil
		}
	}

	// Tier 3: per-namespace total (always applies).
	if req.Namespace != "" {
		if d := l.namespaceBuckets.tryConsume(req.Namespace,
			float64(l.cfg.PerNamespacePerMinute)/60.0,
			float64(l.cfg.PerNamespaceBurst)); !d.Allowed {
			d.Scope = "per_namespace"
			return d, nil
		}
	}

	return Decision{Allowed: true}, nil
}

// Allow satisfies the legacy serverless.RateLimiter interface. Treats `key`
// as the wallet/namespace combo from older call sites. Prefer AllowRequest
// in new code.
func (l *MultiTierLimiter) Allow(ctx context.Context, key string) (bool, error) {
	d, err := l.AllowRequest(ctx, RateLimitRequest{Namespace: "_global", Wallet: key})
	return d.Allowed, err
}

// ----------------------------------------------------------------------------
// Backward compatibility: keep the old TokenBucketLimiter type as a
// single-bucket impl that satisfies the old simple interface. New code uses
// MultiTierLimiter exclusively.
// ----------------------------------------------------------------------------

// TokenBucketLimiter is a single global bucket — kept for backwards compat.
// Prefer MultiTierLimiter for any new use.
type TokenBucketLimiter struct {
	mu       sync.Mutex
	tokens   float64
	max      float64
	refill   float64
	lastTime time.Time
}

// NewTokenBucketLimiter creates a single-bucket limiter with the given per-minute limit.
func NewTokenBucketLimiter(perMinute int) *TokenBucketLimiter {
	perSecond := float64(perMinute) / 60.0
	return &TokenBucketLimiter{
		tokens:   float64(perMinute),
		max:      float64(perMinute),
		refill:   perSecond,
		lastTime: time.Now(),
	}
}

// Allow checks if a request should be allowed.
// The key argument is ignored — this is a single global bucket.
func (t *TokenBucketLimiter) Allow(_ context.Context, _ string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(t.lastTime).Seconds()
	t.lastTime = now

	t.tokens += elapsed * t.refill
	if t.tokens > t.max {
		t.tokens = t.max
	}
	if t.tokens < 1.0 {
		return false, nil
	}
	t.tokens--
	return true, nil
}

// ----------------------------------------------------------------------------
// lruBuckets: sharded map of token buckets with LRU eviction
// ----------------------------------------------------------------------------

const lruShards = 16

type lruBuckets struct {
	shards [lruShards]*bucketShard
}

type bucketShard struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	order   *list.List // each Element.Value is a string (the key); front = most recent
	keyToEl map[string]*list.Element
	cap     int
}

func newLRUBuckets(capacity int) *lruBuckets {
	per := capacity / lruShards
	if per < 1 {
		per = 1
	}
	lb := &lruBuckets{}
	for i := range lb.shards {
		lb.shards[i] = &bucketShard{
			buckets: make(map[string]*tokenBucket, per),
			order:   list.New(),
			keyToEl: make(map[string]*list.Element, per),
			cap:     per,
		}
	}
	return lb
}

// tryConsume looks up or creates the bucket for `key`, attempts to consume
// one token, and returns the decision. Updates LRU on touch and evicts the
// least-recently-used bucket when the shard is at capacity.
func (l *lruBuckets) tryConsume(key string, ratePerSec, burst float64) Decision {
	s := l.shards[shardIdx(key)]
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		// Capacity check + LRU eviction.
		if len(s.buckets) >= s.cap {
			oldest := s.order.Back()
			if oldest != nil {
				oldKey := oldest.Value.(string)
				delete(s.buckets, oldKey)
				delete(s.keyToEl, oldKey)
				s.order.Remove(oldest)
			}
		}
		b = &tokenBucket{
			tokens:     burst, // start full
			max:        burst,
			refill:     ratePerSec,
			lastRefill: time.Now(),
		}
		s.buckets[key] = b
		s.keyToEl[key] = s.order.PushFront(key)
	} else {
		// Touch LRU.
		s.order.MoveToFront(s.keyToEl[key])
		// Update rate/burst in case the function's override changed since last call.
		b.refill = ratePerSec
		b.max = burst
	}

	return b.tryConsume()
}

// tokenBucket is a leaky bucket; not safe for concurrent use without external lock.
type tokenBucket struct {
	tokens     float64
	max        float64
	refill     float64 // tokens per second
	lastRefill time.Time
}

func (b *tokenBucket) tryConsume() Decision {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.lastRefill = now
	b.tokens += elapsed * b.refill
	if b.tokens > b.max {
		b.tokens = b.max
	}
	if b.tokens < 1.0 {
		// Compute how long until we have one token.
		needed := 1.0 - b.tokens
		seconds := needed / b.refill
		return Decision{
			Allowed:    false,
			RetryAfter: time.Duration(seconds * float64(time.Second)),
		}
	}
	b.tokens--
	return Decision{Allowed: true}
}

// shardIdx hashes key to one of lruShards.
func shardIdx(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % lruShards
}
