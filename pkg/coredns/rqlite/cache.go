package rqlite

import (
	"fmt"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// CacheEntry represents a cached DNS response
type CacheEntry struct {
	msg       *dns.Msg
	expiresAt time.Time
}

// Cache implements a simple in-memory DNS response cache
type Cache struct {
	entries   map[string]*CacheEntry
	mu        sync.RWMutex
	maxSize   int
	ttl       time.Duration
	hitCount  uint64
	missCount uint64
}

// NewCache creates a new DNS response cache
func NewCache(maxSize int, ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}

	// Start cleanup goroutine
	go c.cleanup()

	return c
}

// Get retrieves a cached DNS message
func (c *Cache) Get(qname string, qtype uint16) *dns.Msg {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.key(qname, qtype)
	entry, exists := c.entries[key]

	if !exists {
		c.missCount++
		return nil
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		c.missCount++
		return nil
	}

	c.hitCount++
	return entry.msg.Copy()
}

// Set stores a DNS message in the cache
func (c *Cache) Set(qname string, qtype uint16, msg *dns.Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Enforce max size
	if len(c.entries) >= c.maxSize {
		// Remove oldest entry (simple eviction strategy)
		c.evictOldest()
	}

	key := c.key(qname, qtype)
	c.entries[key] = &CacheEntry{
		msg:       msg.Copy(),
		expiresAt: time.Now().Add(c.ttl),
	}
}

// key generates a cache key from qname and qtype
func (c *Cache) key(qname string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", qname, qtype)
}

// evictOldest removes the oldest entry from the cache
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.entries {
		if first || entry.expiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.expiresAt
			first = false
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// cleanup periodically removes expired entries
func (c *Cache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.After(entry.expiresAt) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

// Stats returns cache statistics
func (c *Cache) Stats() (hits, misses uint64, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hitCount, c.missCount, len(c.entries)
}

// Clear removes all entries from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*CacheEntry)
}
