package cache

import (
	"context"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// cacheEntry wraps a compiled module with access tracking for LRU eviction.
type cacheEntry struct {
	module       wazero.CompiledModule
	lastAccessed time.Time
}

// ModuleCache manages compiled WASM module caching.
type ModuleCache struct {
	modules  map[string]*cacheEntry
	mu       sync.RWMutex
	capacity int
	logger   *zap.Logger
}

// NewModuleCache creates a new ModuleCache.
func NewModuleCache(capacity int, logger *zap.Logger) *ModuleCache {
	return &ModuleCache{
		modules:  make(map[string]*cacheEntry),
		capacity: capacity,
		logger:   logger,
	}
}

// Get retrieves a compiled module from the cache.
func (c *ModuleCache) Get(wasmCID string) (wazero.CompiledModule, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.modules[wasmCID]
	if !exists {
		return nil, false
	}

	entry.lastAccessed = time.Now()
	return entry.module, true
}

// Set stores a compiled module in the cache.
// If the cache is full, it evicts the least recently used module.
func (c *ModuleCache) Set(wasmCID string, module wazero.CompiledModule) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already exists
	if _, exists := c.modules[wasmCID]; exists {
		return
	}

	// Evict if cache is full
	if len(c.modules) >= c.capacity {
		c.evictOldest()
	}

	c.modules[wasmCID] = &cacheEntry{
		module:       module,
		lastAccessed: time.Now(),
	}

	c.logger.Debug("Module cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(c.modules)),
	)
}

// Delete removes a module from the cache and closes it.
func (c *ModuleCache) Delete(ctx context.Context, wasmCID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.modules[wasmCID]; exists {
		_ = entry.module.Close(ctx)
		delete(c.modules, wasmCID)
		c.logger.Debug("Module removed from cache", zap.String("wasm_cid", wasmCID))
	}
}

// Has checks if a module exists in the cache.
func (c *ModuleCache) Has(wasmCID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, exists := c.modules[wasmCID]
	return exists
}

// Size returns the current number of cached modules.
func (c *ModuleCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.modules)
}

// Capacity returns the maximum cache capacity.
func (c *ModuleCache) Capacity() int {
	return c.capacity
}

// Clear removes all modules from the cache and closes them.
func (c *ModuleCache) Clear(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for cid, entry := range c.modules {
		if err := entry.module.Close(ctx); err != nil {
			c.logger.Warn("Failed to close cached module during clear",
				zap.String("cid", cid),
				zap.Error(err),
			)
		}
	}

	c.modules = make(map[string]*cacheEntry)
	c.logger.Debug("Module cache cleared")
}

// GetStats returns cache statistics.
func (c *ModuleCache) GetStats() (size int, capacity int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.modules), c.capacity
}

// evictOldest removes the least recently accessed module from cache.
// Must be called with mu held.
func (c *ModuleCache) evictOldest() {
	var oldestCID string
	var oldestTime time.Time

	for cid, entry := range c.modules {
		if oldestCID == "" || entry.lastAccessed.Before(oldestTime) {
			oldestCID = cid
			oldestTime = entry.lastAccessed
		}
	}

	if oldestCID != "" {
		_ = c.modules[oldestCID].module.Close(context.Background())
		delete(c.modules, oldestCID)
		c.logger.Debug("Evicted LRU module from cache", zap.String("wasm_cid", oldestCID))
	}
}

// GetOrCompute retrieves a module from cache or computes it if not present.
// The compute function is called with the lock released to avoid blocking.
func (c *ModuleCache) GetOrCompute(wasmCID string, compute func() (wazero.CompiledModule, error)) (wazero.CompiledModule, error) {
	// Try to get from cache first
	c.mu.Lock()
	if entry, exists := c.modules[wasmCID]; exists {
		entry.lastAccessed = time.Now()
		c.mu.Unlock()
		return entry.module, nil
	}
	c.mu.Unlock()

	// Compute the module (without holding the lock)
	module, err := compute()
	if err != nil {
		return nil, err
	}

	// Store in cache
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check (another goroutine might have added it)
	if entry, exists := c.modules[wasmCID]; exists {
		_ = module.Close(context.Background()) // Discard our compilation
		entry.lastAccessed = time.Now()
		return entry.module, nil
	}

	// Evict if cache is full
	if len(c.modules) >= c.capacity {
		c.evictOldest()
	}

	c.modules[wasmCID] = &cacheEntry{
		module:       module,
		lastAccessed: time.Now(),
	}

	c.logger.Debug("Module compiled and cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(c.modules)),
	)

	return module, nil
}
