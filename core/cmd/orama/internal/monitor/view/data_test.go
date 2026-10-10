package view

import (
	"math"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func trafficSnapshot() *cluster.ClusterSnapshot {
	snap := healthySnapshot()
	snap.Nodes[0].Report.Traffic = &report.TrafficReport{RPS: 10, Requests: 600, Errors5xx: 6, Errors4xx: 3, P50Ms: 5, P95Ms: 40, P99Ms: 90,
		Namespaces: []report.NamespaceTraffic{{Namespace: "anchat", Requests: 500, RPS: 8, P95Ms: 30}, {Namespace: "b", Requests: 100, RPS: 2}}}
	snap.Nodes[1].Report.Traffic = &report.TrafficReport{RPS: 30, Requests: 1800, Errors5xx: 0, P50Ms: 7, P95Ms: 20, P99Ms: 150,
		Namespaces: []report.NamespaceTraffic{{Namespace: "anchat", Requests: 1500, RPS: 25, Errors5xx: 1, P95Ms: 50}, {Namespace: "a", Requests: 100, RPS: 2}}}
	snap.Nodes = append(snap.Nodes, cluster.CollectionStatus{Node: cluster.NodeRef{Host: "4.4.4.4"}, Err: "timeout"})
	return snap
}

func TestAggregateTraffic_totals(t *testing.T) {
	s := AggregateTraffic(trafficSnapshot())
	tot := s.Totals
	if tot.Reporting != 2 || tot.RPS != 40 || tot.Requests != 2400 || tot.Errors4xx != 3 {
		t.Fatalf("totals = %+v", tot)
	}
	// Weighted by requests: 6 errors over 2400, not the mean of 1% and 0%.
	if math.Abs(tot.ErrorRate-6.0/2400) > 1e-9 {
		t.Fatalf("error rate = %v", tot.ErrorRate)
	}
	if tot.P50Ms != 7 || tot.P95Ms != 40 || tot.P99Ms != 150 {
		t.Fatalf("percentiles should be the worst node's: %+v", tot)
	}
	if len(s.Nodes) != 4 || s.Nodes[2].Traffic != nil || s.Nodes[3].Traffic != nil {
		t.Fatalf("nodes without traffic must stay listed with none: %+v", s.Nodes)
	}
}

func TestAggregateTraffic_mergesNamespaces(t *testing.T) {
	ns := AggregateTraffic(trafficSnapshot()).Namespaces
	var names []string
	for _, n := range ns {
		names = append(names, n.Namespace)
	}
	if strings.Join(names, ",") != "anchat,a,b" {
		t.Fatalf("order = %v, want busiest first then by name", names)
	}
	if ns[0].Requests != 2000 || ns[0].RPS != 33 || ns[0].Errors5xx != 1 || ns[0].P95Ms != 50 {
		t.Fatalf("anchat = %+v", ns[0])
	}
}

func TestAggregateTraffic_none(t *testing.T) {
	s := AggregateTraffic(healthySnapshot())
	if s.Totals.Reporting != 0 || s.Totals.ErrorRate != 0 || len(s.Namespaces) != 0 {
		t.Fatalf("got %+v", s)
	}
}

func TestTopNamespaces_limitsAndOrders(t *testing.T) {
	ns := map[string]*report.NamespaceTraffic{}
	for _, n := range []string{"a", "b", "c"} {
		ns[n] = &report.NamespaceTraffic{Namespace: n}
	}
	if got := topNamespaces(ns, 2); len(got) != 2 || got[0].Namespace != "a" {
		t.Fatalf("got %+v", got)
	}
}

func TestSeriesPush_keepsTheLastN(t *testing.T) {
	s := NewSeries(3)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		s.Push(v)
	}
	if got := s.Values(); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("got %v", got)
	}
}

func TestSparkline_scalesAndFlatBaseline(t *testing.T) {
	if got := Sparkline([]float64{0, 5, 10}); got != "▁▄█" {
		t.Errorf("got %q", got)
	}
	if got := Sparkline([]float64{0, 0}); got != "▁▁" {
		t.Errorf("all zero: %q", got)
	}
	if got := Sparkline(nil); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func chainSnapshot() *cluster.ClusterSnapshot {
	snap := healthySnapshot()
	vals := []report.ChainValidator{{Address: "AAA", VotingPower: 10}, {Address: "BBB", VotingPower: 30}}
	snap.Nodes[0].Report.Chain = &report.ChainReport{Responsive: true, ChainID: "orama-1", LatestHeight: 100, BlockAgeSec: 2,
		AvgBlockTimeSec: 5, MempoolTxs: 3, Validators: vals, TotalVotingPower: 40}
	snap.Nodes[1].Report.Chain = &report.ChainReport{Responsive: true, ChainID: "orama-1", LatestHeight: 90, CatchingUp: true}
	snap.Nodes[2].Report.Chain = &report.ChainReport{Responsive: false, LatestHeight: 500, Error: "connection refused"}
	return snap
}

func TestPrepareChain_usesTheFurthestAnsweringNode(t *testing.T) {
	s := PrepareChain(chainSnapshot())
	if !s.Present || s.ChainID != "orama-1" || s.Height != 100 || s.MempoolTxs != 3 || s.AvgBlockTimeSec != 5 {
		t.Fatalf("summary = %+v (an unresponsive node's height must not count)", s)
	}
	if len(s.Nodes) != 3 {
		t.Fatalf("nodes = %d, want every node that reported a chain", len(s.Nodes))
	}
	if s.Validators[0].Address != "BBB" || s.Validators[0].SharePct != 75 || s.Validators[1].SharePct != 25 {
		t.Fatalf("validators = %+v", s.Validators)
	}
}

func TestPrepareChain_noChainAndNoAnswer(t *testing.T) {
	if s := PrepareChain(healthySnapshot()); s.Present {
		t.Fatalf("a snapshot with no chain reports one: %+v", s)
	}
	snap := healthySnapshot()
	snap.Nodes[0].Report.Chain = &report.ChainReport{Responsive: false}
	if s := PrepareChain(snap); !s.Present || s.ChainID != "" || len(s.Nodes) != 1 {
		t.Fatalf("all RPCs down: %+v", s)
	}
}

func TestValidatorShares_zeroTotal(t *testing.T) {
	got := validatorShares([]report.ChainValidator{{Address: "A", VotingPower: 1}}, 0)
	if got[0].SharePct != 0 {
		t.Fatalf("share with no total power: %v", got[0].SharePct)
	}
}

func TestBar_clampsAndRounds(t *testing.T) {
	cases := []struct {
		pct   float64
		width int
		want  string
	}{
		{50, 4, "██░░"}, {0, 3, "░░░"}, {100, 3, "███"}, {150, 2, "██"}, {-5, 2, "░░"}, {50, 0, ""},
	}
	for _, tc := range cases {
		if got := Bar(tc.pct, tc.width); got != tc.want {
			t.Errorf("Bar(%v, %d) = %q, want %q", tc.pct, tc.width, got, tc.want)
		}
	}
}
