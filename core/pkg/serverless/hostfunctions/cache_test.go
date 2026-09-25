package hostfunctions

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	olriclib "github.com/olric-data/olric"
)

// bug-421: cache_set dropped its ttl, so every entry was immortal. These tests
// run against a real embedded Olric so they pin the expiry itself, not a mock
// that records an argument.

const testNamespace = "anchat"

func newEmbeddedCache(t *testing.T) *HostFunctions {
	t.Helper()
	return &HostFunctions{cacheClient: olrictest.Start(t).EmbeddedClient()}
}

// nsCtx is an invocation in testNamespace; the cache is scoped by it.
func nsCtx() context.Context {
	return invocationCtx(&serverless.InvocationContext{Namespace: testNamespace})
}

func storedTTL(t *testing.T, h *HostFunctions, key string) int64 {
	t.Helper()
	dm, err := h.cacheClient.NewDMap(cacheDMapName + ":" + testNamespace)
	if err != nil {
		t.Fatalf("failed to open DMap: %v", err)
	}
	res, err := dm.Get(nsCtx(), key)
	if err != nil {
		t.Fatalf("failed to read %q back: %v", key, err)
	}
	return res.TTL()
}

func TestCacheSet_positiveTTLExpires(t *testing.T) {
	h := newEmbeddedCache(t)
	ctx := nsCtx()

	if err := h.CacheSet(ctx, "price:sol", []byte("1.23"), 1); err != nil {
		t.Fatalf("CacheSet: %v", err)
	}
	if ttl := storedTTL(t, h, "price:sol"); ttl <= 0 {
		t.Fatalf("stored TTL = %d, want an expiry to be set", ttl)
	}
	got, err := h.CacheGet(ctx, "price:sol")
	if err != nil || string(got) != "1.23" {
		t.Fatalf("immediate CacheGet = %q, %v; want the stored value", got, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := h.CacheGet(ctx, "price:sol")
		if errors.Is(err, olriclib.ErrKeyNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("entry written with ttl=1s was still readable after 5s (last err: %v)", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCacheSet_zeroTTLHasNoExpiry(t *testing.T) {
	h := newEmbeddedCache(t)

	if err := h.CacheSet(nsCtx(), "forever", []byte("v"), 0); err != nil {
		t.Fatalf("CacheSet: %v", err)
	}
	if ttl := storedTTL(t, h, "forever"); ttl != 0 {
		t.Fatalf("stored TTL = %d, want 0 (no expiry) for ttl_seconds=0", ttl)
	}
}

func TestCacheSet_negativeTTLRejected(t *testing.T) {
	h := newEmbeddedCache(t)
	ctx := nsCtx()

	err := h.CacheSet(ctx, "neg", []byte("v"), -5)
	if err == nil {
		t.Fatal("CacheSet with ttl=-5 succeeded; a negative TTL must not be stored as immortal")
	}
	if _, err := h.CacheGet(ctx, "neg"); !errors.Is(err, olriclib.ErrKeyNotFound) {
		t.Fatalf("rejected write left an entry behind (CacheGet err: %v)", err)
	}
}

func TestCacheSet_negativeTTLRejectedWithoutCache(t *testing.T) {
	// The TTL is validated before the cache is touched, so a bad argument is
	// reported as a bad argument even when Olric is unavailable.
	h := &HostFunctions{}
	err := h.CacheSet(nsCtx(), "k", []byte("v"), -1)
	if err == nil || errors.Is(err, serverless.ErrCacheUnavailable) {
		t.Fatalf("CacheSet(ttl=-1) err = %v; want a ttl validation error", err)
	}
}

func TestCacheSet_noCacheClient(t *testing.T) {
	h := &HostFunctions{}
	err := h.CacheSet(nsCtx(), "k", []byte("v"), 10)
	if !errors.Is(err, serverless.ErrCacheUnavailable) {
		t.Fatalf("CacheSet without a cache client: err = %v, want ErrCacheUnavailable", err)
	}
}

func TestCacheDelete_removesImmortalEntry(t *testing.T) {
	// cache_delete is the only way a guest can heal an entry written before
	// the TTL fix, so it must remove a no-expiry key.
	h := newEmbeddedCache(t)
	ctx := nsCtx()

	if err := h.CacheSet(ctx, "stale", []byte("old"), 0); err != nil {
		t.Fatalf("CacheSet: %v", err)
	}
	if err := h.CacheDelete(ctx, "stale"); err != nil {
		t.Fatalf("CacheDelete: %v", err)
	}
	if _, err := h.CacheGet(ctx, "stale"); !errors.Is(err, olriclib.ErrKeyNotFound) {
		t.Fatalf("CacheGet after delete: err = %v, want ErrKeyNotFound", err)
	}
}

func TestCacheDelete_missingKeyIsNotAnError(t *testing.T) {
	h := newEmbeddedCache(t)
	if err := h.CacheDelete(nsCtx(), "never-set"); err != nil {
		t.Fatalf("CacheDelete of a missing key: %v; want nil (delete is idempotent)", err)
	}
}

func TestCacheSet_overLongTTLRejected(t *testing.T) {
	// Olric stores expiry as now+ttl in UnixNano; a ttl past the bound would
	// overflow it and the entry would be stored already expired while the
	// write reported success.
	h := newEmbeddedCache(t)
	for _, ttl := range []int64{maxCacheTTLSeconds + 1, 7_500_000_000, math.MaxInt64} {
		err := h.CacheSet(nsCtx(), "long", []byte("v"), ttl)
		if !errors.Is(err, serverless.ErrInvalidCacheTTL) {
			t.Errorf("CacheSet(ttl=%d) err = %v, want ErrInvalidCacheTTL", ttl, err)
		}
	}
	if err := h.CacheSet(nsCtx(), "long", []byte("v"), maxCacheTTLSeconds); err != nil {
		t.Fatalf("CacheSet at the maximum ttl: %v", err)
	}
	if ttl := storedTTL(t, h, "long"); ttl <= 0 {
		t.Fatalf("entry at the maximum ttl stored with TTL %d; it expired on write", ttl)
	}
}

func TestCache_isolatedPerNamespace(t *testing.T) {
	// The cluster gateway runs functions for every namespace against one
	// Olric; one namespace must not see, overwrite or delete another's keys.
	h := newEmbeddedCache(t)
	a := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})
	b := invocationCtx(&serverless.InvocationContext{Namespace: "ns-b"})

	if err := h.CacheSet(a, "shared", []byte("from-a"), 0); err != nil {
		t.Fatalf("CacheSet in ns-a: %v", err)
	}
	if _, err := h.CacheGet(b, "shared"); !errors.Is(err, olriclib.ErrKeyNotFound) {
		t.Fatalf("ns-b read ns-a's key (err: %v)", err)
	}
	if err := h.CacheDelete(b, "shared"); err != nil {
		t.Fatalf("CacheDelete in ns-b: %v", err)
	}
	got, err := h.CacheGet(a, "shared")
	if err != nil || string(got) != "from-a" {
		t.Fatalf("ns-a's key after ns-b's delete = %q, %v; want it untouched", got, err)
	}
}

func TestCache_refusedOutsideAnInvocation(t *testing.T) {
	// No invocation means no namespace to scope by; the call is refused rather
	// than served from some shared map.
	h := newEmbeddedCache(t)
	if err := h.CacheSet(context.Background(), "k", []byte("v"), 0); err == nil {
		t.Fatal("CacheSet outside an invocation succeeded")
	}
	if _, err := h.CacheGet(context.Background(), "k"); err == nil {
		t.Fatal("CacheGet outside an invocation succeeded")
	}
}

func TestCacheGet_missIsErrCacheMiss(t *testing.T) {
	// The engine logs every cache_get failure except a miss; the sentinel is
	// how it tells them apart without knowing the cache is Olric.
	h := newEmbeddedCache(t)
	_, err := h.CacheGet(nsCtx(), "absent")
	if !errors.Is(err, serverless.ErrCacheMiss) {
		t.Fatalf("CacheGet of an absent key: err = %v, want ErrCacheMiss", err)
	}
	if _, err := (&HostFunctions{}).CacheGet(nsCtx(), "k"); errors.Is(err, serverless.ErrCacheMiss) {
		t.Fatal("an unavailable cache reported a miss; it must be distinguishable")
	}
}
