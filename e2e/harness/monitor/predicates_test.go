package monitor

import (
	"strings"
	"testing"
)

var testHosts = []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}

func healthyNode(host string, leader bool) *NodeReport {
	state := RaftFollower
	if leader {
		state = RaftLeader
	}
	r := &NodeReport{
		PublicIP: host, WGIP: host,
		RQLite:    &RQLite{Responsive: true, RaftState: state, LeaderAddr: "10.0.0.1:7001"},
		Gateway:   &Gateway{Responsive: true, HTTPStatus: 200},
		WireGuard: &WireGuard{InterfaceUp: true},
		DNS:       &DNS{CoreDNSActive: true, CaddyActive: true},
		Services:  &Services{Services: []Service{{Name: "orama-node", ActiveState: "active"}}},
	}
	for _, peer := range testHosts {
		if peer != host {
			r.WireGuard.Peers = append(r.WireGuard.Peers, WGPeer{PublicKey: "k-" + peer, AllowedIPs: peer + "/32"})
		}
	}
	return r
}

func healthy() *Report {
	r := &Report{Summary: Summary{RQLiteLeader: "10.0.0.1", RQLiteQuorum: StatusOK, WGMeshStatus: StatusOK, ServiceHealth: StatusOK}}
	for i, host := range testHosts {
		r.Nodes = append(r.Nodes, Node{Host: host, Role: "nameserver", Status: StatusOK, Report: healthyNode(host, i == 0)})
	}
	return r
}

func TestConverged_healthyCluster(t *testing.T) {
	if err := healthy().Converged(3); err != nil {
		t.Fatal(err)
	}
}

func TestConverged_rejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Report)
		want   string
	}{
		"no quorum":      {func(r *Report) { r.Summary.RQLiteQuorum = "lost" }, "quorum"},
		"leader none":    {func(r *Report) { r.Summary.RQLiteLeader = NoLeader }, "no rqlite leader"},
		"broken wg mesh": {func(r *Report) { r.Summary.WGMeshStatus = "degraded" }, "wireguard mesh"},
		"critical alert": {func(r *Report) {
			r.Summary.CriticalAlerts = 1
			r.Alerts = []Alert{{Severity: AlertCritical, Subsystem: "rqlite", Node: "10.0.0.2", Message: "split brain"}}
		}, "split brain"},
		"candidate":       {func(r *Report) { r.Nodes[1].Report.RQLite.RaftState = "Candidate" }, "Candidate"},
		"rqlite silent":   {func(r *Report) { r.Nodes[1].Report.RQLite.Responsive = false }, "rqlite not responsive"},
		"no rqlite":       {func(r *Report) { r.Nodes[1].Report.RQLite = nil }, "no rqlite report"},
		"dead gateway":    {func(r *Report) { r.Nodes[1].Report.Gateway = &Gateway{HTTPStatus: 502} }, "gateway http 502"},
		"wg0 down":        {func(r *Report) { r.Nodes[2].Report.WireGuard.InterfaceUp = false }, "wg0 down"},
		"incomplete mesh": {func(r *Report) { r.Nodes[0].Report.WireGuard.Peers = r.Nodes[0].Report.WireGuard.Peers[:1] }, "1 wg peers, want 2"},
		"crash loop":      {func(r *Report) { r.Nodes[0].Report.Services.Services[0].RestartLoopRisk = true }, "crash-looping"},
		"failed unit":     {func(r *Report) { r.Nodes[1].Report.Services.FailedUnits = []string{"orama-turn"} }, "failed units"},
		"stale report":    {func(r *Report) { r.Nodes[1].ReportAgeSec = MaxReportAgeSec + 1 }, "report is 31s old"},
		"unreachable node": {func(r *Report) {
			r.Nodes[2].Status, r.Nodes[2].Error, r.Nodes[2].Report = "unreachable", "timeout", nil
		}, "no report (timeout)"},
		"node count": {func(r *Report) { r.Nodes = r.Nodes[:2] }, "want 3"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := healthy()
			c.mutate(r)
			if err := r.Converged(3); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
	if err := (&Report{}).Converged(0); err == nil {
		t.Fatal("an empty report converged")
	}
}

func TestLeaderAgreement_splitNoneAndSilent(t *testing.T) {
	if err := healthy().LeaderAgreement(); err != nil {
		t.Fatal(err)
	}
	split := healthy()
	split.Nodes[2].Report.RQLite.LeaderAddr = "10.0.0.3:7001"
	if err := split.LeaderAgreement(); err == nil || !strings.Contains(err.Error(), "split brain") {
		t.Fatalf("split: %v", err)
	}
	none := healthy()
	for i := range none.Nodes {
		none.Nodes[i].Report.RQLite.LeaderAddr = ""
	}
	if none.LeaderAgreement() == nil {
		t.Fatal("no leader passed")
	}
	silent := healthy()
	silent.Nodes[1].Report = nil
	silent.Nodes[2].Report.RQLite = nil
	if err := silent.LeaderAgreement(); err != nil {
		t.Fatalf("silent nodes broke agreement: %v", err)
	}
}

func TestForgotten_membershipViews(t *testing.T) {
	if healthy().Forgotten("10.0.0.2") == nil {
		t.Fatal("a listed node was forgotten")
	}
	peer := healthy()
	peer.Nodes = peer.Nodes[:2]
	if err := peer.Forgotten("10.0.0.3"); err == nil || !strings.Contains(err.Error(), "wireguard peer") {
		t.Fatalf("a mesh peer was forgotten: %v", err)
	}
	gone := healthy()
	gone.Nodes = gone.Nodes[1:]
	for i := range gone.Nodes {
		gone.Nodes[i].Report.WireGuard.Peers = []WGPeer{{AllowedIPs: "10.0.0.10/32"}}
	}
	if err := gone.Forgotten("10.0.0.1"); err != nil {
		t.Fatalf("10.0.0.10 taken for 10.0.0.1: %v", err)
	}
}

func TestAllowsIP_listsAndPrefixes(t *testing.T) {
	for allowed, want := range map[string]bool{"10.0.0.3/32": true, "10.0.0.3": true, "10.0.0.9/32, 10.0.0.3/32": true, "10.0.0.30/32": false, "": false} {
		if got := allowsIP(allowed, "10.0.0.3"); got != want {
			t.Errorf("allowsIP(%q) = %v", allowed, got)
		}
	}
}

func TestServing_independentOfRaft(t *testing.T) {
	r := healthy()
	r.Summary.RQLiteLeader, r.Summary.RQLiteQuorum = NoLeader, "lost"
	if err := r.Serving(); err != nil {
		t.Fatalf("mid-election cluster not serving: %v", err)
	}
	noDNS := healthy()
	noDNS.Nodes[0].Report.DNS.CoreDNSActive = false
	if err := noDNS.Serving(); err == nil || !strings.Contains(err.Error(), "coredns") {
		t.Fatalf("coredns down passed: %v", err)
	}
	worker := healthy()
	worker.Nodes[0].Role, worker.Nodes[0].Report.DNS = "node", nil
	if err := worker.Serving(); err != nil {
		t.Fatalf("a worker without CoreDNS: %v", err)
	}
	unreachable := healthy()
	unreachable.Nodes[2].Report = nil
	if err := unreachable.Serving(); err == nil || !strings.Contains(err.Error(), "10.0.0.3: gateway") {
		t.Fatalf("no report passed: %v", err)
	}
}

func TestParse_garbage(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("garbage parsed")
	}
}
