package triggers

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Bugboard #555 — a SINGLE node must never invoke the same publish twice,
// independent of Olric health. These tests pin the local dedup cache's
// claim/expiry/eviction behavior.

func TestLocalDedupCache_sameKeyClaimedOncePerWindow(t *testing.T) {
	c := newLocalDedupCache()
	key := dispatchDedupKey("ns", "messages:new", []byte(`{"id":1}`))

	if !c.claim(key) {
		t.Fatal("first claim of an unseen key must fire (return true)")
	}
	if c.claim(key) {
		t.Error("second claim within the TTL must be deduped (return false)")
	}
}

func TestLocalDedupCache_distinctKeysBothFire(t *testing.T) {
	c := newLocalDedupCache()
	a := dispatchDedupKey("ns", "messages:new", []byte("A"))
	b := dispatchDedupKey("ns", "messages:new", []byte("B"))

	if !c.claim(a) {
		t.Error("distinct payload A must fire")
	}
	if !c.claim(b) {
		t.Error("distinct payload B must fire (different payload → different key)")
	}
}

func TestLocalDedupCache_expiredEntryFiresAgain(t *testing.T) {
	// Drive a controllable clock so we don't sleep in the test.
	cur := time.Unix(1_000_000, 0)
	c := newLocalDedupCache()
	c.now = func() time.Time { return cur }

	key := dispatchDedupKey("ns", "messages:new", []byte("x"))
	if !c.claim(key) {
		t.Fatal("first claim must fire")
	}
	if c.claim(key) {
		t.Fatal("immediate re-claim must be deduped")
	}

	// Advance past the TTL: the entry has expired, so the same key must
	// fire again (a legitimately-repeated publish seconds apart).
	cur = cur.Add(localDedupTTL + time.Second)
	if !c.claim(key) {
		t.Error("after TTL expiry the same key must fire again")
	}
}

func TestLocalDedupCache_evictsExpiredOnPressure(t *testing.T) {
	cur := time.Unix(2_000_000, 0)
	c := &localDedupCache{
		entries: make(map[string]time.Time),
		ttl:     localDedupTTL,
		maxSize: 4, // tiny cap to exercise the sweep path deterministically
		now:     func() time.Time { return cur },
	}

	// Fill to capacity with soon-to-expire entries.
	for i := 0; i < c.maxSize; i++ {
		key := dispatchDedupKey("ns", "t", []byte{byte(i)})
		if !c.claim(key) {
			t.Fatalf("fill claim %d must fire", i)
		}
	}
	if len(c.entries) != c.maxSize {
		t.Fatalf("expected cache full at %d, got %d", c.maxSize, len(c.entries))
	}

	// Advance past TTL so every existing entry is expired, then claim a new
	// key: the sweep must reclaim space and the new key must be recorded.
	cur = cur.Add(localDedupTTL + time.Second)
	newKey := dispatchDedupKey("ns", "t", []byte("fresh"))
	if !c.claim(newKey) {
		t.Fatal("new key under pressure must fire")
	}
	if _, ok := c.entries[newKey]; !ok {
		t.Error("new key must be recorded after expired entries were swept")
	}
	if len(c.entries) > c.maxSize {
		t.Errorf("cache must not exceed maxSize after sweep; got %d", len(c.entries))
	}
}

func TestLocalDedupCache_concurrentClaimsExactlyOneWins(t *testing.T) {
	// Race condition guard: when many goroutines race to claim the SAME key
	// (gossipsub delivering one publish across handler goroutines), exactly
	// one must win. Run under -race to catch unsynchronized map access.
	c := newLocalDedupCache()
	key := dispatchDedupKey("ns", "messages:new", []byte(`{"id":"race"}`))

	const goroutines = 64
	var wins int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if c.claim(key) {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("exactly one concurrent claim of the same key must win; got %d", wins)
	}
}

func TestLocalDedupCache_failsOpenWhenFullOfLiveEntries(t *testing.T) {
	cur := time.Unix(3_000_000, 0)
	c := &localDedupCache{
		entries: make(map[string]time.Time),
		ttl:     localDedupTTL,
		maxSize: 2,
		now:     func() time.Time { return cur },
	}

	// Fill with two still-live entries.
	c.claim(dispatchDedupKey("ns", "t", []byte("a")))
	c.claim(dispatchDedupKey("ns", "t", []byte("b")))

	// A new key when the cache is full of LIVE entries must fail-open
	// (fire) rather than drop a legitimate wake.
	if !c.claim(dispatchDedupKey("ns", "t", []byte("c"))) {
		t.Error("claim must fail-open (true) when the cache is full of live entries")
	}
}
