package rqlite

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

const nodataSOA = "ns1.example.test. admin.example.test. 1700000000 3600 1800 604800 300"

func nodataZone() map[string][][]interface{} {
	return map[string][][]interface{}{
		"example.test. SOA":         {{"example.test.", "SOA", nodataSOA, float64(300)}},
		"web.example.test. A":       {{"web.example.test.", "A", "192.0.2.10", float64(60)}},
		"txt.example.test. TXT":     {{"txt.example.test.", "TXT", "hello", float64(60)}},
		"*.wild.example.test. A":    {{"*.wild.example.test.", "A", "192.0.2.20", float64(60)}},
		"*.other.example.test. TXT": {{"*.other.example.test.", "TXT", "wild", float64(60)}},
	}
}

func serveType(t *testing.T, p *RQLitePlugin, qname string, qtype uint16) (*dnstest.Recorder, int, error) {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion(qname, qtype)
	rec := dnstest.NewRecorder(&test.ResponseWriter{})
	code, err := p.ServeDNS(context.Background(), rec, req)
	return rec, code, err
}

// requireNoData fails unless the reply is NOERROR, empty, and carries the
// zone's SOA for negative caching.
func requireNoData(t *testing.T, rec *dnstest.Recorder, code int, err error) {
	t.Helper()
	if err != nil || code != dns.RcodeSuccess || rec.Msg == nil || rec.Msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("got code %d err %v msg %v, want NOERROR (NODATA)", code, err, rec.Msg)
	}
	if len(rec.Msg.Answer) != 0 {
		t.Fatalf("NODATA carries answers: %v", rec.Msg.Answer)
	}
	if len(rec.Msg.Ns) != 1 {
		t.Fatalf("NODATA has %d authority records, want the zone SOA", len(rec.Msg.Ns))
	}
	if _, ok := rec.Msg.Ns[0].(*dns.SOA); !ok {
		t.Fatalf("authority record is %T, want *dns.SOA", rec.Msg.Ns[0])
	}
	if !rec.Msg.Authoritative {
		t.Fatal("NODATA is not authoritative")
	}
}

// A name that is being served is not NXDOMAIN for a type it lacks. The old
// answer made the resolvers that apply RFC 8020 cache the whole name as
// nonexistent after the first AAAA query, and the next A lookup failed with
// "no such host".
func TestServeDNS_missingTypeOfAnExistingNameIsNoData(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	rec, code, err := serveType(t, p, "web.example.test.", dns.TypeAAAA)
	requireNoData(t, rec, code, err)
}

func TestServeDNS_otherTypeOfAnExistingNameIsNoData(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	rec, code, err := serveType(t, p, "txt.example.test.", dns.TypeA)
	requireNoData(t, rec, code, err)
}

func TestServeDNS_apexMissingTypeIsNoData(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	rec, code, err := serveType(t, p, "example.test.", dns.TypeAAAA)
	requireNoData(t, rec, code, err)
}

func TestServeDNS_missingNameIsStillNXDOMAINForEveryType(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeTXT} {
		rec, code, err := serveType(t, p, "missing.example.test.", qtype)
		if err != nil || code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
			t.Fatalf("%s: got code %d err %v, want NXDOMAIN", dns.TypeToString[qtype], code, err)
		}
	}
}

// A wildcard stands in for a name that owns nothing, so the name exists
// through it: the type it lacks is NODATA, the type it has is its answer.
func TestServeDNS_wildcardAnswersItsTypeAndIsNoDataForOthers(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	rec, code, err := serveType(t, p, "x.wild.example.test.", dns.TypeA)
	if err != nil || code != dns.RcodeSuccess || len(rec.Msg.Answer) != 1 {
		t.Fatalf("wildcard A: code %d err %v answers %v", code, err, rec.Msg.Answer)
	}
	rec, code, err = serveType(t, p, "x.wild.example.test.", dns.TypeAAAA)
	requireNoData(t, rec, code, err)
}

// A name that owns a record of another type is not covered by a wildcard of
// the type asked for (RFC 4592): its answer is NODATA, not the wildcard's.
func TestServeDNS_existingNameIsNotCoveredByAWildcard(t *testing.T) {
	zone := nodataZone()
	zone["*.example.test. AAAA"] = [][]interface{}{{"*.example.test.", "AAAA", "2001:db8::1", float64(60)}}
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.")
	rec, code, err := serveType(t, p, "web.example.test.", dns.TypeAAAA)
	requireNoData(t, rec, code, err)
}

// The nearest wildcard decides: one that lacks the type is NODATA even when a
// farther one has it.
func TestServeDNS_nearestWildcardWithoutTheTypeIsNoData(t *testing.T) {
	zone := nodataZone()
	zone["*.example.test. A"] = [][]interface{}{{"*.example.test.", "A", "192.0.2.30", float64(60)}}
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.")
	rec, code, err := serveType(t, p, "y.other.example.test.", dns.TypeA)
	requireNoData(t, rec, code, err)
}

// The cached negative answer keeps its rcode: NODATA must not come back from
// the cache as NXDOMAIN, nor NXDOMAIN as NODATA.
func TestServeDNS_cachedNegativeAnswersKeepTheirRcode(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	for i := 0; i < 2; i++ {
		rec, code, err := serveType(t, p, "web.example.test.", dns.TypeAAAA)
		requireNoData(t, rec, code, err)
		rec, code, err = serveType(t, p, "missing.example.test.", dns.TypeAAAA)
		if err != nil || code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
			t.Fatalf("pass %d: got code %d err %v, want NXDOMAIN", i, code, err)
		}
		if len(rec.Msg.Ns) != 1 {
			t.Fatalf("pass %d: NXDOMAIN has %d authority records", i, len(rec.Msg.Ns))
		}
	}
	if _, negative := p.cache.Get("web.example.test.", dns.TypeAAAA); !negative {
		t.Fatal("the NODATA answer was not cached")
	}
}

func TestServeDNS_noDataWithoutZoneSOAIsAServerFailure(t *testing.T) {
	zone := nodataZone()
	delete(zone, "example.test. SOA")
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.")
	_, code, err := serveType(t, p, "web.example.test.", dns.TypeAAAA)
	if code != dns.RcodeServerFailure || err == nil {
		t.Fatalf("got code %d err %v, want SERVFAIL: a zone with no SOA cannot give a negative answer", code, err)
	}
	if _, negative := p.cache.Get("web.example.test.", dns.TypeAAAA); negative {
		t.Fatal("an answer without the zone's SOA was cached")
	}
}

// A miss costs the typed query, one query for the name and all its wildcard
// candidates together, and the SOA, however deep the name is: a flood of deep
// random names must not become a query per label against the index rqlite.
func TestServeDNS_missCostIsConstantInTheNameDepth(t *testing.T) {
	srv := fakeZoneDB(t, nodataZone())
	var queries atomic.Int64
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		inner.ServeHTTP(w, r)
	})
	p := nxPlugin(t, srv, "example.test.")
	for _, depth := range []int{1, 4, 12} {
		queries.Store(0)
		name := strings.Repeat("l.", depth) + "missing.example.test."
		rec, code, err := serveType(t, p, name, dns.TypeA)
		if err != nil || code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
			t.Fatalf("depth %d: got code %d err %v, want NXDOMAIN", depth, code, err)
		}
		if got := queries.Load(); got != 3 {
			t.Errorf("depth %d: %d rqlite queries for one miss, want 3 (typed, owners, SOA)", depth, got)
		}
	}
}

// Only candidates inside the zone are asked for: the walk stops at the zone
// edge, and nothing past it is bound into the owners query.
func TestServeDNS_ownersQueryNamesOnlyTheZonesCandidates(t *testing.T) {
	srv := fakeZoneDB(t, nodataZone())
	var bound atomic.Value
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), " IN (") {
			bound.Store(string(body))
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	})
	p := nxPlugin(t, srv, "example.test.")
	if _, code, _ := serveType(t, p, "a.b.missing.example.test.", dns.TypeA); code != dns.RcodeNameError {
		t.Fatalf("got code %d, want NXDOMAIN", code)
	}
	body, _ := bound.Load().(string)
	for _, want := range []string{`"a.b.missing.example.test."`, `"*.b.missing.example.test."`, `"*.missing.example.test."`, `"*.example.test."`} {
		if !strings.Contains(body, want) {
			t.Errorf("owners query %s does not bind %s", body, want)
		}
	}
	if strings.Contains(body, `"*.test."`) {
		t.Errorf("owners query %s binds a name outside the zone", body)
	}
}
