package rqlite

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// Serve-stale bounds.
const (
	// StaleWindow is how long past its TTL an answer may still be served when
	// the backend cannot be reached.
	//
	// DNS is the last thing that should fail when the database does, and it was
	// the first: any backend error became SERVFAIL for the whole zone, so an
	// index rqlite with no leader took every name in the fleet offline —
	// including the names an operator needs to reach the machines and fix it.
	// A day-old answer is very nearly always right, and is unambiguously better
	// than no answer.
	StaleWindow = 24 * time.Hour

	// StaleTTL is the TTL attached to a stale answer. Short, so a resolver
	// comes back promptly once the backend recovers.
	StaleTTL = 30 * time.Second

	// NegativeTTL is how long an NXDOMAIN is cached.
	//
	// Without it, a flood of random subdomains is a query amplifier pointed
	// straight at index rqlite: every one missed the cache and became a
	// database round trip. Short enough that a name appearing for the first
	// time resolves quickly.
	NegativeTTL = 30 * time.Second
)

// CacheEntry is a cached DNS response and the two deadlines that govern it.
type CacheEntry struct {
	msg *dns.Msg

	// expiresAt is when the answer stops being fresh.
	expiresAt time.Time

	// staleUntil is when it stops being usable at all. Between the two, the
	// answer is served only when the backend cannot be reached.
	staleUntil time.Time

	// negative marks a cached NXDOMAIN.
	negative bool
}

// Fresh reports whether the entry may be served without qualification.
func (e *CacheEntry) Fresh(now time.Time) bool { return now.Before(e.expiresAt) }

// Usable reports whether the entry may still be served as a stale answer.
func (e *CacheEntry) Usable(now time.Time) bool { return now.Before(e.staleUntil) }

// Cache implements a simple in-memory DNS response cache with serve-stale.
type Cache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
	maxSize int
	ttl     time.Duration

	// Counters are atomic because Get reads under an RLock, and incrementing a
	// plain uint64 there is a data race that `go test -race` catches.
	hitCount   atomic.Uint64
	missCount  atomic.Uint64
	staleCount atomic.Uint64
}

// NewCache creates a new DNS response cache.
func NewCache(maxSize int, ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
	go c.cleanup()
	return c
}

// Get returns a FRESH cached message and whether it is a cached NXDOMAIN.
//
// The caller needs the second value: a cached negative answer must be replied
// with RcodeNameError, and returning it as a success would turn every cached
// NXDOMAIN into an empty NOERROR — a different answer, which resolvers cache
// differently.
func (c *Cache) Get(qname string, qtype uint16) (msg *dns.Msg, negative bool) {
	entry := c.lookup(qname, qtype)
	if entry == nil || !entry.Fresh(time.Now()) {
		c.missCount.Add(1)
		return nil, false
	}
	c.hitCount.Add(1)
	return entry.msg.Copy(), entry.negative
}

// GetEntry returns the cached entry whatever its state, so a caller that has
// just failed to reach the backend can decide to serve it stale.
func (c *Cache) GetEntry(qname string, qtype uint16) *CacheEntry {
	return c.lookup(qname, qtype)
}

// GetStale returns an expired-but-usable answer with a short TTL, or nil.
//
// Only for use after the backend has actually failed: serving stale when a
// fresh answer was available would hide a record change indefinitely.
func (c *Cache) GetStale(qname string, qtype uint16) *dns.Msg {
	entry := c.lookup(qname, qtype)
	if entry == nil || !entry.Usable(time.Now()) {
		return nil
	}

	c.staleCount.Add(1)
	msg := entry.msg.Copy()
	for _, rr := range msg.Answer {
		rr.Header().Ttl = uint32(StaleTTL.Seconds())
	}
	return msg
}

func (c *Cache) lookup(qname string, qtype uint16) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[c.key(qname, qtype)]
}

// Set stores a DNS message.
func (c *Cache) Set(qname string, qtype uint16, msg *dns.Msg) {
	c.store(qname, qtype, msg, c.ttl, false)
}

// SetNegative caches an NXDOMAIN for a short time.
func (c *Cache) SetNegative(qname string, qtype uint16, msg *dns.Msg) {
	c.store(qname, qtype, msg, NegativeTTL, true)
}

func (c *Cache) store(qname string, qtype uint16, msg *dns.Msg, ttl time.Duration, negative bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	now := time.Now()
	entry := &CacheEntry{
		msg:       msg.Copy(),
		expiresAt: now.Add(ttl),
		negative:  negative,
	}

	// A negative answer is never served stale. "This name does not exist" is
	// exactly the answer most likely to be wrong later — a namespace being
	// provisioned right now — and serving it for a day would keep a new record
	// invisible long after it appeared.
	if !negative {
		entry.staleUntil = now.Add(StaleWindow)
	} else {
		entry.staleUntil = entry.expiresAt
	}

	c.entries[c.key(qname, qtype)] = entry
}

// key generates a cache key from qname and qtype.
func (c *Cache) key(qname string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", qname, qtype)
}

// evictOldest removes the entry closest to being unusable.
//
// Ordered on staleUntil, not expiresAt: an entry past its TTL is still the
// thing standing between a backend outage and SERVFAIL, so evicting it while
// something genuinely dead is still held would be backwards.
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.entries {
		if first || entry.staleUntil.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.staleUntil
			first = false
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// cleanup periodically removes entries that are no longer usable even as a
// stale answer.
func (c *Cache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if !entry.Usable(now) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

// Stats returns cache statistics.
func (c *Cache) Stats() (hits, misses uint64, size int) {
	c.mu.RLock()
	size = len(c.entries)
	c.mu.RUnlock()
	return c.hitCount.Load(), c.missCount.Load(), size
}

// StaleServed returns how many answers have been served past their TTL, which
// is the signal that the backend has been unreachable.
func (c *Cache) StaleServed() uint64 { return c.staleCount.Load() }

// Clear removes all entries from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*CacheEntry)
}
