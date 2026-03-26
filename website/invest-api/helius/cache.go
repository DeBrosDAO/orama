package helius

import (
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

type Cache[T any] struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[T]
	ttl     time.Duration
}

func NewCache[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		entries: make(map[string]cacheEntry[T]),
		ttl:     ttl,
	}
}

func (c *Cache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

func (c *Cache[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry[T]{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *Cache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
