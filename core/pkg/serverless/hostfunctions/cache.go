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
	var client olriclib.Client
	if h.cacheClient != nil {
		client = h.cacheClient()
	}
	if client == nil {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: serverless.ErrCacheUnavailable}
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.Namespace == "" {
		return nil, &serverless.HostFunctionError{Function: fn, Cause: errors.New("cache requires an invocation namespace")}
	}
	dm, err := client.NewDMap(cacheDMapName + ":" + cur.Namespace)
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
	if olric.IsKeyNotFound(err) {
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

// CacheIncr atomically increments a numeric value in cache by 1 and returns the
// new value. See CacheIncrBy for what "atomically" covers.
func (h *HostFunctions) CacheIncr(ctx context.Context, key string) (int64, error) {
	return h.CacheIncrBy(ctx, key, 1)
}

// CacheIncrBy atomically increments a numeric value by delta and returns the new value.
//
// The DMap's Incr is the only thing that changes the counter. Olric runs it on
// the partition owner under a per-key lock, so concurrent increments from any
// number of gateways each see the previous one's result and return distinct
// values. It keeps the key's expiry (an increment neither sets nor extends
// one), so a counter that cache_set stored with a ttl expires on schedule and
// one an increment created never does.
//
// A missing, expired or non-numeric value counts as 0 and is replaced; Olric
// does not report the last as an error. A failed call is not retried (the
// gateway's client sets no retries): when it timed out, the increment may or
// may not have been applied, and the caller gets the error (the function sees 0).
func (h *HostFunctions) CacheIncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	dm, err := h.cacheDMap(ctx, "cache_incr_by")
	if err != nil {
		return 0, err
	}

	// Olric's Incr takes int (not int64) and returns int.
	newValue, err := dm.Incr(ctx, key, int(delta))
	if err != nil {
		return 0, &serverless.HostFunctionError{Function: "cache_incr_by", Cause: fmt.Errorf("failed to increment: %w", err)}
	}

	return int64(newValue), nil
}
