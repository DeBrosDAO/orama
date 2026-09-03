package lifecycle

import (
	"strings"
	"testing"
)

// The predicates in this package decide whether a scenario passed. They have to
// be right on their own terms, because the scenarios that use them cannot run
// without infrastructure — a Converged that returns nil too easily would turn
// the whole harness green while proving nothing.

// healthy builds a converged three-node report.
func healthy() *Report {
	r := &Report{}
	r.Summary.RQLiteLeader = "10.0.0.1"
	r.Summary.RQLiteQuorum = "ok"
	r.Summary.WGMeshStatus = "ok"
	r.Summary.ServiceHealth = "ok"

	for i, host := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		var n Node
		n.Host = host
		n.Role = "nameserver"
		n.Status = "ok"
		n.Report.WireGuardIP = host
		n.Report.RQLite.Responsive = true
		n.Report.RQLite.RaftState = "Follower"
		if i == 0 {
			n.Report.RQLite.RaftState = "Leader"
		}
		n.Report.RQLite.Leader = "10.0.0.1"
		n.Report.Gateway.Responsive = true
		n.Report.Gateway.HTTPStatus = 200
		n.Report.WireGuard.InterfaceUp = true
		n.Report.DNS.CoreDNSActive = true
		n.Report.DNS.CaddyActive = true

		// N-1 peers.
		for _, peer := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
			if peer == host {
				continue
			}
			n.Report.WireGuard.Peers = append(n.Report.WireGuard.Peers, struct {
				PublicKey    string  `json:"public_key"`
				AllowedIP    string  `json:"allowed_ip"`
				HandshakeAge float64 `json:"handshake_age_seconds"`
			}{PublicKey: "k-" + peer, AllowedIP: peer + "/32", HandshakeAge: 12})
		}
		r.Nodes = append(r.Nodes, n)
	}
	return r
}

func TestConverged_healthyCluster(t *testing.T) {
	if err := healthy().Converged(3); err != nil {
		t.Fatalf("a healthy cluster was reported as unconverged: %v", err)
	}
}

// Each of these is a real failure mode that must not slip past.
func TestConverged_rejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Report)
		want   string
	}{
		"no quorum": {
			func(r *Report) { r.Summary.RQLiteQuorum = "lost" }, "quorum",
		},
		"no leader": {
			func(r *Report) { r.Summary.RQLiteLeader = "" }, "no rqlite leader",
		},
		"broken wg mesh": {
			func(r *Report) { r.Summary.WGMeshStatus = "degraded" }, "wireguard mesh",
		},
		"a critical alert": {
			func(r *Report) {
				r.Summary.CriticalAlerts = 1
				r.Alerts = []Alert{{Severity: "critical", Subsystem: "rqlite", Node: "10.0.0.2", Message: "split brain"}}
			}, "split brain",
		},
		"a node still Candidate": {
			func(r *Report) { r.Nodes[1].Report.RQLite.RaftState = "Candidate" }, "Candidate",
		},
		"a dead gateway": {
			func(r *Report) {
				r.Nodes[1].Report.Gateway.Responsive = false
				r.Nodes[1].Report.Gateway.HTTPStatus = 502
			}, "gateway http 502",
		},
		"wg0 down": {
			func(r *Report) { r.Nodes[2].Report.WireGuard.InterfaceUp = false }, "wg0 down",
		},
		"an incomplete mesh": {
			func(r *Report) { r.Nodes[0].Report.WireGuard.Peers = r.Nodes[0].Report.WireGuard.Peers[:1] },
			"1 wg peers, want 2",
		},
		"a crash-looping service": {
			func(r *Report) {
				r.Nodes[0].Report.Services.Services = append(r.Nodes[0].Report.Services.Services, struct {
					Name        string `json:"name"`
					Active      bool   `json:"active"`
					Restarts    int    `json:"restarts"`
					RestartLoop bool   `json:"restart_loop"`
				}{Name: "orama-namespace-olric@index", Active: true, Restarts: 9, RestartLoop: true})
			}, "crash-looping",
		},
		"a failed unit": {
			func(r *Report) { r.Nodes[1].Report.Services.FailedUnits = []string{"orama-namespace-turn@anchat"} },
			"failed units",
		},
		"a node reporting not-ok": {
			func(r *Report) { r.Nodes[2].Status = "unreachable" }, `status "unreachable"`,
		},
	}

	for name, tc := range cases {
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

// Split brain is the failure both halves look healthy from inside, so it needs
// naming rather than inferring.
func TestLeaderAgreement(t *testing.T) {
	if err := healthy().LeaderAgreement(); err != nil {
		t.Fatalf("agreeing nodes were reported as split: %v", err)
	}

	split := healthy()
	split.Nodes[2].Report.RQLite.Leader = "10.0.0.3"
	err := split.LeaderAgreement()
	if err == nil {
		t.Fatal("split brain was not detected")
	}
	if !strings.Contains(err.Error(), "split brain") {
		t.Fatalf("error does not name it: %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.3") || !strings.Contains(err.Error(), "10.0.0.1") {
		t.Fatalf("error does not show who believes what: %v", err)
	}

	none := healthy()
	for i := range none.Nodes {
		none.Nodes[i].Report.RQLite.Leader = ""
	}
	if err := none.LeaderAgreement(); err == nil {
		t.Fatal("a cluster where nobody names a leader passed")
	}
}

// A node that stops answering is not the same as a node that has been
// forgotten. This is the assertion for the kill-a-voter scenario.
func TestForgotten(t *testing.T) {
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
			var kept []struct {
				PublicKey    string  `json:"public_key"`
				AllowedIP    string  `json:"allowed_ip"`
				HandshakeAge float64 `json:"handshake_age_seconds"`
			}
			for _, p := range r.Nodes[i].Report.WireGuard.Peers {
				if !strings.HasPrefix(p.AllowedIP, "10.0.0.3") {
					kept = append(kept, p)
				}
			}
			r.Nodes[i].Report.WireGuard.Peers = kept
		}
		if err := r.Forgotten("10.0.0.3"); err != nil {
			t.Fatalf("a fully evicted node was not reported as forgotten: %v", err)
		}
	})
}

// Serving must be independent of raft. A cluster mid-election should still be
// answering DNS and TLS; losing the zone because no leader has been chosen yet
// is a much worse failure than a slow election.
func TestServing_isIndependentOfRaft(t *testing.T) {
	r := healthy()
	r.Summary.RQLiteLeader = ""
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

	// CoreDNS is only expected on nameservers; a worker without it is fine.
	worker := healthy()
	worker.Nodes[0].Role = "node"
	worker.Nodes[0].Report.DNS.CoreDNSActive = false
	if err := worker.Serving(); err != nil {
		t.Fatalf("a worker without CoreDNS was reported as not serving: %v", err)
	}
}

func TestParseReport(t *testing.T) {
	raw := []byte(`{
	  "meta": {"environment":"devnet","node_count":3,"healthy_count":3,"failed_count":0},
	  "summary": {"rqlite_leader":"10.0.0.1","rqlite_quorum":"ok","wg_mesh_status":"ok","critical_alerts":0},
	  "alerts": [{"severity":"warning","subsystem":"dns","node":"10.0.0.2","message":"cert expires soon"}],
	  "nodes": [{"host":"1.2.3.4","role":"nameserver","status":"ok","report":{
	    "wireguard_ip":"10.0.0.1",
	    "rqlite":{"responsive":true,"raft_state":"Leader","applied_index":40,"commit_index":40},
	    "gateway":{"responsive":true,"http_status":200},
	    "wireguard":{"interface_up":true,"peers":[{"allowed_ip":"10.0.0.2/32"}]}
	  }}]
	}`)

	r, err := ParseReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary.RQLiteLeader != "10.0.0.1" || len(r.Nodes) != 1 {
		t.Fatalf("got %+v", r.Summary)
	}
	if r.Nodes[0].Report.RQLite.AppliedIndex != 40 || r.Nodes[0].Report.WireGuardIP != "10.0.0.1" {
		t.Fatalf("nested fields lost: %+v", r.Nodes[0].Report)
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
