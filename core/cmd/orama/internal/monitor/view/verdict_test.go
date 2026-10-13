package view

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func healthyStatus(host string, leader bool) cluster.CollectionStatus {
	state := "Follower"
	if leader {
		state = "Leader"
	}
	return cluster.CollectionStatus{
		Node: cluster.NodeRef{Host: host, Role: "node"},
		Report: &report.NodeReport{
			PublicIP:  host,
			Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: 200},
			RQLite:    &report.RQLiteReport{Responsive: true, RaftState: state},
			Olric:     &report.OlricReport{ServiceActive: true, MemberlistUp: true},
			IPFS:      &report.IPFSReport{DaemonActive: true, ClusterActive: true},
			Vault:     &report.VaultReport{ServiceActive: true, Responsive: true},
			WireGuard: &report.WireGuardReport{InterfaceUp: true},
		},
	}
}

func healthySnapshot() *cluster.ClusterSnapshot {
	return &cluster.ClusterSnapshot{Environment: "devnet", Nodes: []cluster.CollectionStatus{
		healthyStatus("1.1.1.1", true), healthyStatus("2.2.2.2", false), healthyStatus("3.3.3.3", false),
	}}
}

func TestVerdictLine_operational(t *testing.T) {
	_, v := Verdict(healthySnapshot())
	got := VerdictLine(NewTheme(false), v, 2*time.Second, false)
	if got != "✓ All systems operational · 3/3 nodes · updated 2s ago" {
		t.Fatalf("got %q", got)
	}
}

func TestVerdictLine_degraded(t *testing.T) {
	snap := healthySnapshot()
	snap.Nodes[2].Report.RQLite.RaftState = "Candidate"
	snap.Alerts = []cluster.Alert{
		{Severity: cluster.AlertCritical}, {Severity: cluster.AlertCritical}, {Severity: cluster.AlertWarning},
	}
	_, v := Verdict(snap)
	got := VerdictLine(NewTheme(false), v, 4*time.Second, false)
	want := "✗ Degraded: Database (RQLite) · 2 critical, 1 warning · 3/3 nodes · updated 4s ago"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestVerdictLine_outage(t *testing.T) {
	snap := healthySnapshot()
	for i := range snap.Nodes {
		snap.Nodes[i].Report.Gateway.Responsive = false
	}
	_, v := Verdict(snap)
	got := VerdictLine(NewTheme(false), v, time.Minute+5*time.Second, false)
	if !strings.HasPrefix(got, "✗ Outage: API Gateway") || !strings.HasSuffix(got, "updated 1m05s ago") {
		t.Fatalf("got %q", got)
	}
}

func TestVerdictLine_unknownAndStale(t *testing.T) {
	_, v := Verdict(&cluster.ClusterSnapshot{})
	got := VerdictLine(NewTheme(false), v, 90*time.Second, true)
	if !strings.HasPrefix(got, "? No nodes to report on") || !strings.Contains(got, "STALE: updated 1m30s ago") {
		t.Fatalf("got %q", got)
	}
}

func TestVerdictLine_plainThemeHasNoANSI(t *testing.T) {
	snap := healthySnapshot()
	snap.Nodes[0].Report.Gateway.Responsive = false
	_, v := Verdict(snap)
	if got := VerdictLine(NewTheme(false), v, 0, true); strings.Contains(got, "\x1b") {
		t.Fatalf("plain verdict carries ANSI: %q", got)
	}
}

func TestAlertCounts_onlyNonZero(t *testing.T) {
	cases := map[[2]int]string{{0, 0}: "", {1, 0}: "1 critical", {0, 1}: "1 warning", {2, 3}: "2 critical, 3 warnings"}
	for in, want := range cases {
		if got := AlertCounts(in[0], in[1]); got != want {
			t.Errorf("AlertCounts(%d, %d) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestFormatAge_unitsAndNegative(t *testing.T) {
	cases := map[time.Duration]string{
		-time.Second: "0s", 0: "0s", 59 * time.Second: "59s",
		61 * time.Second: "1m01s", 3*time.Hour + 5*time.Minute: "3h05m",
	}
	for d, want := range cases {
		if got := FormatAge(d); got != want {
			t.Errorf("FormatAge(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestSnapshotAge_nilAndZero(t *testing.T) {
	now := time.Now()
	if got := SnapshotAge(&cluster.ClusterSnapshot{CollectedAt: now.Add(-3 * time.Second)}, now); got != 3*time.Second {
		t.Errorf("got %s", got)
	}
	if got := SnapshotAge(nil, now); got != 0 {
		t.Errorf("nil snapshot: %s", got)
	}
	if got := SnapshotAge(&cluster.ClusterSnapshot{}, now); got != 0 {
		t.Errorf("zero time: %s", got)
	}
}
