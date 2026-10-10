package rqlite

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func answerMsg(name, ip string) *dns.Msg {
	msg := new(dns.Msg)
	msg.Answer = append(msg.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   []byte{10, 0, 0, 1},
	})
	return msg
}

func TestCache_freshAnswerIsAHit(t *testing.T) {
	c := NewCache(10, time.Minute)
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.1"))

	msg, negative := c.Get("a.example.", dns.TypeA)
	if msg == nil {
		t.Fatal("a fresh entry was not returned")
	}
	if negative {
		t.Fatal("a positive answer was reported as negative")
	}

	hits, _, size := c.Stats()
	if hits != 1 || size != 1 {
		t.Fatalf("hits=%d size=%d", hits, size)
	}
}

func TestCache_expiredAnswerIsNotFreshButIsStillUsable(t *testing.T) {
	// The whole point of serve-stale: past its TTL an answer stops being served
	// normally, but remains the thing standing between a backend outage and
	// SERVFAIL for the entire zone.
	c := NewCache(10, time.Millisecond)
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.1"))
	time.Sleep(20 * time.Millisecond)

	if msg, _ := c.Get("a.example.", dns.TypeA); msg != nil {
		t.Fatal("an expired entry was served as fresh")
	}

	stale := c.GetStale("a.example.", dns.TypeA)
	if stale == nil {
		t.Fatal("an expired entry was not available as a stale answer")
	}
	if len(stale.Answer) != 1 {
		t.Fatalf("stale answer has %d records", len(stale.Answer))
	}
	if got := stale.Answer[0].Header().Ttl; got != uint32(StaleTTL.Seconds()) {
		t.Fatalf("stale TTL = %d, want %d — a resolver must come back promptly once the backend recovers",
			got, uint32(StaleTTL.Seconds()))
	}
	if c.StaleServed() != 1 {
		t.Fatalf("StaleServed = %d, want 1", c.StaleServed())
	}
}

func TestCache_staleWindowIsBounded(t *testing.T) {
	c := NewCache(10, time.Millisecond)
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.1"))

	// Reach past the stale window by hand rather than waiting a day.
	c.mu.Lock()
	entry := c.entries[c.key("a.example.", dns.TypeA)]
	entry.staleUntil = time.Now().Add(-time.Second)
	c.mu.Unlock()

	if c.GetStale("a.example.", dns.TypeA) != nil {
		t.Fatal("an answer past the stale window was still served")
	}
}

func TestCache_negativeAnswerIsCachedButNeverServedStale(t *testing.T) {
	// "This name does not exist" is the answer most likely to be wrong soon —
	// a namespace being provisioned right now — so it gets a short TTL and no
	// stale window at all.
	c := NewCache(10, time.Minute)

	msg := new(dns.Msg)
	msg.Rcode = dns.RcodeNameError
	c.SetNegative("gone.example.", dns.TypeA, msg)

	cached, negative := c.Get("gone.example.", dns.TypeA)
	if cached == nil {
		t.Fatal("the NXDOMAIN was not cached; a random-subdomain flood becomes a query amplifier")
	}
	if !negative {
		t.Fatal("the cached NXDOMAIN was not reported as negative; it would be served as an empty NOERROR")
	}

	c.mu.Lock()
	entry := c.entries[c.key("gone.example.", dns.TypeA)]
	if entry.staleUntil.After(entry.expiresAt) {
		t.Error("a negative answer has a stale window; a name that appears later would stay invisible")
	}
	entry.expiresAt = time.Now().Add(-time.Second)
	entry.staleUntil = entry.expiresAt
	c.mu.Unlock()

	if c.GetStale("gone.example.", dns.TypeA) != nil {
		t.Fatal("an expired NXDOMAIN was served stale")
	}
}

func TestCache_evictionPrefersTheLeastUsableEntry(t *testing.T) {
	// Ordered on staleUntil, not expiresAt: an entry past its TTL is still what
	// keeps the zone answering during an outage.
	c := NewCache(2, time.Minute)

	c.Set("keep.example.", dns.TypeA, answerMsg("keep.example.", "10.0.0.1"))
	negative := new(dns.Msg)
	negative.Rcode = dns.RcodeNameError
	c.SetNegative("drop.example.", dns.TypeA, negative) // no stale window

	// Adding a third evicts one.
	c.Set("new.example.", dns.TypeA, answerMsg("new.example.", "10.0.0.3"))

	if _, _, size := c.Stats(); size != 2 {
		t.Fatalf("size = %d, want 2", size)
	}
	if c.GetStale("keep.example.", dns.TypeA) == nil {
		t.Error("the entry with the longest stale window was evicted")
	}
}

func TestCache_countersAreRaceFree(t *testing.T) {
	// Get reads under an RLock; incrementing a plain uint64 there is a data
	// race `go test -race` catches.
	c := NewCache(100, time.Minute)
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.1"))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c.Get("a.example.", dns.TypeA)
				c.Get("missing.example.", dns.TypeA)
				_, _, _ = c.Stats()
			}
		}()
	}
	wg.Wait()

	hits, misses, _ := c.Stats()
	if hits != 32*50 {
		t.Errorf("hits = %d, want %d", hits, 32*50)
	}
	if misses != 32*50 {
		t.Errorf("misses = %d, want %d", misses, 32*50)
	}
}

func TestCache_clear(t *testing.T) {
	c := NewCache(10, time.Minute)
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.1"))
	c.Clear()
	if _, _, size := c.Stats(); size != 0 {
		t.Fatalf("size = %d after Clear", size)
	}
}

// queueLens are the lengths of the eviction queues; with the entries map they
// must always describe the same set.
func queueLens(c *Cache) int {
	n := 0
	for _, q := range c.queues {
		n += q.Len()
	}
	return n
}

// A wildcard-synthesised answer is evicted before a real one, and stays
// usable stale for the short window only.
func TestCache_wildcardAnswersAreEvictedFirstAndDoNotOutliveTheirWindow(t *testing.T) {
	c := NewCache(3, time.Minute)
	c.Set("real1.example.", dns.TypeA, answerMsg("real1.example.", "10.0.0.1"))
	c.SetWildcard("wild.example.", dns.TypeA, answerMsg("wild.example.", "10.0.0.2"))
	c.Set("real2.example.", dns.TypeA, answerMsg("real2.example.", "10.0.0.3"))
	c.Set("real3.example.", dns.TypeA, answerMsg("real3.example.", "10.0.0.4"))

	if c.GetEntry("wild.example.", dns.TypeA) != nil {
		t.Fatal("the wildcard answer outlived a real one at the size limit")
	}
	for _, name := range []string{"real1.example.", "real2.example.", "real3.example."} {
		if c.GetEntry(name, dns.TypeA) == nil {
			t.Errorf("%s was evicted", name)
		}
	}

	c.SetWildcard("w2.example.", dns.TypeA, answerMsg("w2.example.", "10.0.0.5"))
	entry := c.GetEntry("w2.example.", dns.TypeA)
	if window := entry.staleUntil.Sub(entry.expiresAt); window > WildcardStaleWindow || window <= 0 {
		t.Fatalf("a wildcard answer is stale-usable for %s past its TTL, want at most %s", window, WildcardStaleWindow)
	}
	if real := c.GetEntry("real2.example.", dns.TypeA); real.staleUntil.Sub(real.expiresAt) < StaleWindow-time.Minute {
		t.Fatal("a real answer lost its serve-stale window")
	}
}

// A flood of distinct wildcard-covered names does not displace the real
// entries: the cache the platform's own names depend on during an outage.
func TestCache_aFloodOfWildcardNamesDoesNotDisplaceRealEntries(t *testing.T) {
	const real = 50
	c := NewCache(100, time.Minute)
	for i := 0; i < real; i++ {
		name := fmt.Sprintf("real%d.example.", i)
		c.Set(name, dns.TypeA, answerMsg(name, "10.0.0.1"))
	}
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("flood%d.example.", i)
		c.SetWildcard(name, dns.TypeA, answerMsg(name, "10.0.0.2"))
	}
	for i := 0; i < real; i++ {
		if c.GetEntry(fmt.Sprintf("real%d.example.", i), dns.TypeA) == nil {
			t.Fatalf("real%d.example. was displaced by the flood", i)
		}
	}
	if _, _, size := c.Stats(); size != 100 {
		t.Fatalf("size = %d, want the cache full at 100", size)
	}
}

// Eviction takes the entry closest to being unusable across the classes, and
// overwriting a key at the limit evicts nothing else and leaves one queue
// entry per cache entry.
func TestCache_evictionOrderAndQueueConsistency(t *testing.T) {
	c := NewCache(2, time.Minute)
	c.SetNegative("neg.example.", dns.TypeA, answerMsg("neg.example.", "10.0.0.1"))
	c.Set("real.example.", dns.TypeA, answerMsg("real.example.", "10.0.0.2"))

	// Overwrite at the limit: nothing is evicted.
	c.Set("real.example.", dns.TypeA, answerMsg("real.example.", "10.0.0.3"))
	if _, _, size := c.Stats(); size != 2 || queueLens(c) != 2 {
		t.Fatalf("after an overwrite: %d entries, %d queued, want 2 and 2", size, queueLens(c))
	}

	// A new key evicts the negative answer (30 s from unusable), not the 24 h one.
	c.Set("new.example.", dns.TypeA, answerMsg("new.example.", "10.0.0.4"))
	if c.GetEntry("neg.example.", dns.TypeA) != nil || c.GetEntry("real.example.", dns.TypeA) == nil {
		t.Fatal("the entry closest to being unusable was not the one evicted")
	}
	if _, _, size := c.Stats(); size != 2 || queueLens(c) != 2 {
		t.Fatalf("after an eviction: %d entries, %d queued, want 2 and 2", size, queueLens(c))
	}

	c.Clear()
	if _, _, size := c.Stats(); size != 0 || queueLens(c) != 0 {
		t.Fatalf("after Clear: %d entries, %d queued", size, queueLens(c))
	}
	c.Set("a.example.", dns.TypeA, answerMsg("a.example.", "10.0.0.5"))
	if queueLens(c) != 1 {
		t.Fatal("the cache does not queue after Clear")
	}
}

// Inserting into a full cache is constant time. A scan per insert made 50k
// inserts into a 50k cache 2.5 billion map visits, tens of seconds under the
// write lock; the bound here is generous against that and against a slow CI.
func TestCache_insertIntoAFullCacheIsNotLinear(t *testing.T) {
	const size = 50000
	c := NewCache(size, time.Minute)
	msg := answerMsg("x.example.", "10.0.0.1")
	for i := 0; i < size; i++ {
		c.Set(fmt.Sprintf("n%d.example.", i), dns.TypeA, msg)
	}
	start := time.Now()
	for i := size; i < 2*size; i++ {
		c.Set(fmt.Sprintf("n%d.example.", i), dns.TypeA, msg)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("%d inserts into a full cache took %s; eviction is not constant time", size, took)
	}
	if _, _, got := c.Stats(); got != size || queueLens(c) != size {
		t.Fatalf("%d entries, %d queued, want %d", got, queueLens(c), size)
	}
}

func BenchmarkCache_insertIntoAFullCache(b *testing.B) {
	for _, size := range []int{1000, 100000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			c := NewCache(size, time.Minute)
			msg := answerMsg("x.example.", "10.0.0.1")
			for i := 0; i < size; i++ {
				c.Set(fmt.Sprintf("n%d.example.", i), dns.TypeA, msg)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Set(fmt.Sprintf("m%d.example.", i), dns.TypeA, msg)
			}
		})
	}
}
