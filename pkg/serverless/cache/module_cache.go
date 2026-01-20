package cache

import (
	"context"
	"sync"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// ModuleCache manages compiled WASM module caching.
type ModuleCache struct {
	modules  map[string]wazero.CompiledModule
	mu       sync.RWMutex
	capacity int
	logger   *zap.Logger
}

// NewModuleCache creates a new ModuleCache.
func NewModuleCache(capacity int, logger *zap.Logger) *ModuleCache {
	return &ModuleCache{
		modules:  make(map[string]wazero.CompiledModule),
		capacity: capacity,
		logger:   logger,
	}
}

// Get retrieves a compiled module from the cache.
func (c *ModuleCache) Get(wasmCID string) (wazero.CompiledModule, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	module, exists := c.modules[wasmCID]
	return module, exists
}

// Set stores a compiled module in the cache.
// If the cache is full, it evicts the oldest module.
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

	c.modules[wasmCID] = module

	c.logger.Debug("Module cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(c.modules)),
	)
}

// Delete removes a module from the cache and closes it.
func (c *ModuleCache) Delete(ctx context.Context, wasmCID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if module, exists := c.modules[wasmCID]; exists {
		_ = module.Close(ctx)
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

	for cid, module := range c.modules {
		if err := module.Close(ctx); err != nil {
			c.logger.Warn("Failed to close cached module during clear",
				zap.String("cid", cid),
				zap.Error(err),
			)
		}
	}

	c.modules = make(map[string]wazero.CompiledModule)
	c.logger.Debug("Module cache cleared")
}

// GetStats returns cache statistics.
func (c *ModuleCache) GetStats() (size int, capacity int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.modules), c.capacity
}

// evictOldest removes the oldest module from cache.
// Must be called with mu held.
func (c *ModuleCache) evictOldest() {
	// Simple LRU: just remove the first one we find
	// In production, you'd want proper LRU tracking
	for cid, module := range c.modules {
		_ = module.Close(context.Background())
		delete(c.modules, cid)
		c.logger.Debug("Evicted module from cache", zap.String("wasm_cid", cid))
		break
	}
}

// GetOrCompute retrieves a module from cache or computes it if not present.
// The compute function is called with the lock released to avoid blocking.
func (c *ModuleCache) GetOrCompute(wasmCID string, compute func() (wazero.CompiledModule, error)) (wazero.CompiledModule, error) {
	// Try to get from cache first
	c.mu.RLock()
	if module, exists := c.modules[wasmCID]; exists {
		c.mu.RUnlock()
		return module, nil
	}
	c.mu.RUnlock()

	// Compute the module (without holding the lock)
	module, err := compute()
	if err != nil {
		return nil, err
	}

	// Store in cache
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check (another goroutine might have added it)
	if existingModule, exists := c.modules[wasmCID]; exists {
		_ = module.Close(context.Background()) // Discard our compilation
		return existingModule, nil
	}

	// Evict if cache is full
	if len(c.modules) >= c.capacity {
		c.evictOldest()
	}

	c.modules[wasmCID] = module

	c.logger.Debug("Module compiled and cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(c.modules)),
	)

	return module, nil
}
