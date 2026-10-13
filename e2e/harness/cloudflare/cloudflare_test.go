package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sub = "e2e-ab12cd.dbrsteting.bid"

func TestNew_refusesOtherZones(t *testing.T) {
	cases := map[string]string{"com": "bare TLD", "example.com": "not dbrsteting.bid", "": "bare TLD", "sub.dbrsteting.bid": "not"}
	for zone, want := range cases {
		if _, err := New("t", zone, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("zone %q: %v, want %q", zone, err, want)
		}
	}
	if _, err := New("", AllowedZone, ""); err == nil || !strings.Contains(err.Error(), "CF_API_TOKEN") {
		t.Errorf("empty token: %v", err)
	}
	if _, err := New("t", "DBRSTETING.bid.", ""); err != nil {
		t.Errorf("the allowed zone in another case: %v", err)
	}
}

func TestDelegate_createsNSAndGlue(t *testing.T) {
	f, c := newFakeCF(t)
	nss := []Nameserver{{"ns1", "203.0.113.1"}, {"ns2", "203.0.113.2"}}
	if err := c.Delegate(context.Background(), sub, nss); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if len(f.records) != 4 {
		t.Fatalf("%d records, want 4: %+v", len(f.records), f.records)
	}
	// A second delegation that says the same writes nothing.
	before := len(f.calls)
	if err := c.Delegate(context.Background(), sub, nss); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls[before:] {
		if !strings.HasPrefix(call, "GET") {
			t.Fatalf("idempotent Delegate wrote: %s", call)
		}
	}
}

func TestDelegate_updatesMovedGlue(t *testing.T) {
	f, c := newFakeCF(t)
	f.add("A", "ns1."+sub, "203.0.113.50")
	if err := c.Delegate(context.Background(), sub, []Nameserver{{"ns1", "203.0.113.1"}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.records {
		if r.Type == "A" && r.Content != "203.0.113.1" {
			t.Fatalf("glue not updated: %+v", r)
		}
	}
}

func TestDelegate_refusesOutsideRunSubdomain(t *testing.T) {
	_, c := newFakeCF(t)
	for _, name := range []string{"dbrsteting.bid", "www.dbrsteting.bid", "x.e2e-ab12cd.dbrsteting.bid", "e2e-AB.dbrsteting.bid", "e2e-ab12cd.example.com"} {
		if err := c.Delegate(context.Background(), name, []Nameserver{{"ns1", "203.0.113.1"}}); err == nil {
			t.Errorf("Delegate(%q) was allowed", name)
		}
	}
}

func TestDelegate_refusesBadNameservers(t *testing.T) {
	_, c := newFakeCF(t)
	for _, ns := range []Nameserver{{"www", "203.0.113.1"}, {"ns1", "not-an-ip"}, {"ns1", "2001:db8::1"}} {
		if err := c.Delegate(context.Background(), sub, []Nameserver{ns}); err == nil {
			t.Errorf("nameserver %+v was accepted", ns)
		}
	}
	if err := c.Delegate(context.Background(), sub, nil); err == nil {
		t.Error("an empty delegation was accepted")
	}
}

func TestDeleteUnder_removesOnlyTheRunsRecords(t *testing.T) {
	f, c := newFakeCF(t)
	f.add("NS", sub, "ns1."+sub)
	f.add("A", "ns1."+sub, "203.0.113.1")
	f.add("A", "www.dbrsteting.bid", "203.0.113.9")
	f.add("A", "e2e-other1.dbrsteting.bid", "203.0.113.8")
	deleted, err := c.DeleteUnder(context.Background(), sub)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("DeleteUnder: %v deleted %d", err, len(deleted))
	}
	if len(f.records) != 2 {
		t.Fatalf("records left %+v", f.records)
	}
}

func TestDeleteUnder_nothingThereIsIdempotent(t *testing.T) {
	_, c := newFakeCF(t)
	deleted, err := c.DeleteUnder(context.Background(), sub)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("DeleteUnder on nothing: %v %d", err, len(deleted))
	}
}

func TestDeleteUnder_failsWhenRecordsSurvive(t *testing.T) {
	f, c := newFakeCF(t)
	f.add("NS", sub, "ns1."+sub)
	f.keepOnDelete = true
	if _, err := c.DeleteUnder(context.Background(), sub); err == nil || !strings.Contains(err.Error(), "still there") {
		t.Fatalf("surviving records: %v", err)
	}
}

func TestDeleteUnder_reportsDeleteFailure(t *testing.T) {
	f, c := newFakeCF(t)
	f.add("NS", sub, "ns1."+sub)
	f.failDelete = true
	if _, err := c.DeleteUnder(context.Background(), sub); err == nil {
		t.Fatal("a failed delete was swallowed")
	}
}

func TestListUnder_paginates(t *testing.T) {
	f, c := newFakeCF(t)
	for i := 0; i < perPage+5; i++ {
		f.add("TXT", "t.ns1."+sub, "x")
	}
	got, err := c.ListUnder(context.Background(), sub)
	if err != nil || len(got) != perPage+5 {
		t.Fatalf("ListUnder: %v %d", err, len(got))
	}
}

func TestRunRecordsAndDeleteRecords(t *testing.T) {
	f, c := newFakeCF(t)
	f.add("NS", sub, "ns1."+sub)
	f.add("A", "www.dbrsteting.bid", "203.0.113.9")
	runs, err := c.RunRecords(context.Background())
	if err != nil || len(runs) != 1 {
		t.Fatalf("RunRecords: %v %+v", err, runs)
	}
	if _, err := c.DeleteRecords(context.Background(), []Record{{ID: "x", Type: "A", Name: "www.dbrsteting.bid"}}); err == nil {
		t.Fatal("DeleteRecords deleted a record outside a run subdomain")
	}
	if _, err := c.DeleteRecords(context.Background(), runs); err != nil || len(f.records) != 1 {
		t.Fatalf("DeleteRecords: %v, %d left", err, len(f.records))
	}
}

func TestRunIDOf(t *testing.T) {
	_, c := newFakeCF(t)
	cases := map[string]string{sub: "ab12cd", "ns2." + sub + ".": "ab12cd", "www.dbrsteting.bid": "", "e2e-x.dbrsteting.bid": ""}
	for name, want := range cases {
		got, ok := c.RunIDOf(name)
		if got != want || ok != (want != "") {
			t.Errorf("RunIDOf(%q) = %q %v, want %q", name, got, ok, want)
		}
	}
}

func TestZoneID_rejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Unauthorized"}]}`))
	}))
	defer srv.Close()
	c, _ := New("secret-token-value", AllowedZone, srv.URL)
	_, err := c.ZoneID(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DNS:Edit") || strings.Contains(err.Error(), "secret-token-value") {
		t.Fatalf("ZoneID with a rejected token: %v", err)
	}
}
