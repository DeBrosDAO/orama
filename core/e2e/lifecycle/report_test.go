package lifecycle

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// The predicates in this package decide whether a scenario passed. They have to
// be right on their own terms, because the scenarios that use them cannot run
// without infrastructure — a Converged that returns nil too easily would turn
// the whole harness green while proving nothing.

var testHosts = []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}

// healthyNodeReport is one converged node's report, built from the real type.
func healthyNodeReport(host string, leader bool) *report.NodeReport {
	state := "Follower"
	if leader {
		state = "Leader"
	}
	r := &report.NodeReport{
		PublicIP:  host,
		WGIP:      host,
		RQLite:    &report.RQLiteReport{Responsive: true, RaftState: state, LeaderAddr: "10.0.0.1:7001", LeaderID: "node-1"},
		Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: 200},
		WireGuard: &report.WireGuardReport{InterfaceUp: true},
		DNS:       &report.DNSReport{CoreDNSActive: true, CaddyActive: true},
		Services:  &report.ServicesReport{Services: []report.ServiceInfo{{Name: "orama-node", ActiveState: "active"}}},
	}
	// N-1 peers.
	for _, peer := range testHosts {
		if peer != host {
			r.WireGuard.Peers = append(r.WireGuard.Peers, report.WGPeerInfo{
				PublicKey: "k-" + peer, AllowedIPs: peer + "/32", LatestHandshake: 1, HandshakeAgeSec: 12})
		}
	}
	return r
}

// healthy builds a converged three-node report.
func healthy() *Report {
	r := &Report{}
	r.Summary.RQLiteLeader = "10.0.0.1"
	r.Summary.RQLiteQuorum = "ok"
	r.Summary.WGMeshStatus = "ok"
	r.Summary.ServiceHealth = "ok"
	for i, host := range testHosts {
		r.Nodes = append(r.Nodes, Node{Host: host, Role: "nameserver", Status: "ok", Report: healthyNodeReport(host, i == 0)})
	}
	return r
}

func TestConverged_healthyCluster(t *testing.T) {
	if err := healthy().Converged(3); err != nil {
		t.Fatalf("a healthy cluster was reported as unconverged: %v", err)
	}
}

// convergedRejections are real failure modes that must not slip past.
var convergedRejections = map[string]struct {
	mutate func(*Report)
	want   string
}{
	"no quorum":      {func(r *Report) { r.Summary.RQLiteQuorum = "lost" }, "quorum"},
	"no leader":      {func(r *Report) { r.Summary.RQLiteLeader = "" }, "no rqlite leader"},
	"leader none":    {func(r *Report) { r.Summary.RQLiteLeader = NoLeader }, "no rqlite leader"},
	"broken wg mesh": {func(r *Report) { r.Summary.WGMeshStatus = "degraded" }, "wireguard mesh"},
	"a critical alert": {func(r *Report) {
		r.Summary.CriticalAlerts = 1
		r.Alerts = []cluster.Alert{{Severity: cluster.AlertCritical, Subsystem: "rqlite", Node: "10.0.0.2", Message: "split brain"}}
	}, "split brain"},
	"a node still Candidate": {func(r *Report) { r.Nodes[1].Report.RQLite.RaftState = "Candidate" }, "Candidate"},
	"rqlite unresponsive":    {func(r *Report) { r.Nodes[1].Report.RQLite.Responsive = false }, "rqlite not responsive"},
	"no rqlite section":      {func(r *Report) { r.Nodes[1].Report.RQLite = nil }, "no rqlite report"},
	"a dead gateway": {func(r *Report) {
		r.Nodes[1].Report.Gateway = &report.GatewayReport{Responsive: false, HTTPStatus: 502}
	}, "gateway http 502"},
	"no gateway section": {func(r *Report) { r.Nodes[1].Report.Gateway = nil }, "gateway http 0"},
	"wg0 down":           {func(r *Report) { r.Nodes[2].Report.WireGuard.InterfaceUp = false }, "wg0 down"},
	"no wg section":      {func(r *Report) { r.Nodes[2].Report.WireGuard = nil }, "wg0 down"},
	"an incomplete mesh": {func(r *Report) {
		r.Nodes[0].Report.WireGuard.Peers = r.Nodes[0].Report.WireGuard.Peers[:1]
	}, "1 wg peers, want 2"},
	"a crash-looping service": {func(r *Report) {
		r.Nodes[0].Report.Services.Services = append(r.Nodes[0].Report.Services.Services, report.ServiceInfo{
			Name: "orama-namespace-olric@index", ActiveState: "active", NRestarts: 9, RestartLoopRisk: true})
	}, "crash-looping (9 restarts)"},
	"a failed unit": {func(r *Report) {
		r.Nodes[1].Report.Services.FailedUnits = []string{"orama-namespace-turn@anchat"}
	}, "failed units"},
	"a node reporting not-ok": {func(r *Report) { r.Nodes[2].Status = "degraded" }, `status "degraded"`},
	"a stale report":          {func(r *Report) { r.Nodes[1].ReportAgeSec = MaxReportAgeSec + 1 }, "report is 31s old"},
	"an unreachable node": {func(r *Report) {
		r.Nodes[2].Status, r.Nodes[2].Error, r.Nodes[2].Report = "unreachable", "SSH failed", nil
	}, "no report (SSH failed)"},
}

func TestConverged_rejects(t *testing.T) {
	for name, tc := range convergedRejections {
		t.Run(name, func(t *testing.T) {
			r := healthy()
			tc.mutate(r)
			err := r.Converged(3)
			if err == nil {
				t.Fatalf("%s was reported as converged", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

// The node count is part of the assertion. A kill-a-voter scenario expects two
// nodes, and a report still showing three means the dead one has not been
// evicted — a success by any weaker predicate.
func TestConverged_nodeCountIsAsserted(t *testing.T) {
	r := healthy()
	if err := r.Converged(2); err == nil {
		t.Fatal("a 3-node report satisfied a 2-node expectation")
	} else if !strings.Contains(err.Error(), "want 2") {
		t.Fatalf("error does not explain the count: %v", err)
	}
}

func TestConverged_emptyReport(t *testing.T) {
	if err := (&Report{}).Converged(0); err == nil {
		t.Fatal("an empty report with no quorum and no leader passed")
	}
}

// Split brain is the failure both halves look healthy from inside, so it needs
// naming rather than inferring.
func TestLeaderAgreement_splitAndNone(t *testing.T) {
	if err := healthy().LeaderAgreement(); err != nil {
		t.Fatalf("agreeing nodes were reported as split: %v", err)
	}

	split := healthy()
	split.Nodes[2].Report.RQLite.LeaderAddr = "10.0.0.3:7001"
	err := split.LeaderAgreement()
	if err == nil {
		t.Fatal("split brain was not detected")
	}
	if !strings.Contains(err.Error(), "split brain") {
		t.Fatalf("error does not name it: %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.3:7001") || !strings.Contains(err.Error(), "10.0.0.1:7001") {
		t.Fatalf("error does not show who believes what: %v", err)
	}

	none := healthy()
	for i := range none.Nodes {
		none.Nodes[i].Report.RQLite.LeaderAddr = ""
	}
	if err := none.LeaderAgreement(); err == nil {
		t.Fatal("a cluster where nobody names a leader passed")
	}
}

// Nodes without a report or an rqlite section have no opinion on the leader;
// they must neither panic nor count as a second leader.
func TestLeaderAgreement_ignoresSilentNodes(t *testing.T) {
	r := healthy()
	r.Nodes[1].Report = nil
	r.Nodes[2].Report.RQLite = nil
	if err := r.LeaderAgreement(); err != nil {
		t.Fatalf("silent nodes broke agreement: %v", err)
	}
}

func TestHasLeader_noneAndEmpty(t *testing.T) {
	r := &Report{}
	for leader, want := range map[string]bool{"": false, NoLeader: false, "10.0.0.1": true} {
		r.Summary.RQLiteLeader = leader
		if got := r.HasLeader(); got != want {
			t.Errorf("HasLeader(%q) = %v, want %v", leader, got, want)
		}
	}
}
