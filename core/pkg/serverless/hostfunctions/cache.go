package hostfunctions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	olriclib "github.com/olric-data/olric"
)

// maxCacheTTLSeconds is olric.MaxEntryTTL in the unit cache_set takes.
const maxCacheTTLSeconds = int64(olric.MaxEntryTTL / time.Second)

// cacheDMap opens the calling namespace's cache map.
//
// The map is named per namespace. The cluster gateway used to run every
// namespace's functions against one Olric, where a shared map name let one
// namespace's function read, overwrite and delete another's keys; a gateway
// now runs only its own namespace's functions (bugboard #427), and the name
// still keeps each namespace's keys apart on any Olric more than one reaches.
func (h *HostFunctions) cacheDMap(ctx context.Context, fn string) (olriclib.DMap, error) {
	if h.cacheClient == nil {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: serverless.ErrCacheUnavailable}
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.Namespace == "" {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: errors.New("cache requires an invocation namespace")}
	}
	dm, err := h.cacheClient.NewDMap(cacheDMapName + ":" + cur.Namespace)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: fmt.Errorf("failed to get cache DMap for namespace %s: %w", cur.Namespace, err)}
	}
	return dm, nil
}

// CacheGet retrieves a value from the cache.
func (h *HostFunctions) CacheGet(ctx context.Context, key string) ([]byte, error) {
	dm, err := h.cacheDMap(ctx, "cache_get")
	if err != nil {
		return nil, err
	}

	result, err := dm.Get(ctx, key)
	if errors.Is(err, olriclib.ErrKeyNotFound) {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("%w: %w", serverless.ErrCacheMiss, err)}
	}
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: err}
	}

	value, err := result.Byte()
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("failed to decode value: %w", err)}
	}

	return value, nil
}

// CacheSet stores a value in the cache.
//
// ttlSeconds > 0 expires the entry after that many seconds (Olric EX), up to
// olric.MaxEntryTTL. ttlSeconds == 0 stores the entry with no expiry; it lives
// until cache_delete or until the namespace Olric cluster loses it. A negative
// or over-long ttl is rejected rather than stored as something it is not.
func (h *HostFunctions) CacheSet(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	if ttlSeconds < 0 || ttlSeconds > maxCacheTTLSeconds {
		return &serverless.HostFunctionError{
			Function: "cache_set",
			Cause:    fmt.Errorf("%w: must be 0 (no expiry) to %d seconds, got %d", serverless.ErrInvalidCacheTTL, maxCacheTTLSeconds, ttlSeconds),
		}
	}
	dm, err := h.cacheDMap(ctx, "cache_set")
	if err != nil {
		return err
	}

	var opts []olriclib.PutOption
	if ttlSeconds > 0 {
		opts = append(opts, olriclib.EX(time.Duration(ttlSeconds)*time.Second))
	}
	if err := dm.Put(ctx, key, value, opts...); err != nil {
		return &serverless.HostFunctionError{Function: "cache_set", Cause: err}
	}

	return nil
}

// CacheDelete removes a value from the cache. Deleting a key that is not
// there is not an error.
func (h *HostFunctions) CacheDelete(ctx context.Context, key string) error {
	dm, err := h.cacheDMap(ctx, "cache_delete")
	if err != nil {
		return err
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
	dm, err := h.cacheDMap(ctx, "cache_incr_by")
	if err != nil {
		return 0, err
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
