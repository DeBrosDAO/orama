package rqlite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || len(body[0]) != 3 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		key := body[0][1].(string) + " " + body[0][2].(string)
		_ = json.NewEncoder(w).Encode(QueryResponse{Results: []QueryResult{{Values: rows[key]}}})
	}))
	t.Cleanup(srv.Close)
	return srv
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
	req := new(dns.Msg)
	req.SetQuestion(qname, dns.TypeA)
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
