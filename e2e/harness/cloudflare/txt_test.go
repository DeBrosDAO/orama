package cloudflare

import (
	"context"
	"strings"
	"testing"
)

const clusterSub = "e2e-ab12cd-evalx.dbrsteting.bid"

func TestClusterSubdomain_validAndRefused(t *testing.T) {
	_, c := newFakeCF(t)
	got, err := c.ClusterSubdomain("ab12cd", "evalx")
	if err != nil || got != clusterSub {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range [][2]string{{"AB", "x"}, {"ab12cd", ""}, {"ab12cd", "a-b"}, {"ab12cd", "1abc"}, {"ab12cd", strings.Repeat("a", 13)}} {
		if _, err := c.ClusterSubdomain(bad[0], bad[1]); err == nil {
			t.Errorf("ClusterSubdomain(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

func TestRunIDOf_clusterSubdomains(t *testing.T) {
	_, c := newFakeCF(t)
	cases := map[string]string{clusterSub: "ab12cd", "_orama-verify.app." + clusterSub: "ab12cd",
		"e2e-ab12cd-.dbrsteting.bid": "", "e2e-ab12cd-a-b.dbrsteting.bid": "", "e2e-ab-x.dbrsteting.bid": ""}
	for name, want := range cases {
		got, ok := c.RunIDOf(name)
		if got != want || ok != (want != "") {
			t.Errorf("RunIDOf(%q) = %q %v, want %q", name, got, ok, want)
		}
	}
}

func TestDelegate_clusterSubdomainAllowed(t *testing.T) {
	f, c := newFakeCF(t)
	if err := c.Delegate(context.Background(), clusterSub, []Nameserver{{"ns1", "203.0.113.9"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 2 {
		t.Fatalf("records %+v", f.records)
	}
	if err := c.Delegate(context.Background(), "x."+clusterSub, []Nameserver{{"ns1", "203.0.113.9"}}); err == nil {
		t.Fatal("a name under a cluster subdomain was delegated")
	}
}

func TestSetTXT_createsOnceAndDeletes(t *testing.T) {
	f, c := newFakeCF(t)
	ctx := context.Background()
	name := "_orama-verify.app." + clusterSub
	for range 2 {
		if err := c.SetTXT(ctx, name, "token-1"); err != nil {
			t.Fatal(err)
		}
	}
	f.add("TXT", name, `"token-2"`)
	recs, err := c.ListTXT(ctx, name)
	if err != nil || len(recs) != 2 {
		t.Fatalf("records %+v err %v", recs, err)
	}
	deleted, err := c.DeleteTXT(ctx, name, "token-2")
	if err != nil || len(deleted) != 1 || TXTValue(deleted[0].Content) != "token-2" {
		t.Fatalf("deleted %+v err %v", deleted, err)
	}
	if deleted, err = c.DeleteTXT(ctx, name, ""); err != nil || len(deleted) != 1 {
		t.Fatalf("delete all: %+v %v", deleted, err)
	}
	if len(f.records) != 0 {
		t.Fatalf("left %+v", f.records)
	}
}

func TestSetTXT_refusesOutsideRunsAndBadValues(t *testing.T) {
	f, c := newFakeCF(t)
	ctx := context.Background()
	for _, name := range []string{"dbrsteting.bid", "_orama-verify.www.dbrsteting.bid", "x.example.com", "e2e-x.dbrsteting.bid"} {
		if err := c.SetTXT(ctx, name, "v"); err == nil {
			t.Errorf("SetTXT(%q) allowed", name)
		}
		if _, err := c.DeleteTXT(ctx, name, ""); err == nil {
			t.Errorf("DeleteTXT(%q) allowed", name)
		}
	}
	for _, v := range []string{"", `a"b`, "a\nb", strings.Repeat("v", 256)} {
		if err := c.SetTXT(ctx, "x."+sub, v); err == nil {
			t.Errorf("value %q accepted", v)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused call reached the API: %v", f.calls)
	}
}

func TestTXTValue_quotes(t *testing.T) {
	for in, want := range map[string]string{`"a"`: "a", "a": "a", `"`: `"`, "": ""} {
		if got := TXTValue(in); got != want {
			t.Errorf("TXTValue(%q) = %q", in, got)
		}
	}
}
