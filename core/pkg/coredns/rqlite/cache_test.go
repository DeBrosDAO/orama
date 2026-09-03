package rqlite

import (
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
