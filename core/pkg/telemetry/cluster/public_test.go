package cluster

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestSummarize_headlines(t *testing.T) {
	healthy := snapshotOf(reported("a", "node", healthyReport("Leader")))
	v := Summarize(healthy, Components(healthy))
	if v.State != StateOperational || v.Headline != "All systems operational" {
		t.Errorf("healthy = %s %q", v.State, v.Headline)
	}

	healthy.Alerts = []Alert{{Severity: AlertWarning}}
	if v := Summarize(healthy, Components(healthy)); !strings.Contains(v.Headline, "1 warning") {
		t.Errorf("warning not in headline: %q", v.Headline)
	}

	down := snapshotOf(CollectionStatus{Node: NodeRef{Host: "a"}, Err: "timeout"})
	if v := Summarize(down, Components(down)); v.State != StateOutage || !strings.HasPrefix(v.Headline, "Outage: ") {
		t.Errorf("all nodes down = %s %q, want an outage headline", v.State, v.Headline)
	}
}

func TestSummarize_criticalAlertDegradesHealthyComponents(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport("Leader")))
	snap.Alerts = []Alert{{Severity: AlertCritical, Message: "UFW inactive"}}
	v := Summarize(snap, Components(snap))
	if v.State != StateDegraded || v.Critical != 1 || v.Headline != "Degraded: 1 critical problem" {
		t.Fatalf("got %+v", v)
	}
}

func TestSummarize_noNodes(t *testing.T) {
	if v := Summarize(&ClusterSnapshot{}, nil); v.State != StateUnknown {
		t.Fatalf("no nodes = %s, want unknown", v.State)
	}
}

// The public view is served to anyone. No node's address, hostname or error
// text may reach it.
func TestPublic_namesNoNode(t *testing.T) {
	r := healthyReport("Leader")
	r.Hostname = "athena-secret-host"
	r.PublicIP = "37.59.116.212"
	r.WGIP = "10.0.0.1"
	snap := snapshotOf(
		CollectionStatus{Node: NodeRef{Host: "37.59.116.212", WGIP: "10.0.0.1"}, Report: r},
		CollectionStatus{Node: NodeRef{Host: "57.128.226.141"}, Err: "dial tcp 10.0.0.3:10104: connection refused"},
	)
	snap.Alerts = DeriveAlerts(snap)
	body, err := json.Marshal(Public(snap, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"37.59.116.212", "57.128.226.141", "10.0.0.", "athena-secret-host", "connection refused", "10104"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("public status leaks %q: %s", secret, body)
		}
	}
}

func TestPublic_chainTakesMedianNodeAndSharesPower(t *testing.T) {
	behind := healthyReport("Follower")
	behind.Chain = &report.ChainReport{Responsive: true, LatestHeight: 90}
	median := healthyReport("Leader")
	median.Chain = &report.ChainReport{
		Responsive: true, ChainID: "orama-stagenet-1", LatestHeight: 100, TotalVotingPower: 400,
		Validators: []report.ChainValidator{{Address: "A", VotingPower: 100}, {Address: "B", VotingPower: 300}},
	}
	// A spoofed view claiming a far higher height must not be what is published.
	spoofed := healthyReport("Follower")
	spoofed.Chain = &report.ChainReport{Responsive: true, ChainID: "evil", LatestHeight: 1 << 40}
	p := Public(snapshotOf(reported("a", "node", behind), reported("b", "node", median), reported("c", "node", spoofed)), nil)
	if p.Chain == nil || p.Chain.Height != 100 || p.Chain.ChainID != "orama-stagenet-1" {
		t.Fatalf("chain = %+v, want the median node at height 100", p.Chain)
	}
	if v := p.Chain.Validators; len(v) != 2 || v[0].Address != "B" || v[0].SharePct != 75 {
		t.Fatalf("validators = %+v, want B first at 75%%", v)
	}
}

func TestPublic_noChainAndNoTrafficAreOmitted(t *testing.T) {
	p := Public(snapshotOf(reported("a", "node", healthyReport("Leader"))), nil)
	if p.Chain != nil || p.Traffic != nil {
		t.Fatalf("chain=%v traffic=%v, want both nil when no node reports them", p.Chain, p.Traffic)
	}
	for _, c := range p.Components {
		if c.UptimePct != nil || c.History == nil {
			t.Errorf("%s: uptime=%v history=%v, want nil uptime and an empty history", c.ID, c.UptimePct, c.History)
		}
	}
}

func TestPublic_trafficSumsRatesAndWeightsErrors(t *testing.T) {
	a, b := healthyReport("Leader"), healthyReport("Follower")
	a.Traffic = &report.TrafficReport{RPS: 10, Requests: 600, Errors5xx: 6, P95Ms: 40}
	b.Traffic = &report.TrafficReport{RPS: 5, Requests: 300, Errors5xx: 0, P95Ms: 90}
	p := Public(snapshotOf(reported("a", "node", a), reported("b", "node", b)), nil)
	if p.Traffic.RPS != 15 || p.Traffic.P95Ms != 90 {
		t.Errorf("traffic = %+v, want rps 15, p95 the worse node's 90", p.Traffic)
	}
	if want := 6.0 / 900; p.Traffic.ErrorRate != want {
		t.Errorf("error rate = %v, want %v", p.Traffic.ErrorRate, want)
	}
}

func TestPublic_uptimeAveragesHistory(t *testing.T) {
	// A full day at 100% and a quarter-day at 96%: weighted by minutes, 99%.
	history := UptimeHistory{"gateway": {{Date: "2026-09-26", UptimePct: 100, Minutes: 1080}, {Date: "2026-09-27", UptimePct: 96, Minutes: 360}}}
	snap := snapshotOf(reported("a", "node", healthyReport("Leader")))
	snap.CollectedAt = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, c := range Public(snap, history).Components {
		if c.ID == "gateway" && (c.UptimePct == nil || *c.UptimePct != 99) {
			t.Fatalf("gateway uptime = %v, want 99 (minute-weighted)", c.UptimePct)
		}
	}
}

func TestPublic_verdictIgnoresOperatorAlerts(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport("Leader")))
	snap.Alerts = []Alert{{Severity: AlertCritical, Message: "UFW inactive"}, {Severity: AlertWarning, Message: "TLS expires in 10 days"}}
	p := Public(snap, nil)
	if p.Overall != StateOperational || p.Headline != "All systems operational" {
		t.Fatalf("public verdict = %s %q, want operational: alerts are for operators", p.Overall, p.Headline)
	}
}

func TestPublic_unknownNodesAreCountedApart(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport(report.RaftLeader)),
		CollectionStatus{Node: NodeRef{Host: "b"}, Unknown: true})
	p := Public(snap, nil)
	if p.Nodes.Total != 1 || p.Nodes.Healthy != 1 || p.Nodes.Unknown != 1 {
		t.Fatalf("nodes = %+v, want 1/1 with 1 unknown", p.Nodes)
	}
}
