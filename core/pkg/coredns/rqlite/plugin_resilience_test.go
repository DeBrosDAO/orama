package rqlite

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// A reply with no results says nothing about the table. Read as "no rows" it
// became an NXDOMAIN, cached for 30 s, for names that exist.
func TestServeDNS_aReplyWithNoResultsIsAServerFailureNotNXDOMAIN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(QueryResponse{})
	}))
	t.Cleanup(srv.Close)
	p := nxPlugin(t, srv, "example.test.")
	for _, qname := range []string{"web.example.test.", "missing.example.test."} {
		_, code, err := serveType(t, p, qname, dns.TypeA)
		if code != dns.RcodeServerFailure || err == nil {
			t.Fatalf("%s: got code %d err %v, want SERVFAIL", qname, code, err)
		}
		if _, negative := p.cache.Get(qname, dns.TypeA); negative {
			t.Fatalf("%s: an empty reply was cached as a negative answer", qname)
		}
	}
}

// Names a wildcard stands in for are cached as wildcard answers (short serve-
// stale window); a name's own records as ordinary ones; negatives as negatives.
func TestServeDNS_cachesEachAnswerWithTheLifetimeOfItsSource(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	for qname, want := range map[string]cacheClass{
		"web.example.test.":          classAnswer,
		"x.wild.example.test.":       classWildcard,
		"missing.example.test.":      classNegative,
		"sub.example.test.":          classNegative,
		"nothing.wild.example.test.": classNegative,
	} {
		qtype := dns.TypeA
		if want == classNegative {
			qtype = dns.TypeAAAA
		}
		serveType(t, p, qname, qtype)
		entry := p.cache.GetEntry(qname, qtype)
		if entry == nil || entry.class != want {
			t.Errorf("%s: cached as %v, want class %v", qname, entry, want)
		}
	}
	entry := p.cache.GetEntry("x.wild.example.test.", dns.TypeA)
	if entry == nil || entry.staleUntil.Sub(entry.expiresAt) > WildcardStaleWindow {
		t.Fatal("a wildcard answer is stale-usable for longer than WildcardStaleWindow")
	}
}

// Concurrent identical misses are one resolution: the database sees one set of
// lookups, and every caller gets the answer.
func TestServeDNS_concurrentIdenticalMissesShareOneResolution(t *testing.T) {
	const callers = 20
	srv := fakeZoneDB(t, nodataZone())
	var queries atomic.Int64
	var armed atomic.Bool
	release := make(chan struct{})
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() && queries.Add(1) == 1 {
			<-release // hold the first lookup until every caller has missed the cache
		}
		inner.ServeHTTP(w, r)
	})
	p := nxPlugin(t, srv, "example.test.")
	armed.Store(true)

	var wg sync.WaitGroup
	results := make([]int, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, code, err := serveType(t, p, "x.wild.example.test.", dns.TypeA)
			if err == nil && rec.Msg != nil && len(rec.Msg.Answer) == 1 {
				results[i] = code + 1
			}
		}()
	}
	for {
		if _, misses, _ := p.cache.Stats(); misses >= callers {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the last callers reach the shared call
	close(release)
	wg.Wait()

	for i, r := range results {
		if r != dns.RcodeSuccess+1 {
			t.Fatalf("caller %d did not get the wildcard's answer", i)
		}
	}
	// One resolution is 2 queries (typed, owners). Without sharing it is 2 x 20.
	if got := queries.Load(); got > 2*callers/4 {
		t.Fatalf("%d callers cost %d database queries, want one shared resolution (2)", callers, got)
	}
}

// A dead backend writes a line per interval, with the number it suppressed,
// not a line per query.
func TestServeDNS_aDeadBackendLogsOncePerInterval(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nil), "example.test.")
	core, logs := observer.New(zap.ErrorLevel)
	p.logger = zap.New(core)
	now := time.Unix(1_700_000_000, 0)
	p.failLog.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		serveType(t, p, fmt.Sprintf("n%d.example.test.", i), dns.TypeA)
	}
	if got := logs.Len(); got != 1 {
		t.Fatalf("100 failed queries logged %d lines, want 1", got)
	}

	now = now.Add(BackendErrorLogInterval)
	serveType(t, p, "again.example.test.", dns.TypeA)
	if got := logs.Len(); got != 2 {
		t.Fatalf("after the interval: %d lines, want 2", got)
	}
	if suppressed := logs.All()[1].ContextMap()["suppressed_since_last_line"]; suppressed != int64(99) {
		t.Fatalf("the second line reports %v suppressed, want 99", suppressed)
	}
}

// A sub-zone is never answered from its parent zone's wildcard.
func TestServeDNS_aSubZoneIsNotAnsweredFromItsParentsWildcard(t *testing.T) {
	zone := map[string][][]interface{}{
		"example.test. SOA":     {{"example.test.", "SOA", nodataSOA, float64(300)}},
		"sub.example.test. SOA": {{"sub.example.test.", "SOA", "ns3.sub.example.test. admin.sub.example.test. 2 3600 1800 604800 300", float64(300)}},
		"*.example.test. A":     {{"*.example.test.", "A", "192.0.2.50", float64(60)}},
	}
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.", "sub.example.test.")
	if rec, code, _ := serveType(t, p, "x.other.example.test.", dns.TypeA); code != dns.RcodeSuccess || len(rec.Msg.Answer) != 1 {
		t.Fatalf("the parent zone's own wildcard stopped answering: %d", code)
	}
	if _, code, _ := serveType(t, p, "x.sub.example.test.", dns.TypeA); code != dns.RcodeNameError {
		t.Fatalf("a name in the sub-zone was answered from the parent's wildcard: code %d, want NXDOMAIN", code)
	}
}
