package lifecycle

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// A node that stops answering is not the same as a node that has been
// forgotten. This is the assertion for the kill-a-voter scenario.
func TestForgotten_membershipViews(t *testing.T) {
	t.Run("still in the node list", func(t *testing.T) {
		if err := healthy().Forgotten("10.0.0.2"); err == nil {
			t.Fatal("a node still in the report was reported as forgotten")
		}
	})

	t.Run("evicted from raft but still a wireguard peer", func(t *testing.T) {
		r := healthy()
		r.Nodes = r.Nodes[:2] // 10.0.0.3 is gone from the node list...
		// ...but the survivors still carry it as a peer.
		err := r.Forgotten("10.0.0.3")
		if err == nil {
			t.Fatal("a node left in the WireGuard mesh was reported as forgotten")
		}
		if !strings.Contains(err.Error(), "wireguard peer") {
			t.Fatalf("error does not say where it survives: %v", err)
		}
	})

	t.Run("fully gone", func(t *testing.T) {
		r := healthy()
		r.Nodes = r.Nodes[:2]
		for i := range r.Nodes {
			var kept []report.WGPeerInfo
			for _, p := range r.Nodes[i].Report.WireGuard.Peers {
				if !strings.HasPrefix(p.AllowedIPs, "10.0.0.3") {
					kept = append(kept, p)
				}
			}
			r.Nodes[i].Report.WireGuard.Peers = kept
		}
		if err := r.Forgotten("10.0.0.3"); err != nil {
			t.Fatalf("a fully evicted node was not reported as forgotten: %v", err)
		}
	})

	t.Run("a prefix of another address is not the node", func(t *testing.T) {
		r := healthy()
		r.Nodes = r.Nodes[1:] // 10.0.0.1 is gone...
		for i := range r.Nodes {
			// ...and the survivors' only peer is 10.0.0.10, which must not
			// be taken for it.
			r.Nodes[i].Report.WireGuard.Peers = []report.WGPeerInfo{{AllowedIPs: "10.0.0.10/32"}}
		}
		if err := r.Forgotten("10.0.0.1"); err != nil {
			t.Fatalf("10.0.0.10 was taken for 10.0.0.1: %v", err)
		}
	})
}

func TestAllowsIP_listsAndPrefixes(t *testing.T) {
	cases := map[string]bool{
		"10.0.0.3/32":              true,
		"10.0.0.3":                 true,
		"10.0.0.9/32, 10.0.0.3/32": true,
		"10.0.0.30/32":             false,
		"":                         false,
	}
	for allowed, want := range cases {
		if got := allowsIP(allowed, "10.0.0.3"); got != want {
			t.Errorf("allowsIP(%q) = %v, want %v", allowed, got, want)
		}
	}
}

// Serving must be independent of raft. A cluster mid-election should still be
// answering DNS and TLS; losing the zone because no leader has been chosen yet
// is a much worse failure than a slow election.
func TestServing_isIndependentOfRaft(t *testing.T) {
	r := healthy()
	r.Summary.RQLiteLeader = NoLeader
	r.Summary.RQLiteQuorum = "lost"
	for i := range r.Nodes {
		r.Nodes[i].Report.RQLite.RaftState = "Candidate"
	}

	if err := r.Serving(); err != nil {
		t.Fatalf("a cluster mid-election was reported as not serving: %v", err)
	}
	if err := r.Converged(3); err == nil {
		t.Fatal("the same cluster was also reported as converged; the two must differ")
	}
}

func TestServing_rejectsDeadSurfaces(t *testing.T) {
	dead := healthy()
	dead.Nodes[1].Report.Gateway.Responsive = false
	if err := dead.Serving(); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("a dead gateway passed Serving: %v", err)
	}

	noDNS := healthy()
	noDNS.Nodes[0].Report.DNS.CoreDNSActive = false
	if err := noDNS.Serving(); err == nil || !strings.Contains(err.Error(), "coredns") {
		t.Fatalf("a nameserver with CoreDNS down passed Serving: %v", err)
	}

	unreachable := healthy()
	unreachable.Nodes[2].Report = nil
	if err := unreachable.Serving(); err == nil || !strings.Contains(err.Error(), "10.0.0.3: gateway") {
		t.Fatalf("a node with no report passed Serving: %v", err)
	}

	// CoreDNS is only expected on nameservers; a worker without it is fine.
	worker := healthy()
	worker.Nodes[0].Role = "node"
	worker.Nodes[0].Report.DNS = nil
	if err := worker.Serving(); err != nil {
		t.Fatalf("a worker without CoreDNS was reported as not serving: %v", err)
	}
}

// The node reports inside the monitor report are marshalled from
// report.NodeReport. Decoding one marshalled from the real type is the
// contract test for bug 2701: every field a predicate reads must survive the
// trip, or a predicate would silently read zero.
func TestParseReport_decodesTheRealNodeReport(t *testing.T) {
	nodeJSON, err := json.Marshal(healthyNodeReport("10.0.0.1", true))
	if err != nil {
		t.Fatal(err)
	}
	crashing := healthyNodeReport("10.0.0.2", false)
	crashing.Services.Services[0].RestartLoopRisk = true
	crashing.Services.Services[0].NRestarts = 7
	crashingJSON, err := json.Marshal(crashing)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
	  "summary": {"rqlite_leader":"10.0.0.1","rqlite_quorum":"ok","wg_mesh_status":"ok"},
	  "alerts": [{"severity":"warning","subsystem":"dns","node":"10.0.0.2","message":"cert expires soon"}],
	  "nodes": [{"host":"10.0.0.1","role":"nameserver","status":"ok","report":` + string(nodeJSON) + `},
	            {"host":"10.0.0.2","role":"nameserver","status":"ok","report":` + string(crashingJSON) + `}]}`)

	r, err := ParseReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Nodes[0].Report
	if got.WGIP != "10.0.0.1" || got.RQLite.LeaderAddr != "10.0.0.1:7001" ||
		got.WireGuard.Peers[0].AllowedIPs != "10.0.0.2/32" || got.WireGuard.Peers[0].HandshakeAgeSec != 12 {
		t.Fatalf("fields the predicates read did not survive decoding: %+v", got)
	}
	if err := r.LeaderAgreement(); err != nil {
		t.Fatalf("leader addresses were lost in decoding: %v", err)
	}
	// Two nodes each carrying two peers from a three-node fixture: the mesh
	// check fails, but the crash loop must be among the reasons.
	err = r.Converged(2)
	if err == nil || !strings.Contains(err.Error(), "crash-looping (7 restarts)") {
		t.Fatalf("restart_loop_risk / n_restarts were lost in decoding: %v", err)
	}
	if len(r.Alerts) != 1 || r.Alerts[0].Severity != "warning" {
		t.Fatalf("alerts lost: %+v", r.Alerts)
	}
}

// An unparseable report must be an error, not an empty one — an empty Report
// would sail through Converged's node-count check on a 0-node expectation and
// report a dead cluster as fine.
func TestParseReport_garbage(t *testing.T) {
	if _, err := ParseReport([]byte("orama: command not found")); err == nil {
		t.Fatal("garbage parsed as a report")
	}
}
