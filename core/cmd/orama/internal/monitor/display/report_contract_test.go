package display

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/e2e/lifecycle"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

var contractHosts = []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}

func contractNode(i int, host string) cluster.CollectionStatus {
	wgIP := "10.0.0." + string(rune('1'+i))
	state := "Follower"
	if i == 0 {
		state = "Leader"
	}
	r := &report.NodeReport{
		PublicIP: host, WGIP: wgIP,
		RQLite:    &report.RQLiteReport{Responsive: true, Ready: true, RaftState: state, LeaderAddr: "10.0.0.1:7001", LeaderID: "10.0.0.1:7001"},
		Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: 200},
		WireGuard: &report.WireGuardReport{InterfaceUp: true, WgIP: wgIP},
		DNS:       &report.DNSReport{CoreDNSActive: true, CaddyActive: true},
		Services:  &report.ServicesReport{Services: []report.ServiceInfo{{Name: "orama-node", ActiveState: "active"}}},
		Olric:     &report.OlricReport{ServiceActive: true, MemberlistUp: true},
		IPFS:      &report.IPFSReport{DaemonActive: true, ClusterActive: true},
		Vault:     &report.VaultReport{ServiceActive: true, Responsive: true},
	}
	for j := range contractHosts {
		if j != i {
			r.WireGuard.Peers = append(r.WireGuard.Peers, report.WGPeerInfo{
				AllowedIPs: "10.0.0." + string(rune('1'+j)) + "/32", LatestHandshake: 1, HandshakeAgeSec: 5})
		}
	}
	return cluster.CollectionStatus{Node: cluster.NodeRef{Host: host, Role: "nameserver-ns" + string(rune('1'+i))}, Report: r}
}

func contractSnapshot() *cluster.ClusterSnapshot {
	snap := &cluster.ClusterSnapshot{Environment: "devnet", CollectedAt: time.Now()}
	for i, h := range contractHosts {
		snap.Nodes = append(snap.Nodes, contractNode(i, h))
	}
	return snap
}

func fullReportThroughHarness(t *testing.T, snap *cluster.ClusterSnapshot) *lifecycle.Report {
	t.Helper()
	var buf bytes.Buffer
	if err := FullReport(snap, &buf); err != nil {
		t.Fatal(err)
	}
	r, err := lifecycle.ParseReport(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The lifecycle harness reads `orama status report --json` and nothing else.
// This runs the real report through the harness's decoder, so renaming a field
// either side reads (bug 2701) fails here instead of in a live scenario.
func TestFullReport_contractWithTheLifecycleHarness(t *testing.T) {
	r := fullReportThroughHarness(t, contractSnapshot())

	if r.Meta.Environment != "devnet" || r.Meta.NodeCount != 3 || r.Meta.HealthyCount != 3 {
		t.Fatalf("meta did not decode: %+v", r.Meta)
	}
	if r.Summary.RQLiteLeader != "1.1.1.1" || r.Summary.RQLiteQuorum != "ok" ||
		r.Summary.WGMeshStatus != "ok" || r.Summary.ServiceHealth != "ok" {
		t.Fatalf("summary did not decode: %+v", r.Summary)
	}
	if err := r.Converged(3); err != nil {
		t.Fatalf("a healthy cluster fails the harness: %v", err)
	}
	if err := r.LeaderAgreement(); err != nil {
		t.Fatalf("leader agreement: %v", err)
	}
	if err := r.Serving(); err != nil {
		t.Fatalf("serving: %v", err)
	}
	if err := r.Forgotten("10.0.0.3"); err == nil {
		t.Fatal("a node in the report was reported forgotten: the WireGuard fields did not decode")
	}
}

// The failure modes the harness exists to catch have to reach it through the
// real report too.
func TestFullReport_contractCarriesFailures(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[1].Report.Services.Services[0].RestartLoopRisk = true
	snap.Nodes[1].Report.Services.Services[0].NRestarts = 11
	snap.Nodes[2].Report.RQLite.LeaderAddr = "10.0.0.3:7001"
	r := fullReportThroughHarness(t, snap)

	if err := r.Converged(3); err == nil || !strings.Contains(err.Error(), "crash-looping (11 restarts)") {
		t.Fatalf("a crash loop did not reach the harness: %v", err)
	}
	if err := r.LeaderAgreement(); err == nil || !strings.Contains(err.Error(), "split brain") {
		t.Fatalf("split brain did not reach the harness: %v", err)
	}

	noLeader := contractSnapshot()
	noLeader.Nodes[0].Report.RQLite.RaftState = "Candidate"
	if r := fullReportThroughHarness(t, noLeader); r.HasLeader() {
		t.Fatalf("a cluster with no leader has one in the harness: %q", r.Summary.RQLiteLeader)
	}
}

// Fields are added, never renamed: scripts that read the old ones keep working.
func TestFullReport_keepsItsFieldsAndAddsTheVerdict(t *testing.T) {
	var buf bytes.Buffer
	if err := FullReport(contractSnapshot(), &buf); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"meta", "summary", "alerts", "nodes", "components"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("top-level %q missing", key)
		}
	}
	doc := map[string]map[string]any{}
	for _, key := range []string{"meta", "summary"} {
		var m map[string]any
		if err := json.Unmarshal(raw[key], &m); err != nil {
			t.Fatal(err)
		}
		doc[key] = m
	}
	for _, key := range []string{"environment", "collected_at", "duration_seconds", "node_count", "healthy_count", "failed_count"} {
		if _, ok := doc["meta"][key]; !ok {
			t.Errorf("meta.%s missing", key)
		}
	}
	for _, key := range []string{"rqlite_leader", "rqlite_quorum", "wg_mesh_status", "service_health", "critical_alerts", "warning_alerts", "verdict"} {
		if _, ok := doc["summary"][key]; !ok {
			t.Errorf("summary.%s missing", key)
		}
	}
	verdict, _ := doc["summary"]["verdict"].(map[string]any)
	if verdict["state"] != string(cluster.StateOperational) {
		t.Errorf("verdict = %v", verdict)
	}
}

func TestFullReport_emptySnapshot(t *testing.T) {
	r := fullReportThroughHarness(t, &cluster.ClusterSnapshot{})
	if r.HasLeader() || len(r.Nodes) != 0 || r.Summary.RQLiteQuorum != "unknown" {
		t.Fatalf("got %+v", r.Summary)
	}
}

// The harness names the values it compares against; they must be the ones
// the report is written with.
func TestFullReport_harnessConstantsMatch(t *testing.T) {
	pairs := map[string][2]string{
		"no leader":     {lifecycle.NoLeader, NoLeader},
		"node ok":       {lifecycle.StatusOK, NodeStatusOK},
		"summary ok":    {lifecycle.StatusOK, SummaryOK},
		"raft leader":   {lifecycle.RaftLeader, view.RaftLeader},
		"raft follower": {lifecycle.RaftFollower, view.RaftFollower},
	}
	for name, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("%s: harness %q, report %q", name, p[0], p[1])
		}
	}
}

// A report the gateway has not refreshed must not count as converged.
func TestFullReport_reportAgeReachesTheHarness(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[2].ReportAgeSec = lifecycle.MaxReportAgeSec + 10
	r := fullReportThroughHarness(t, snap)
	if r.Nodes[2].ReportAgeSec != lifecycle.MaxReportAgeSec+10 {
		t.Fatalf("report_age_sec = %d", r.Nodes[2].ReportAgeSec)
	}
	if err := r.Converged(3); err == nil || !strings.Contains(err.Error(), "report is 100s old") {
		t.Fatalf("a stale report counted as converged: %v", err)
	}
}
