package rqlite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/miekg/dns"
)

func TestParentName(t *testing.T) {
	cases := map[string]string{
		"a.b.c.":        "b.c.",
		"b.c.":          "c.",
		"c.":            "",
		".":             "",
		"":              "",
		`a\.b.c.d.`:     "c.d.",
		"*.ns-x.base.":  "ns-x.base.",
		"single-label.": "",
	}
	for in, want := range cases {
		if got := parentName(in); got != want {
			t.Errorf("parentName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The set holds each ancestor once, never a name that is only a string suffix
// of another (ub.example.test. of sub.example.test.), never a record's own name
// unless another record is below it, and never the root.
func TestAncestorsOf_labelBoundariesAndSharing(t *testing.T) {
	got := ancestorsOf([]string{"deep.sub.example.test.", "x.sub.example.test.", "example.test.", "*.wild.example.test."})
	want := []string{"sub.example.test.", "example.test.", "test.", "wild.example.test."}
	if len(got) != len(want) {
		t.Fatalf("got %d ancestors %v, want %v", len(got), got, want)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("%s is missing from %v", name, got)
		}
	}
	for _, name := range []string{"ub.example.test.", "deep.sub.example.test.", "."} {
		if _, ok := got[name]; ok {
			t.Errorf("%s must not be an ancestor", name)
		}
	}
}

// Memory follows the records: one entry per distinct ancestor, at most
// (labels x records), and records that share a parent share its entries. After
// the records go the entries go at the next refresh.
func TestAncestorSet_isBoundedByRecordsAndShrinksWithThem(t *testing.T) {
	const records, labels = 500, 4 // <name-i>.<tenant>.example.test.: 4 labels
	rows := map[string][][]interface{}{}
	for i := 0; i < records; i++ {
		fqdn := fmt.Sprintf("n%d.t%d.example.test.", i, i%10)
		rows[fqdn+" A"] = [][]interface{}{{fqdn, "A", "192.0.2.1", float64(60)}}
	}
	srv, db := newZoneDB(t, rows)
	p := nxPlugin(t, srv, "example.test.")

	// 10 tenants + example.test. + test.: shared parents are held once.
	if got := p.backend.below.size(); got != 12 {
		t.Fatalf("the set holds %d names for %d records under 10 parents, want 12", got, records)
	}
	if got, limit := p.backend.below.size(), records*(labels-1); got > limit {
		t.Fatalf("the set holds %d names, more than labels x records = %d", got, limit)
	}

	if _, err := db.Exec(`DELETE FROM dns_records`); err != nil {
		t.Fatal(err)
	}
	if err := p.backend.refreshAncestors(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.backend.below.size(); got != 0 {
		t.Fatalf("the set still holds %d names after every record is gone", got)
	}
}

func TestAncestorSet_concurrentLearnAndReplaceAreSafe(t *testing.T) {
	var s ancestorSet
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.learn(fmt.Sprintf("a%d.b%d.example.test.", i, j%7))
				s.has("b1.example.test.")
				if j%50 == 0 {
					s.replace(ancestorsOf([]string{"x.example.test."}))
				}
			}
		}()
	}
	wg.Wait()
}

// An empty non-terminal is answered from the set built at start-up, then
// follows the table: a descendant created later makes the name exist after the
// next refresh, and one removed makes it NXDOMAIN again after the next.
func TestHasBelow_followsTheTableAtEachRefresh(t *testing.T) {
	srv, db := newZoneDB(t, nodataZone())
	p := nxPlugin(t, srv, "example.test.")
	ctx := context.Background()

	if !p.backend.HasBelow("sub.example.test.") || p.backend.HasBelow("later.example.test.") {
		t.Fatal("the set built at start is wrong")
	}

	insertRecord(t, db, "leaf.later.example.test.", "A", "192.0.2.77", 60, true)
	if p.backend.HasBelow("later.example.test.") {
		t.Fatal("a record the node has not seen made its parent exist before any refresh (the answer is allowed to lag by one refresh, not to guess)")
	}
	rec, code, err := serveType(t, p, "later.example.test.", dns.TypeA)
	if err != nil || code != dns.RcodeNameError {
		t.Fatalf("before the refresh: got %d %v %v, want the stale NXDOMAIN", code, err, rec.Msg)
	}

	p.cache.Clear()
	if err := p.backend.refreshAncestors(ctx); err != nil {
		t.Fatal(err)
	}
	rec, code, err = serveType(t, p, "later.example.test.", dns.TypeA)
	requireNoData(t, rec, code, err)

	if _, err := db.Exec(`DELETE FROM dns_records WHERE fqdn = 'leaf.later.example.test.'`); err != nil {
		t.Fatal(err)
	}
	p.cache.Clear()
	if err := p.backend.refreshAncestors(ctx); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := serveType(t, p, "later.example.test.", dns.TypeA); code != dns.RcodeNameError {
		t.Fatalf("after the descendant went and the refresh: got %d, want NXDOMAIN", code)
	}
}

// An indexed lookup that finds a record teaches the node its ancestors at once,
// without waiting for a refresh: the first query for the new name's own
// address is enough to make its parent exist.
func TestHasBelow_isLearnedFromAnIndexedLookup(t *testing.T) {
	srv, db := newZoneDB(t, nodataZone())
	p := nxPlugin(t, srv, "example.test.")

	insertRecord(t, db, "app.fresh.example.test.", "A", "192.0.2.88", 60, true)
	if _, code, _ := serveType(t, p, "app.fresh.example.test.", dns.TypeA); code != dns.RcodeSuccess {
		t.Fatalf("the new record does not resolve: %d", code)
	}
	rec, code, err := serveType(t, p, "fresh.example.test.", dns.TypeAAAA)
	requireNoData(t, rec, code, err)
}

// Records that are not active do not exist, neither as names nor as the
// descendants that make a parent exist.
func TestServeDNS_inactiveRowsDoNotMakeANameExist(t *testing.T) {
	srv, db := newZoneDB(t, nodataZone())
	insertRecord(t, db, "old.example.test.", "A", "192.0.2.5", 60, false)
	insertRecord(t, db, "leaf.gone.example.test.", "A", "192.0.2.6", 60, false)
	p := nxPlugin(t, srv, "example.test.")
	for _, qname := range []string{"old.example.test.", "gone.example.test.", "leaf.gone.example.test."} {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA} {
			if _, code, _ := serveType(t, p, qname, qtype); code != dns.RcodeNameError {
				t.Errorf("%s %s: got %d, want NXDOMAIN for a name only inactive rows hold", qname, dns.TypeToString[qtype], code)
			}
		}
	}
}

// A cache miss reads the table only through the index: by name (fqdn = ?, fqdn
// IN (...)), never by scanning it, however many distinct names ask. The names
// the zone holds are read on the refresh cadence, by nothing a query does.
func TestServeDNS_aMissNeverScansTheTable(t *testing.T) {
	srv, _ := newZoneDB(t, nodataZone())
	var mu sync.Mutex
	var statements []string
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		statements = append(statements, string(body))
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	})
	p := nxPlugin(t, srv, "example.test.")
	mu.Lock()
	statements = nil
	mu.Unlock()

	names := []string{"web.example.test.", "sub.example.test.", "x.wild.example.test.", "turn.ns-ns1.example.test."}
	for i := 0; i < 50; i++ {
		names = append(names, fmt.Sprintf("random-%d.deep.example.test.", i))
	}
	for _, qname := range names {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA} {
			serveType(t, p, qname, qtype)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(statements) == 0 {
		t.Fatal("no statement was recorded")
	}
	for _, stmt := range statements {
		if !strings.Contains(stmt, "fqdn = ?") && !strings.Contains(stmt, "fqdn IN (") {
			t.Errorf("a query reads the table without a name to look up: %s", stmt)
		}
		for _, scan := range []string{"substr(", "LIKE", "DISTINCT"} {
			if strings.Contains(stmt, scan) {
				t.Errorf("a query scans the table (%s): %s", scan, stmt)
			}
		}
	}
}
