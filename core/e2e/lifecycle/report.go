package lifecycle

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The subset of `orama monitor report --json` this harness asserts on.
//
// Deliberately partial. A full mirror of the report schema would break every
// time a collector gains a field, and the scenarios only care about what
// "converged" means: quorum, one agreed leader, a complete WireGuard mesh,
// services that are up and not restarting, and DNS answering.
type Report struct {
	Meta struct {
		Environment  string `json:"environment"`
		NodeCount    int    `json:"node_count"`
		HealthyCount int    `json:"healthy_count"`
		FailedCount  int    `json:"failed_count"`
	} `json:"meta"`

	Summary struct {
		RQLiteLeader   string `json:"rqlite_leader"`
		RQLiteQuorum   string `json:"rqlite_quorum"`
		WGMeshStatus   string `json:"wg_mesh_status"`
		ServiceHealth  string `json:"service_health"`
		CriticalAlerts int    `json:"critical_alerts"`
		WarningAlerts  int    `json:"warning_alerts"`
	} `json:"summary"`

	Alerts []Alert `json:"alerts"`
	Nodes  []Node  `json:"nodes"`
}

// Alert is one finding from the cross-node analysis.
type Alert struct {
	Severity  string `json:"severity"`
	Subsystem string `json:"subsystem"`
	Node      string `json:"node"`
	Message   string `json:"message"`
}

// Node is one node's slice of the report.
type Node struct {
	Host   string `json:"host"`
	Role   string `json:"role"`
	Status string `json:"status"`
	Report struct {
		WireGuardIP string `json:"wireguard_ip"`

		RQLite struct {
			Responsive   bool   `json:"responsive"`
			RaftState    string `json:"raft_state"`
			Leader       string `json:"leader"`
			Term         uint64 `json:"term"`
			AppliedIndex uint64 `json:"applied_index"`
			CommitIndex  uint64 `json:"commit_index"`
			StrongRead   bool   `json:"strong_read"`
		} `json:"rqlite"`

		Gateway struct {
			Responsive bool `json:"responsive"`
			HTTPStatus int  `json:"http_status"`
		} `json:"gateway"`

		WireGuard struct {
			InterfaceUp bool `json:"interface_up"`
			Peers       []struct {
				PublicKey    string  `json:"public_key"`
				AllowedIP    string  `json:"allowed_ip"`
				HandshakeAge float64 `json:"handshake_age_seconds"`
			} `json:"peers"`
		} `json:"wireguard"`

		DNS struct {
			CoreDNSActive bool `json:"coredns_active"`
			CaddyActive   bool `json:"caddy_active"`
		} `json:"dns"`

		Services struct {
			Services []struct {
				Name        string `json:"name"`
				Active      bool   `json:"active"`
				Restarts    int    `json:"restarts"`
				RestartLoop bool   `json:"restart_loop"`
			} `json:"services"`
			FailedUnits []string `json:"failed_units"`
		} `json:"services"`
	} `json:"report"`
}

// ParseReport decodes `orama monitor report --json` output.
func ParseReport(raw []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse monitor report: %w", err)
	}
	return &r, nil
}

// Converged reports whether the cluster has settled, and says why not.
//
// One predicate rather than a scattering of assertions, so every scenario means
// the same thing by "the cluster came back" — and so a scenario cannot pass by
// checking a weaker condition than its neighbour.
//
// expectNodes is how many nodes should be present: a kill-a-voter scenario
// expects one fewer than it started with, and a converged 3-node report is a
// failure there rather than a success.
func (r *Report) Converged(expectNodes int) error {
	var problems []string

	if got := len(r.Nodes); got != expectNodes {
		problems = append(problems, fmt.Sprintf("%d nodes in the report, want %d", got, expectNodes))
	}
	if r.Summary.RQLiteQuorum != "ok" {
		problems = append(problems, fmt.Sprintf("rqlite quorum is %q", r.Summary.RQLiteQuorum))
	}
	if r.Summary.RQLiteLeader == "" {
		problems = append(problems, "no rqlite leader")
	}
	if r.Summary.WGMeshStatus != "ok" {
		problems = append(problems, fmt.Sprintf("wireguard mesh is %q", r.Summary.WGMeshStatus))
	}
	if r.Summary.CriticalAlerts > 0 {
		problems = append(problems, fmt.Sprintf("%d critical alert(s): %s",
			r.Summary.CriticalAlerts, strings.Join(r.criticalMessages(), "; ")))
	}

	problems = append(problems, r.nodeProblems()...)

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("cluster has not converged: %s", strings.Join(problems, "; "))
}

// nodeProblems collects the per-node reasons a cluster is not converged.
func (r *Report) nodeProblems() []string {
	var problems []string
	peers := len(r.Nodes) - 1

	for _, n := range r.Nodes {
		if n.Status != "ok" {
			problems = append(problems, fmt.Sprintf("%s: status %q", n.Host, n.Status))
		}
		if !n.Report.RQLite.Responsive {
			problems = append(problems, fmt.Sprintf("%s: rqlite not responsive", n.Host))
		}
		switch n.Report.RQLite.RaftState {
		case "Leader", "Follower":
		default:
			problems = append(problems, fmt.Sprintf("%s: raft state %q", n.Host, n.Report.RQLite.RaftState))
		}
		if !n.Report.Gateway.Responsive || n.Report.Gateway.HTTPStatus != 200 {
			problems = append(problems, fmt.Sprintf("%s: gateway http %d", n.Host, n.Report.Gateway.HTTPStatus))
		}
		if !n.Report.WireGuard.InterfaceUp {
			problems = append(problems, fmt.Sprintf("%s: wg0 down", n.Host))
		}
		// N-1 peers, or the mesh is not complete and some pair of nodes cannot
		// reach each other over the overlay.
		if got := len(n.Report.WireGuard.Peers); got != peers {
			problems = append(problems, fmt.Sprintf("%s: %d wg peers, want %d", n.Host, got, peers))
		}
		for _, svc := range n.Report.Services.Services {
			if svc.RestartLoop {
				problems = append(problems, fmt.Sprintf("%s: %s is crash-looping (%d restarts)",
					n.Host, svc.Name, svc.Restarts))
			}
		}
		if len(n.Report.Services.FailedUnits) > 0 {
			problems = append(problems, fmt.Sprintf("%s: failed units %v", n.Host, n.Report.Services.FailedUnits))
		}
	}
	return problems
}

func (r *Report) criticalMessages() []string {
	var msgs []string
	for _, a := range r.Alerts {
		if a.Severity == "critical" {
			msgs = append(msgs, fmt.Sprintf("%s/%s: %s", a.Node, a.Subsystem, a.Message))
		}
	}
	return msgs
}

// LeaderAgreement reports whether every responsive node names the same leader.
//
// Separate from Converged because split brain is worth failing on by name: two
// nodes each leading their own half both look healthy from inside.
func (r *Report) LeaderAgreement() error {
	seen := map[string][]string{}
	for _, n := range r.Nodes {
		if !n.Report.RQLite.Responsive || n.Report.RQLite.Leader == "" {
			continue
		}
		seen[n.Report.RQLite.Leader] = append(seen[n.Report.RQLite.Leader], n.Host)
	}
	if len(seen) == 0 {
		return fmt.Errorf("no node names a leader")
	}
	if len(seen) > 1 {
		var parts []string
		for leader, hosts := range seen {
			parts = append(parts, fmt.Sprintf("%s believed by %v", leader, hosts))
		}
		sort.Strings(parts)
		return fmt.Errorf("split brain: %s", strings.Join(parts, "; "))
	}
	return nil
}

// Forgotten reports whether every membership view has dropped wgIP.
//
// The assertion for the kill-a-voter scenario: a dead node is not gone because
// it stopped answering, it is gone when no surviving node still lists it as a
// peer or a voter. A node that is evicted from raft but left in the WireGuard
// mesh is the shape of failure that survives a restart.
func (r *Report) Forgotten(wgIP string) error {
	var stillThere []string
	for _, n := range r.Nodes {
		if n.Report.WireGuardIP == wgIP {
			stillThere = append(stillThere, n.Host+": still in the node list")
			continue
		}
		for _, p := range n.Report.WireGuard.Peers {
			if strings.HasPrefix(p.AllowedIP, wgIP+"/") || p.AllowedIP == wgIP {
				stillThere = append(stillThere, n.Host+": still a wireguard peer")
			}
		}
	}
	if len(stillThere) == 0 {
		return nil
	}
	sort.Strings(stillThere)
	return fmt.Errorf("%s has not been forgotten: %s", wgIP, strings.Join(stillThere, "; "))
}

// Serving reports whether every node answers on the public surfaces, ignoring
// raft entirely.
//
// The reboot-everything scenario asserts this first: a cluster with no leader
// yet should still be serving DNS and TLS, and the two failures are worth
// telling apart.
func (r *Report) Serving() error {
	var down []string
	for _, n := range r.Nodes {
		if !n.Report.Gateway.Responsive {
			down = append(down, n.Host+": gateway")
		}
		if strings.HasPrefix(n.Role, "nameserver") && !n.Report.DNS.CoreDNSActive {
			down = append(down, n.Host+": coredns")
		}
	}
	if len(down) == 0 {
		return nil
	}
	sort.Strings(down)
	return fmt.Errorf("not serving: %s", strings.Join(down, "; "))
}
