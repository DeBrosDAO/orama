package cache

import (
	"context"
	"errors"
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

	// inflight holds the compilation in progress for each CID, so concurrent
	// first invocations of one function share a single compile.
	inflight map[string]*compileCall
}

// compileCall is one compilation that callers wanting the same CID wait on.
type compileCall struct {
	done chan struct{}
	// waiters counts the callers blocked on done; guarded by ModuleCache.mu.
	// Production code never reads it; the tests use it to know a caller has
	// attached before they let the compile finish.
	waiters int
	module  wazero.CompiledModule
	err     error
}

// NewModuleCache creates a new ModuleCache.
func NewModuleCache(capacity int, logger *zap.Logger) *ModuleCache {
	return &ModuleCache{
		modules:  make(map[string]*cacheEntry),
		inflight: make(map[string]*compileCall),
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
// Concurrent callers for the same CID share one compute: a cold function hit
// by N simultaneous invocations compiled the module N times, and on a busy node
// those compiles took longer than the invocation timeout. The compute function
// runs with the lock released. A waiter whose leader failed because the
// leader's own context ended computes again under its own.
func (c *ModuleCache) GetOrCompute(ctx context.Context, wasmCID string, compute func() (wazero.CompiledModule, error)) (wazero.CompiledModule, error) {
	for {
		c.mu.Lock()
		if entry, exists := c.modules[wasmCID]; exists {
			entry.lastAccessed = time.Now()
			c.mu.Unlock()
			return entry.module, nil
		}
		call, running := c.inflight[wasmCID]
		if !running {
			call = &compileCall{done: make(chan struct{})}
			c.inflight[wasmCID] = call
			c.mu.Unlock()
			return c.lead(wasmCID, call, compute)
		}
		call.waiters++
		c.mu.Unlock()

		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if call.err == nil {
			return call.module, nil
		}
		if !errors.Is(call.err, context.Canceled) && !errors.Is(call.err, context.DeadlineExceeded) {
			return nil, call.err
		}
	}
}

// errCompilePanicked is what waiters get when the compile they waited on
// panicked.
var errCompilePanicked = errors.New("module compile panicked")

// lead runs compute for call, caches the result and releases the waiters. If
// compute panics, the waiters are released with errCompilePanicked and the CID
// is free to be compiled again, then the panic continues in the leader: this
// cache does not decide how its caller handles a panic, it only guarantees one
// does not leave the function stuck behind a compile that will never finish.
func (c *ModuleCache) lead(wasmCID string, call *compileCall, compute func() (wazero.CompiledModule, error)) (wazero.CompiledModule, error) {
	settled := false
	defer func() {
		if settled {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.inflight, wasmCID)
		call.err = errCompilePanicked
		close(call.done)
	}()

	module, err := compute()

	c.mu.Lock()
	defer c.mu.Unlock()
	settled = true
	delete(c.inflight, wasmCID)
	defer close(call.done)
	if err != nil {
		call.err = err
		return nil, err
	}

	// Set may have stored it while compute ran.
	if entry, exists := c.modules[wasmCID]; exists {
		_ = module.Close(context.Background())
		entry.lastAccessed = time.Now()
		call.module = entry.module
		return entry.module, nil
	}

	if len(c.modules) >= c.capacity {
		c.evictOldest()
	}
	c.modules[wasmCID] = &cacheEntry{module: module, lastAccessed: time.Now()}
	call.module = module

	c.logger.Debug("Module compiled and cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(c.modules)),
	)
	return module, nil
}
