package rqlite

import (
	"container/list"
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

	// WildcardStaleWindow is how long past its TTL an answer a wildcard
	// synthesised may still be served when the backend cannot be reached.
	//
	// Shorter than StaleWindow on purpose. Any name under a wildcard gets an
	// answer, so a flood of random names fills the cache with wildcard answers,
	// and a 24-hour window would let them outlive (and, at the size limit,
	// displace) the entries for names that really exist. Eviction goes by the
	// stale deadline, so these leave first. The cost is that a wildcard-covered
	// name (turn.ns-<name>.<base>) survives a database outage for minutes, not
	// a day.
	WildcardStaleWindow = 5 * time.Minute

	// NegativeTTL is how long a negative answer (NXDOMAIN or NODATA) is cached.
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

	// negative marks a cached negative answer, whose rcode is its message's.
	negative bool

	// key, class and elem place the entry in its class's eviction queue.
	key   string
	class cacheClass
	elem  *list.Element
}

// cacheClass is the kind of an entry, which decides how long it stays usable.
// Within a class every entry has the same window, so the order of insertion is
// the order of the stale deadline and the queue's front is always the next to
// go.
type cacheClass int

const (
	classAnswer   cacheClass = iota // an answer from the name's own records
	classWildcard                   // an answer a wildcard synthesised
	classNegative                   // NXDOMAIN or NODATA
	cacheClasses
)

// Fresh reports whether the entry may be served without qualification.
func (e *CacheEntry) Fresh(now time.Time) bool { return now.Before(e.expiresAt) }

// Usable reports whether the entry may still be served as a stale answer.
func (e *CacheEntry) Usable(now time.Time) bool { return now.Before(e.staleUntil) }

// Cache implements a simple in-memory DNS response cache with serve-stale.
type Cache struct {
	entries map[string]*CacheEntry
	// queues hold each class's entries, oldest stale deadline first, so
	// evicting the entry closest to being unusable is a comparison of the
	// queues' fronts rather than a scan of the cache.
	queues  [cacheClasses]*list.List
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
	c.resetQueues()
	go c.cleanup()
	return c
}

// Get returns a FRESH cached message and whether it is a cached negative
// answer.
//
// The caller needs the second value: a cached negative answer must be replied
// with the rcode it was cached with (the message's own), and returning an
// NXDOMAIN as a success would turn it into an empty NOERROR — a different
// answer, which resolvers cache differently.
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
	c.store(qname, qtype, msg, c.ttl, classAnswer)
}

// SetWildcard stores an answer a wildcard synthesised for qname, which stays
// usable as a stale answer only for WildcardStaleWindow.
func (c *Cache) SetWildcard(qname string, qtype uint16, msg *dns.Msg) {
	c.store(qname, qtype, msg, c.ttl, classWildcard)
}

// SetNegative caches a negative answer (NXDOMAIN or NODATA) for a short time.
func (c *Cache) SetNegative(qname string, qtype uint16, msg *dns.Msg) {
	c.store(qname, qtype, msg, NegativeTTL, classNegative)
}

func (c *Cache) store(qname string, qtype uint16, msg *dns.Msg, ttl time.Duration, class cacheClass) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.key(qname, qtype)
	if old, ok := c.entries[key]; ok {
		c.remove(old)
	} else if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	now := time.Now()
	entry := &CacheEntry{
		msg:       msg.Copy(),
		expiresAt: now.Add(ttl),
		key:       key,
		class:     class,
		negative:  class == classNegative,
	}

	// A negative answer is never served stale. "This name does not exist" is
	// exactly the answer most likely to be wrong later — a namespace being
	// provisioned right now — and serving it for a day would keep a new record
	// invisible long after it appeared.
	switch class {
	case classAnswer:
		entry.staleUntil = now.Add(StaleWindow)
	case classWildcard:
		entry.staleUntil = now.Add(WildcardStaleWindow)
	default:
		entry.staleUntil = entry.expiresAt
	}

	entry.elem = c.queues[class].PushBack(entry)
	c.entries[key] = entry
}

// key generates a cache key from qname and qtype.
func (c *Cache) key(qname string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", qname, qtype)
}

// remove takes an entry out of the cache and its queue. The caller holds mu.
func (c *Cache) remove(entry *CacheEntry) {
	c.queues[entry.class].Remove(entry.elem)
	delete(c.entries, entry.key)
}

// resetQueues empties the eviction queues. The caller holds mu, or owns c.
func (c *Cache) resetQueues() {
	for i := range c.queues {
		c.queues[i] = list.New()
	}
}

// evictOldest removes the entry closest to being unusable, in constant time:
// the oldest of the three queues' fronts.
//
// Ordered on staleUntil, not expiresAt: an entry past its TTL is still the
// thing standing between a backend outage and SERVFAIL, so evicting it while
// something genuinely dead is still held would be backwards. It used to scan
// every entry under the cache's write lock on each insert into a full cache,
// which a flood of distinct names turned into a stall of every lookup.
func (c *Cache) evictOldest() {
	var oldest *CacheEntry
	for _, queue := range c.queues {
		if front := queue.Front(); front != nil {
			if entry := front.Value.(*CacheEntry); oldest == nil || entry.staleUntil.Before(oldest.staleUntil) {
				oldest = entry
			}
		}
	}
	if oldest != nil {
		c.remove(oldest)
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
		for _, entry := range c.entries {
			if !entry.Usable(now) {
				c.remove(entry)
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
	c.resetQueues()
}
