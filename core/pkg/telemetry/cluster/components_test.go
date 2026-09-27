package cluster

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// healthyReport is a node on which every always-on service serves.
func healthyReport(raft string) *report.NodeReport {
	return &report.NodeReport{
		Gateway:   &report.GatewayReport{Responsive: true, HTTPStatus: http.StatusOK},
		RQLite:    &report.RQLiteReport{Responsive: true, RaftState: raft},
		Olric:     &report.OlricReport{ServiceActive: true, MemberlistUp: true},
		IPFS:      &report.IPFSReport{DaemonActive: true, ClusterActive: true},
		Vault:     &report.VaultReport{ServiceActive: true, Responsive: true},
		WireGuard: &report.WireGuardReport{InterfaceUp: true, Peers: []report.WGPeerInfo{{LatestHandshake: 1, HandshakeAgeSec: 30}}},
	}
}

func snapshotOf(nodes ...CollectionStatus) *ClusterSnapshot {
	return &ClusterSnapshot{Nodes: nodes}
}

func reported(host, role string, r *report.NodeReport) CollectionStatus {
	return CollectionStatus{Node: NodeRef{Host: host, Role: role}, Report: r}
}

func componentByID(t *testing.T, comps []Component, id string) Component {
	t.Helper()
	for _, c := range comps {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("component %q not in %+v", id, comps)
	return Component{}
}

func TestComponents_allHealthy(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport("Leader")), reported("b", "node", healthyReport("Follower")))
	for _, c := range Components(snap) {
		if c.State != StateOperational || c.Healthy != 2 || c.Total != 2 {
			t.Errorf("%s = %s %d/%d, want operational 2/2", c.ID, c.State, c.Healthy, c.Total)
		}
	}
}

// leaderWithMembers is a leader whose view of the raft membership is members.
func leaderWithMembers(members map[string]report.RQLiteNodeInfo) *report.NodeReport {
	r := healthyReport(report.RaftLeader)
	r.RQLite.Nodes = members
	return r
}

func TestComponents_databaseOutageIsRaftQuorum(t *testing.T) {
	voter := func(reachable bool) report.RQLiteNodeInfo {
		return report.RQLiteNodeInfo{Voter: true, Reachable: reachable}
	}
	cases := map[string]struct {
		members map[string]report.RQLiteNodeInfo
		want    State
	}{
		"one of three voters lost": {map[string]report.RQLiteNodeInfo{"a": voter(true), "b": voter(true), "c": voter(false)}, StateDegraded},
		"two of three voters lost": {map[string]report.RQLiteNodeInfo{"a": voter(true), "b": voter(false), "c": voter(false)}, StateOutage},
		"only non-voters lost": {map[string]report.RQLiteNodeInfo{
			"a": voter(true), "b": voter(true), "c": voter(true),
			"d": {Reachable: false}, "e": {Reachable: false}, "f": {Reachable: false},
		}, StateDegraded},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snap := snapshotOf(reported("a", "node", leaderWithMembers(tc.members)),
				CollectionStatus{Node: NodeRef{Host: "b"}, Err: "timeout"})
			if got := componentByID(t, Components(snap), "database").State; got != tc.want {
				t.Fatalf("database = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestComponents_databaseWithoutLeaderIsOutage(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport(report.RaftFollower)), reported("b", "node", healthyReport(report.RaftFollower)))
	if got := componentByID(t, Components(snap), "database").State; got != StateOutage {
		t.Fatalf("no leader = %s, want outage: nothing can commit a write", got)
	}
}

func TestComponents_unknownNodeCountsNeitherWay(t *testing.T) {
	snap := snapshotOf(reported("a", "node", healthyReport(report.RaftLeader)),
		CollectionStatus{Node: NodeRef{Host: "b"}, Unknown: true, Err: "runs an older release"})
	gw := componentByID(t, Components(snap), "gateway")
	if gw.State != StateOperational || gw.Total != 1 {
		t.Fatalf("gateway = %s %d/%d, want operational 1/1: an old-release node is not a down node", gw.State, gw.Healthy, gw.Total)
	}
	if h := snap.Nodes[1].Health(); h != HealthUnknown {
		t.Fatalf("health = %s, want unknown", h)
	}
}

func TestComponents_chainOutageOnlyWhenNoNodeSeesBlocks(t *testing.T) {
	fresh, stalled := healthyReport(report.RaftLeader), healthyReport(report.RaftFollower)
	fresh.Chain = &report.ChainReport{Responsive: true, BlockAgeSec: 3}
	stalled.Chain = &report.ChainReport{Responsive: true, BlockAgeSec: chainStallSec + 5}
	third := healthyReport(report.RaftFollower)
	third.Chain = &report.ChainReport{Responsive: false}
	snap := snapshotOf(reported("a", "node", fresh), reported("b", "node", stalled), reported("c", "node", third))
	if got := componentByID(t, Components(snap), "chain").State; got != StateDegraded {
		t.Fatalf("one node seeing fresh blocks = %s, want degraded", got)
	}
	snap.Nodes[0].Report.Chain.BlockAgeSec = chainStallSec + 5
	if got := componentByID(t, Components(snap), "chain").State; got != StateOutage {
		t.Fatalf("no node seeing blocks = %s, want outage", got)
	}
}

func TestComponents_unreachableNodeCountsAgainstServicesItRuns(t *testing.T) {
	snap := snapshotOf(
		reported("a", "nameserver-ns1", healthyReport("Leader")),
		CollectionStatus{Node: NodeRef{Host: "b", Role: "nameserver-ns2"}, Err: "timeout"},
		CollectionStatus{Node: NodeRef{Host: "c", Role: "node"}, Err: "timeout"},
	)
	snap.Nodes[0].Report.DNS = &report.DNSReport{CoreDNSActive: true, CaddyActive: true}
	comps := Components(snap)

	if gw := componentByID(t, comps, "gateway"); gw.Total != 3 || gw.Healthy != 1 {
		t.Errorf("gateway = %d/%d, want 1/3", gw.Healthy, gw.Total)
	}
	// DNS runs on nameservers: the silent nameserver counts, the silent
	// plain node does not.
	if dns := componentByID(t, comps, "dns"); dns.Total != 2 || dns.Healthy != 1 || dns.State != StateDegraded {
		t.Errorf("dns = %s %d/%d, want degraded 1/2", dns.State, dns.Healthy, dns.Total)
	}
}

func TestComponents_omitsServicesNoNodeRuns(t *testing.T) {
	comps := Components(snapshotOf(reported("a", "node", healthyReport("Leader"))))
	for _, c := range comps {
		if c.ID == "chain" || c.ID == "dns" {
			t.Errorf("component %s listed although no node runs it", c.ID)
		}
	}
}

func TestComponents_emptySnapshot(t *testing.T) {
	if comps := Components(&ClusterSnapshot{}); len(comps) != 0 {
		t.Fatalf("empty snapshot produced components %+v", comps)
	}
}

func TestProbeMesh_staleOrMissingHandshakeIsDown(t *testing.T) {
	r := healthyReport("Leader")
	r.WireGuard.Peers = append(r.WireGuard.Peers, report.WGPeerInfo{LatestHandshake: 0})
	if _, ok := probeMesh(r); ok {
		t.Error("a peer that never handshaked counted as up")
	}
	r.WireGuard.Peers = []report.WGPeerInfo{{LatestHandshake: 1, HandshakeAgeSec: wgHandshakeStaleSec + 1}}
	if _, ok := probeMesh(r); ok {
		t.Error("a stale handshake counted as up")
	}
}

func TestProbeChain_stalledOrSyncingIsDown(t *testing.T) {
	for name, c := range map[string]*report.ChainReport{
		"rpc down":    {Responsive: false},
		"catching up": {Responsive: true, CatchingUp: true},
		"stalled":     {Responsive: true, BlockAgeSec: chainStallSec + 1},
	} {
		if applies, ok := probeChain(&report.NodeReport{Chain: c}); !applies || ok {
			t.Errorf("%s: applies=%v healthy=%v, want applies and unhealthy", name, applies, ok)
		}
	}
	if applies, ok := probeChain(&report.NodeReport{Chain: &report.ChainReport{Responsive: true, BlockAgeSec: 3}}); !applies || !ok {
		t.Error("a synced chain with a fresh block counted as down")
	}
}

func TestWorse_ordersStates(t *testing.T) {
	if Worse(StateOperational, StateOutage) != StateOutage || Worse(StateDegraded, StateOperational) != StateDegraded {
		t.Fatal("Worse did not pick the worse state")
	}
}

func TestComponents_leaderOnOldReleaseIsNotOutage(t *testing.T) {
	follower := func() *report.NodeReport {
		r := healthyReport(report.RaftFollower)
		r.RQLite.LeaderAddr = "10.0.0.1:10101"
		return r
	}
	snap := snapshotOf(reported("b", "node", follower()), reported("c", "node", follower()),
		CollectionStatus{Node: NodeRef{Host: "a"}, Unknown: true, Err: "older release"})
	if got := componentByID(t, Components(snap), "database").State; got != StateOperational {
		t.Fatalf("leader upgraded last = %s, want operational: followers name a leader", got)
	}
}

func TestComponents_leaderReportMissingFollowersSeeLeaderIsNotOutage(t *testing.T) {
	follower := healthyReport(report.RaftFollower)
	follower.RQLite.LeaderID = "node-a"
	snap := snapshotOf(reported("b", "node", follower), CollectionStatus{Node: NodeRef{Host: "a"}, Err: "gateway down"})
	if got := componentByID(t, Components(snap), "database").State; got != StateDegraded {
		t.Fatalf("leader's report missing = %s, want degraded, not outage", got)
	}
}

func TestComponents_noLeaderAndANodeSilentIsNotCalledOutage(t *testing.T) {
	snap := snapshotOf(reported("b", "node", healthyReport(report.RaftFollower)), CollectionStatus{Node: NodeRef{Host: "a"}, Err: "timeout"})
	if got := componentByID(t, Components(snap), "database").State; got == StateOutage {
		t.Fatal("a missing report with no leader named was called an outage; it may be a leader that did not report")
	}
}
