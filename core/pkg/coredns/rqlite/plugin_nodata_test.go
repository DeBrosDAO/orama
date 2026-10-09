package rqlite

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/miekg/dns"
)

const nodataSOA = "ns1.example.test. admin.example.test. 1700000000 3600 1800 604800 300"

// nodataZone is a zone with one name of each kind: a name with an A record, one
// with only a TXT record, wildcards (of A, and of TXT), a namespace as
// CreateNamespaceRecords writes it (ns-ns1 and its wildcard), the apex, and
// sub.example.test., which owns nothing but has deep.sub.example.test. below it.
func nodataZone() map[string][][]interface{} {
	return map[string][][]interface{}{
		"example.test. SOA":         {{"example.test.", "SOA", nodataSOA, float64(300)}},
		"example.test. NS":          {{"example.test.", "NS", "ns1.example.test.", float64(300)}},
		"web.example.test. A":       {{"web.example.test.", "A", "192.0.2.10", float64(60)}},
		"txt.example.test. TXT":     {{"txt.example.test.", "TXT", "hello", float64(60)}},
		"*.wild.example.test. A":    {{"*.wild.example.test.", "A", "192.0.2.20", float64(60)}},
		"*.other.example.test. TXT": {{"*.other.example.test.", "TXT", "wild", float64(60)}},
		"ns-ns1.example.test. A":    {{"ns-ns1.example.test.", "A", "192.0.2.30", float64(60)}},
		"*.ns-ns1.example.test. A":  {{"*.ns-ns1.example.test.", "A", "192.0.2.30", float64(60)}},
		"deep.sub.example.test. A":  {{"deep.sub.example.test.", "A", "192.0.2.40", float64(60)}},
	}
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

// A name that exists is not NXDOMAIN for a type it lacks. The old answer made
// the resolvers that apply RFC 8020 cache the whole name as nonexistent after
// the first AAAA query, and the next A lookup failed with "no such host". Every
// way a name can exist is a row here: it owns a record, it is covered by a
// wildcard, it owns nothing but has names below it (an empty non-terminal), it
// is the apex.
func TestServeDNS_existingNameWithoutTheTypeIsNoData(t *testing.T) {
	cases := []struct {
		kind, qname string
		qtypes      []uint16
	}{
		{"owns another type (A)", "web.example.test.", []uint16{dns.TypeAAAA, dns.TypeTXT, dns.TypeMX, dns.TypeNS}},
		{"owns only a TXT", "txt.example.test.", []uint16{dns.TypeA, dns.TypeAAAA}},
		{"namespace name", "ns-ns1.example.test.", []uint16{dns.TypeAAAA, dns.TypeTXT, dns.TypeMX}},
		{"covered by a wildcard", "turn.ns-ns1.example.test.", []uint16{dns.TypeAAAA, dns.TypeTXT, dns.TypeMX}},
		{"covered by a wildcard of other types", "x.wild.example.test.", []uint16{dns.TypeAAAA}},
		{"empty non-terminal", "sub.example.test.", []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeTXT}},
		{"zone apex", "example.test.", []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeMX}},
	}
	for _, tc := range cases {
		for _, qtype := range tc.qtypes {
			t.Run(tc.kind+"/"+dns.TypeToString[qtype], func(t *testing.T) {
				p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
				rec, code, err := serveType(t, p, tc.qname, qtype)
				requireNoData(t, rec, code, err)
			})
		}
	}
}

// A wildcard stands in for a name that owns nothing and has nothing below it:
// the type it has is its answer, with the queried name as owner.
func TestServeDNS_wildcardAnswersItsType(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	for _, qname := range []string{"x.wild.example.test.", "turn.ns-ns1.example.test."} {
		rec, code, err := serveType(t, p, qname, dns.TypeA)
		if err != nil || code != dns.RcodeSuccess || len(rec.Msg.Answer) != 1 || rec.Msg.Answer[0].Header().Name != qname {
			t.Fatalf("%s: code %d err %v answers %v, want the wildcard's A owned by the queried name", qname, code, err, rec.Msg.Answer)
		}
	}
}

// A name nothing owns, covers or lives below is NXDOMAIN whatever the type. The
// names include string suffixes that are not label boundaries (ub, sub2).
func TestServeDNS_absentNameIsNXDOMAINForEveryType(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, nodataZone()), "example.test.")
	for _, qname := range []string{"missing.example.test.", "ns-other.example.test.", "x.sub2.example.test.", "ub.example.test.", "a.b.c.missing.example.test."} {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeTXT} {
			rec, code, err := serveType(t, p, qname, qtype)
			if err != nil || code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
				t.Errorf("%s %s: got code %d err %v, want NXDOMAIN", qname, dns.TypeToString[qtype], code, err)
			}
		}
	}
}

// With nothing active left the namespace is gone and every type is NXDOMAIN
// again (the real query reads only active rows).
func TestServeDNS_withdrawnNamespaceIsNXDOMAIN(t *testing.T) {
	zone := nodataZone()
	delete(zone, "ns-ns1.example.test. A")
	delete(zone, "*.ns-ns1.example.test. A")
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.")
	for _, qname := range []string{"ns-ns1.example.test.", "turn.ns-ns1.example.test."} {
		if _, code, _ := serveType(t, p, qname, dns.TypeAAAA); code != dns.RcodeNameError {
			t.Fatalf("%s: got code %d, want NXDOMAIN for a withdrawn namespace", qname, code)
		}
	}
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

// The same for a name that owns nothing but has names below it: it exists, so a
// wildcard above does not stand in for it. A wildcard record below a name makes
// the name exist too (*.wild.example.test. implies wild.example.test.).
func TestServeDNS_emptyNonTerminalIsNotCoveredByAWildcard(t *testing.T) {
	zone := nodataZone()
	zone["*.example.test. A"] = [][]interface{}{{"*.example.test.", "A", "192.0.2.50", float64(60)}}
	p := nxPlugin(t, fakeZoneDB(t, zone), "example.test.")
	for _, qname := range []string{"sub.example.test.", "wild.example.test."} {
		rec, code, err := serveType(t, p, qname, dns.TypeA)
		requireNoData(t, rec, code, err)
	}
	rec, code, err := serveType(t, p, "missing.example.test.", dns.TypeA)
	if err != nil || code != dns.RcodeSuccess || len(rec.Msg.Answer) != 1 {
		t.Fatalf("a name nothing owns is still the wildcard's: code %d err %v answers %v", code, err, rec.Msg.Answer)
	}
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
		for _, qname := range []string{"web.example.test.", "sub.example.test."} {
			rec, code, err := serveType(t, p, qname, dns.TypeAAAA)
			requireNoData(t, rec, code, err)
		}
		rec, code, err := serveType(t, p, "missing.example.test.", dns.TypeAAAA)
		if err != nil || code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
			t.Fatalf("pass %d: got code %d err %v, want NXDOMAIN", i, code, err)
		}
		if len(rec.Msg.Ns) != 1 {
			t.Fatalf("pass %d: NXDOMAIN has %d authority records", i, len(rec.Msg.Ns))
		}
	}
	if hits, _, _ := p.cache.Stats(); hits != 3 {
		t.Fatalf("cache hits = %d, want the second pass served from cache", hits)
	}
	if _, negative := p.cache.Get("web.example.test.", dns.TypeAAAA); !negative {
		t.Fatal("the NODATA answer was not cached")
	}
}

// The owners query is what says whether a missing type is NODATA, so when it
// fails the answer is SERVFAIL — a guessed NXDOMAIN would be the wrong answer
// this plugin used to give — and nothing is cached.
func TestServeDNS_ownersQueryFailureIsAServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [][]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sql, _ := body[0][0].(string); strings.Contains(sql, " IN (") {
			http.Error(w, "no leader", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(QueryResponse{Results: []QueryResult{{}}})
	}))
	t.Cleanup(srv.Close)
	p := nxPlugin(t, srv, "example.test.")
	for _, qname := range []string{"web.example.test.", "missing.example.test."} {
		_, code, err := serveType(t, p, qname, dns.TypeAAAA)
		if code != dns.RcodeServerFailure || err == nil {
			t.Fatalf("%s: got code %d err %v, want SERVFAIL", qname, code, err)
		}
		if _, negative := p.cache.Get(qname, dns.TypeAAAA); negative {
			t.Fatalf("%s: a failed existence check was cached as a negative answer", qname)
		}
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

// The cost of an answer, in rqlite queries, does not grow with the depth of the
// name: a flood of deep random names must not become a query per label against
// the index rqlite. An exact hit is the typed query alone; a name answered from
// a wildcard adds the owners query (the name, its wildcard candidates and what
// lives below it, together); every negative answer adds the zone SOA.
func TestServeDNS_queryCountIsConstantInTheNameDepth(t *testing.T) {
	srv := fakeZoneDB(t, nodataZone())
	var queries atomic.Int64
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		inner.ServeHTTP(w, r)
	})
	cases := []struct {
		kind    string
		suffix  string
		qtype   uint16
		rcode   int
		queries int64
	}{
		{"exact hit", "web.example.test.", dns.TypeA, dns.RcodeSuccess, 1},
		{"answered by a wildcard", "wild.example.test.", dns.TypeA, dns.RcodeSuccess, 2},
		{"NODATA by a wildcard", "wild.example.test.", dns.TypeAAAA, dns.RcodeSuccess, 3},
		{"NODATA of an empty non-terminal", "sub.example.test.", dns.TypeA, dns.RcodeSuccess, 3},
		{"NXDOMAIN", "missing.example.test.", dns.TypeA, dns.RcodeNameError, 3},
	}
	for _, tc := range cases {
		for _, depth := range []int{1, 4, 12} {
			p := nxPlugin(t, srv, "example.test.")
			qname := tc.suffix
			if tc.kind != "exact hit" && tc.kind != "NODATA of an empty non-terminal" {
				qname = strings.Repeat("l.", depth) + tc.suffix
			}
			queries.Store(0)
			rec, code, err := serveType(t, p, qname, tc.qtype)
			if err != nil || code != tc.rcode || rec.Msg.Rcode != tc.rcode {
				t.Fatalf("%s depth %d: got code %d err %v, want rcode %d", tc.kind, depth, code, err, tc.rcode)
			}
			if got := queries.Load(); got != tc.queries {
				t.Errorf("%s depth %d: %d rqlite queries, want %d", tc.kind, depth, got, tc.queries)
			}
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
