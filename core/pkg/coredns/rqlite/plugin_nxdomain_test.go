package rqlite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

// fakeZoneDB serves rqlite /db/query for the records in rows, keyed by
// "<fqdn> <type>". A nil map answers every query with a 500.
func fakeZoneDB(t *testing.T, rows map[string][][]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rows == nil {
			http.Error(w, "no leader", http.StatusServiceUnavailable)
			return
		}
		var body [][]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		if sql, _ := body[0][0].(string); strings.Contains(sql, "substr(") {
			_ = json.NewEncoder(w).Encode(QueryResponse{Results: []QueryResult{{Values: nameExists(rows, body[0][1:])}}})
			return
		}
		if len(body[0]) != 3 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		key := body[0][1].(string) + " " + body[0][2].(string)
		_ = json.NewEncoder(w).Encode(QueryResponse{Results: []QueryResult{{Values: rows[key]}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// nameExists answers Backend.NameExists's query the way SQLite would: args are
// the exact names, then the substr start (negative: counted from the end) and
// the suffix it must equal.
func nameExists(rows map[string][][]interface{}, args []interface{}) [][]interface{} {
	names := args[:len(args)-2]
	fromEnd := -int(args[len(args)-2].(float64))
	suffix := args[len(args)-1].(string)
	for key := range rows {
		fqdn, _, _ := strings.Cut(key, " ")
		for _, n := range names {
			if fqdn == n {
				return [][]interface{}{{float64(1)}}
			}
		}
		if len(fqdn) >= fromEnd && fqdn[len(fqdn)-fromEnd:] == suffix {
			return [][]interface{}{{float64(1)}}
		}
	}
	return nil
}

func nxPlugin(t *testing.T, srv *httptest.Server, zones ...string) *RQLitePlugin {
	t.Helper()
	client, err := NewRQLiteClient(srv.URL, zap.NewNop(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return &RQLitePlugin{
		zones:   zones,
		logger:  zap.NewNop(),
		backend: &Backend{client: client, logger: zap.NewNop(), healthy: true},
		cache:   NewCache(100, time.Minute),
	}
}

func serveA(t *testing.T, p *RQLitePlugin, qname string) (*dnstest.Recorder, int, error) {
	t.Helper()
	return serveType(t, p, qname, dns.TypeA)
}

func serveType(t *testing.T, p *RQLitePlugin, qname string, qtype uint16) (*dnstest.Recorder, int, error) {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion(qname, qtype)
	rec := dnstest.NewRecorder(&test.ResponseWriter{})
	code, err := p.ServeDNS(context.Background(), rec, req)
	return rec, code, err
}

func TestHandleNXDomain_carriesTheZonesOwnSOA(t *testing.T) {
	// The zone's ns1 slot was released: ns2 is the lowest glued slot, so the
	// SOA the nameserver component wrote names it as the primary.
	srv := fakeZoneDB(t, map[string][][]interface{}{
		"example.test. SOA": {{"example.test.", "SOA", "ns2.example.test. admin.example.test. 1700000000 3600 1800 604800 300", float64(600)}},
	})
	p := nxPlugin(t, srv, "example.test.")

	rec, code, err := serveA(t, p, "missing.example.test.")
	if err != nil || code != dns.RcodeNameError {
		t.Fatalf("got rcode %d err %v, want NXDOMAIN", code, err)
	}
	if len(rec.Msg.Ns) != 1 {
		t.Fatalf("authority section has %d records, want the zone SOA", len(rec.Msg.Ns))
	}
	soa, ok := rec.Msg.Ns[0].(*dns.SOA)
	if !ok {
		t.Fatalf("authority record is %T, want *dns.SOA", rec.Msg.Ns[0])
	}
	if soa.Ns != "ns2.example.test." {
		t.Errorf("SOA primary = %s, want the zone's own ns2.example.test. (never an invented ns1)", soa.Ns)
	}
	if soa.Serial != 1700000000 {
		t.Errorf("SOA serial = %d, want the stored 1700000000", soa.Serial)
	}
	if soa.Hdr.Name != "example.test." {
		t.Errorf("SOA owner = %s, want the zone apex", soa.Hdr.Name)
	}
	if soa.Hdr.Ttl != 300 {
		t.Errorf("SOA TTL = %d, want min(record TTL 600, minimum 300) = 300 (RFC 2308)", soa.Hdr.Ttl)
	}
}

func TestHandleNXDomain_usesTheMostSpecificZone(t *testing.T) {
	srv := fakeZoneDB(t, map[string][][]interface{}{
		"example.test. SOA":     {{"example.test.", "SOA", "ns1.example.test. admin.example.test. 1 3600 1800 604800 300", float64(300)}},
		"sub.example.test. SOA": {{"sub.example.test.", "SOA", "ns3.sub.example.test. admin.sub.example.test. 2 3600 1800 604800 300", float64(300)}},
	})
	p := nxPlugin(t, srv, "example.test.", "sub.example.test.")

	rec, code, _ := serveA(t, p, "gone.sub.example.test.")
	if code != dns.RcodeNameError || len(rec.Msg.Ns) != 1 {
		t.Fatalf("got rcode %d with %d authority records", code, len(rec.Msg.Ns))
	}
	if soa := rec.Msg.Ns[0].(*dns.SOA); soa.Ns != "ns3.sub.example.test." || soa.Hdr.Name != "sub.example.test." {
		t.Fatalf("SOA %s owned by %s, want sub.example.test.'s own", soa.Ns, soa.Hdr.Name)
	}
}

func TestHandleNXDomain_zoneWithoutSOAIsAServerFailure(t *testing.T) {
	// No slot is glued yet, so no SOA exists. Inventing one is what the old
	// code did; the honest answer is that this server cannot answer yet.
	srv := fakeZoneDB(t, map[string][][]interface{}{})
	p := nxPlugin(t, srv, "example.test.")

	_, code, err := serveA(t, p, "missing.example.test.")
	if code != dns.RcodeServerFailure || err == nil {
		t.Fatalf("got rcode %d err %v, want SERVFAIL with the missing-SOA error", code, err)
	}
	if _, negative := p.cache.Get("missing.example.test.", dns.TypeA); negative {
		t.Fatal("an answer without the zone's SOA was cached as NXDOMAIN")
	}
}

func TestHandleNXDomain_backendDownIsAServerFailure(t *testing.T) {
	srv := fakeZoneDB(t, nil)
	p := nxPlugin(t, srv, "example.test.")

	_, code, err := serveA(t, p, "missing.example.test.")
	if code != dns.RcodeServerFailure || err == nil {
		t.Fatalf("got rcode %d err %v, want SERVFAIL", code, err)
	}
}

// zoneWithNamespace is a zone whose only names are a namespace's A records —
// the apex SOA, ns-ns1.<zone> and its wildcard — as CreateNamespaceRecords
// writes them.
func zoneWithNamespace() map[string][][]interface{} {
	return map[string][][]interface{}{
		"example.test. SOA":        {{"example.test.", "SOA", "ns1.example.test. admin.example.test. 1 3600 1800 604800 300", float64(300)}},
		"ns-ns1.example.test. A":   {{"ns-ns1.example.test.", "A", "192.0.2.10", float64(60)}},
		"*.ns-ns1.example.test. A": {{"*.ns-ns1.example.test.", "A", "192.0.2.10", float64(60)}},
		"deep.sub.example.test. A": {{"deep.sub.example.test.", "A", "192.0.2.11", float64(60)}},
		"example.test. NS":         {{"example.test.", "NS", "ns1.example.test.", float64(300)}},
		"ns1.example.test. A":      {{"ns1.example.test.", "A", "192.0.2.1", float64(300)}},
	}
}

// TestHandleNegative_existingNameWithoutTheTypeIsNODATA reproduces the stagenet
// failure: AAAA, NS and TXT for ns-<name>.<base> were answered NXDOMAIN while
// its A record existed, so a resolver that asked any of them concluded the name
// did not exist and answered "no such host" to the A query as well.
func TestHandleNegative_existingNameWithoutTheTypeIsNODATA(t *testing.T) {
	cases := []struct {
		name  string
		qname string
	}{
		{"name with records", "ns-ns1.example.test."},
		{"covered by a wildcard", "turn.ns-ns1.example.test."},
		{"empty non-terminal", "sub.example.test."},
		{"zone apex", "example.test."},
	}
	for _, tc := range cases {
		for _, qtype := range []uint16{dns.TypeAAAA, dns.TypeTXT, dns.TypeMX} {
			t.Run(tc.name+"/"+dns.TypeToString[qtype], func(t *testing.T) {
				p := nxPlugin(t, fakeZoneDB(t, zoneWithNamespace()), "example.test.")

				rec, code, err := serveType(t, p, tc.qname, qtype)
				if err != nil || code != dns.RcodeSuccess {
					t.Fatalf("got rcode %s err %v, want NOERROR (NODATA): the name exists", dns.RcodeToString[code], err)
				}
				if rec.Msg.Rcode != dns.RcodeSuccess || len(rec.Msg.Answer) != 0 {
					t.Fatalf("message rcode %d with %d answers, want NOERROR and none", rec.Msg.Rcode, len(rec.Msg.Answer))
				}
				if len(rec.Msg.Ns) != 1 {
					t.Fatalf("authority section has %d records, want the zone SOA for negative caching", len(rec.Msg.Ns))
				}
				if _, ok := rec.Msg.Ns[0].(*dns.SOA); !ok {
					t.Fatalf("authority record is %T, want *dns.SOA", rec.Msg.Ns[0])
				}
				if !rec.Msg.Authoritative {
					t.Error("a NODATA answer must be authoritative")
				}
			})
		}
	}
}

func TestHandleNegative_absentNameStaysNXDOMAIN(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, zoneWithNamespace()), "example.test.")

	for _, qname := range []string{"missing.example.test.", "ns-other.example.test.", "x.sub2.example.test.", "ub.example.test."} {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA} {
			if _, code, _ := serveType(t, p, qname, qtype); code != dns.RcodeNameError {
				t.Errorf("%s %s: got rcode %s, want NXDOMAIN", qname, dns.TypeToString[qtype], dns.RcodeToString[code])
			}
		}
	}
}

func TestHandleNegative_withdrawnNamespaceIsNXDOMAIN(t *testing.T) {
	// The fake serves only active rows, like the real query; with nothing
	// active the namespace is gone and every type is NXDOMAIN again.
	rows := zoneWithNamespace()
	delete(rows, "ns-ns1.example.test. A")
	delete(rows, "*.ns-ns1.example.test. A")
	p := nxPlugin(t, fakeZoneDB(t, rows), "example.test.")

	if _, code, _ := serveType(t, p, "ns-ns1.example.test.", dns.TypeAAAA); code != dns.RcodeNameError {
		t.Fatalf("got rcode %s, want NXDOMAIN for a withdrawn namespace", dns.RcodeToString[code])
	}
}

func TestHandleNegative_cachedNODATAIsServedAsNODATA(t *testing.T) {
	// The cache replays with SetReply, which resets the rcode; the second
	// answer must still be NOERROR/empty, not NXDOMAIN and not a bare success.
	p := nxPlugin(t, fakeZoneDB(t, zoneWithNamespace()), "example.test.")

	for i, want := range []string{"database", "cache"} {
		rec, code, err := serveType(t, p, "ns-ns1.example.test.", dns.TypeAAAA)
		if err != nil || code != dns.RcodeSuccess || rec.Msg.Rcode != dns.RcodeSuccess || len(rec.Msg.Ns) != 1 {
			t.Fatalf("answer %d from the %s: code %d rcode %d authority %d err %v", i+1, want, code, rec.Msg.Rcode, len(rec.Msg.Ns), err)
		}
	}
	if hits, _, _ := p.cache.Stats(); hits != 1 {
		t.Fatalf("cache hits = %d, want the second answer served from cache", hits)
	}
}

func TestHandleNegative_cachedNXDOMAINIsStillNXDOMAIN(t *testing.T) {
	p := nxPlugin(t, fakeZoneDB(t, zoneWithNamespace()), "example.test.")

	for range 2 {
		rec, code, _ := serveA(t, p, "missing.example.test.")
		if code != dns.RcodeNameError || rec.Msg.Rcode != dns.RcodeNameError {
			t.Fatalf("code %d rcode %d, want NXDOMAIN both times", code, rec.Msg.Rcode)
		}
	}
}

func TestHandleNegative_existenceCheckFailureIsAServerFailure(t *testing.T) {
	// The type lookups answer; the existence query fails. Guessing NXDOMAIN
	// here is the wrong answer this change removes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [][]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sql, _ := body[0][0].(string); strings.Contains(sql, "substr(") {
			http.Error(w, "no leader", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(QueryResponse{Results: []QueryResult{{}}})
	}))
	t.Cleanup(srv.Close)
	p := nxPlugin(t, srv, "example.test.")

	_, code, err := serveType(t, p, "ns-ns1.example.test.", dns.TypeAAAA)
	if code != dns.RcodeServerFailure || err == nil {
		t.Fatalf("got rcode %d err %v, want SERVFAIL", code, err)
	}
	if _, negative := p.cache.Get("ns-ns1.example.test.", dns.TypeAAAA); negative {
		t.Fatal("an unanswered existence check was cached as a negative answer")
	}
}

func TestZoneWildcards_stopsAtTheZoneEdge(t *testing.T) {
	p := wildcardPlugin("example.test.")
	got := p.zoneWildcards("a.ns-ns1.example.test.")
	want := []string{"*.ns-ns1.example.test.", "*.example.test."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (nothing above the zone)", got, want)
	}
	if got := p.zoneWildcards("outside.other."); len(got) != 0 {
		t.Fatalf("got %v for a name outside the zone", got)
	}
}
