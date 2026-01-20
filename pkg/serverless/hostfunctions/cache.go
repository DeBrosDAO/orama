package hostfunctions

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// CacheGet retrieves a value from the cache.
func (h *HostFunctions) CacheGet(ctx context.Context, key string) ([]byte, error) {
	if h.cacheClient == nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: serverless.ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	result, err := dm.Get(ctx, key)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: err}
	}

	value, err := result.Byte()
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("failed to decode value: %w", err)}
	}

	return value, nil
}

// CacheSet stores a value in the cache with optional TTL.
// Note: TTL is currently not supported by the underlying Olric DMap.Put method.
// Values are stored indefinitely until explicitly deleted.
func (h *HostFunctions) CacheSet(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	if h.cacheClient == nil {
		return &serverless.HostFunctionError{Function: "cache_set", Cause: serverless.ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return &serverless.HostFunctionError{Function: "cache_set", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	// Note: Olric DMap.Put doesn't support TTL in the basic API
	// For TTL support, consider using Olric's Expire API separately
	if err := dm.Put(ctx, key, value); err != nil {
		return &serverless.HostFunctionError{Function: "cache_set", Cause: err}
	}

	return nil
}

// CacheDelete removes a value from the cache.
func (h *HostFunctions) CacheDelete(ctx context.Context, key string) error {
	if h.cacheClient == nil {
		return &serverless.HostFunctionError{Function: "cache_delete", Cause: serverless.ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return &serverless.HostFunctionError{Function: "cache_delete", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	if _, err := dm.Delete(ctx, key); err != nil {
		return &serverless.HostFunctionError{Function: "cache_delete", Cause: err}
	}

	return nil
}

// CacheIncr atomically increments a numeric value in cache by 1 and returns the new value.
// If the key doesn't exist, it is initialized to 0 before incrementing.
// Returns an error if the value exists but is not numeric.
func (h *HostFunctions) CacheIncr(ctx context.Context, key string) (int64, error) {
	return h.CacheIncrBy(ctx, key, 1)
}

// CacheIncrBy atomically increments a numeric value by delta and returns the new value.
// If the key doesn't exist, it is initialized to 0 before incrementing.
// Returns an error if the value exists but is not numeric.
func (h *HostFunctions) CacheIncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	if h.cacheClient == nil {
		return 0, &serverless.HostFunctionError{Function: "cache_incr_by", Cause: serverless.ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return 0, &serverless.HostFunctionError{Function: "cache_incr_by", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	// Olric's Incr method atomically increments a numeric value
	// It initializes the key to 0 if it doesn't exist, then increments by delta
	// Note: Olric's Incr takes int (not int64) and returns int
	newValue, err := dm.Incr(ctx, key, int(delta))
	if err != nil {
		return 0, &serverless.HostFunctionError{Function: "cache_incr_by", Cause: fmt.Errorf("failed to increment: %w", err)}
	}

	return int64(newValue), nil
}
