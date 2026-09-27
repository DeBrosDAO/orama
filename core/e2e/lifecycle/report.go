package lifecycle

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Values `orama monitor report` writes, pinned against the monitor's own
// constants by cmd/orama/internal/monitor/display/report_contract_test.go.
const (
	// NoLeader is summary.rqlite_leader when no node is the leader.
	NoLeader = "none"
	// StatusOK is a node's status, and a summary field's value, when all is well.
	StatusOK = "ok"
	// RaftLeader and RaftFollower are the settled raft states.
	RaftLeader   = report.RaftLeader
	RaftFollower = report.RaftFollower
	// nameserverRolePrefix starts the role of a node that runs a nameserver.
	nameserverRolePrefix = "nameserver"
)

// MaxReportAgeSec is the oldest a node's report may be for the cluster to
// count as converged. The gateway gathers each node's telemetry every 10s and
// caches a snapshot for 5s, so a live node's report is well inside this; an
// older one describes the cluster as it was, not as it is.
const MaxReportAgeSec = 30

// Report is `orama monitor report --json` as this harness reads it.
//
// Each node's report and the alerts decode into the real types from
// pkg/telemetry, the ones the report is written from. This used to be a hand
// copy of the report schema, and its field names drifted from the real ones
// (rqlite.leader, wireguard peers' allowed_ip and handshake_age_seconds,
// services' active / restarts / restart_loop): those fields silently decoded
// as zero, so LeaderAgreement could never pass, and a crash-looping service
// could never fail Converged. Only the envelope — meta and summary, which the
// monitor's display package builds — is declared here, and a test in that
// package decodes its real output through ParseReport so the two cannot drift
// apart again.
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

	Alerts []cluster.Alert `json:"alerts"`
	Nodes  []Node          `json:"nodes"`
}

// Node is one node's entry in the report. Report is nil when the node's
// report could not be collected; Error says why.
type Node struct {
	Host         string             `json:"host"`
	Role         string             `json:"role"`
	Status       string             `json:"status"`
	Error        string             `json:"error,omitempty"`
	ReportAgeSec int                `json:"report_age_sec"`
	Report       *report.NodeReport `json:"report,omitempty"`
}

// ParseReport decodes `orama monitor report --json` output.
func ParseReport(raw []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse monitor report: %w", err)
	}
	return &r, nil
}

// HasLeader reports whether the summary names an rqlite leader.
func (r *Report) HasLeader() bool {
	return r.Summary.RQLiteLeader != "" && r.Summary.RQLiteLeader != NoLeader
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
	if r.Summary.RQLiteQuorum != StatusOK {
		problems = append(problems, fmt.Sprintf("rqlite quorum is %q", r.Summary.RQLiteQuorum))
	}
	if !r.HasLeader() {
		problems = append(problems, "no rqlite leader")
	}
	if r.Summary.WGMeshStatus != StatusOK {
		problems = append(problems, fmt.Sprintf("wireguard mesh is %q", r.Summary.WGMeshStatus))
	}
	if r.Summary.CriticalAlerts > 0 {
		problems = append(problems, fmt.Sprintf("%d critical alert(s): %s",
			r.Summary.CriticalAlerts, strings.Join(r.criticalMessages(), "; ")))
	}

	peers := len(r.Nodes) - 1
	for _, n := range r.Nodes {
		problems = append(problems, n.problems(peers)...)
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("cluster has not converged: %s", strings.Join(problems, "; "))
}

// problems collects the reasons one node is not converged. peers is the N-1
// WireGuard peers a complete mesh gives every node.
func (n Node) problems(peers int) []string {
	var p []string
	if n.Status != StatusOK {
		p = append(p, fmt.Sprintf("%s: status %q", n.Host, n.Status))
	}
	if n.ReportAgeSec > MaxReportAgeSec {
		p = append(p, fmt.Sprintf("%s: report is %ds old", n.Host, n.ReportAgeSec))
	}
	if n.Report == nil {
		return append(p, fmt.Sprintf("%s: no report (%s)", n.Host, n.Error))
	}
	p = append(p, rqliteProblems(n.Host, n.Report.RQLite)...)
	if g := n.Report.Gateway; g == nil || !g.Responsive || g.HTTPStatus != http.StatusOK {
		p = append(p, fmt.Sprintf("%s: gateway http %d", n.Host, gatewayStatus(g)))
	}
	wg := n.Report.WireGuard
	switch {
	case wg == nil || !wg.InterfaceUp:
		p = append(p, fmt.Sprintf("%s: wg0 down", n.Host))
	case len(wg.Peers) != peers:
		// N-1 peers, or the mesh is not complete and some pair of nodes
		// cannot reach each other over the overlay.
		p = append(p, fmt.Sprintf("%s: %d wg peers, want %d", n.Host, len(wg.Peers), peers))
	}
	return append(p, serviceProblems(n.Host, n.Report.Services)...)
}

func rqliteProblems(host string, q *report.RQLiteReport) []string {
	if q == nil {
		return []string{fmt.Sprintf("%s: no rqlite report", host)}
	}
	var p []string
	if !q.Responsive {
		p = append(p, fmt.Sprintf("%s: rqlite not responsive", host))
	}
	switch q.RaftState {
	case RaftLeader, RaftFollower:
	default:
		p = append(p, fmt.Sprintf("%s: raft state %q", host, q.RaftState))
	}
	return p
}

func serviceProblems(host string, s *report.ServicesReport) []string {
	if s == nil {
		return nil
	}
	var p []string
	for _, svc := range s.Services {
		if svc.RestartLoopRisk {
			p = append(p, fmt.Sprintf("%s: %s is crash-looping (%d restarts)", host, svc.Name, svc.NRestarts))
		}
	}
	if len(s.FailedUnits) > 0 {
		p = append(p, fmt.Sprintf("%s: failed units %v", host, s.FailedUnits))
	}
	return p
}

func gatewayStatus(g *report.GatewayReport) int {
	if g == nil {
		return 0
	}
	return g.HTTPStatus
}

func (r *Report) criticalMessages() []string {
	var msgs []string
	for _, a := range r.Alerts {
		if a.Severity == cluster.AlertCritical {
			msgs = append(msgs, fmt.Sprintf("%s/%s: %s", a.Node, a.Subsystem, a.Message))
		}
	}
	return msgs
}

// rqlite is a node's rqlite report, nil when there is none.
func (n Node) rqlite() *report.RQLiteReport {
	if n.Report == nil {
		return nil
	}
	return n.Report.RQLite
}

// LeaderAgreement reports whether every responsive node names the same leader,
// by the leader's raft address.
//
// Separate from Converged because split brain is worth failing on by name: two
// nodes each leading their own half both look healthy from inside.
func (r *Report) LeaderAgreement() error {
	seen := map[string][]string{}
	for _, n := range r.Nodes {
		q := n.rqlite()
		if q == nil || !q.Responsive || q.LeaderAddr == "" {
			continue
		}
		seen[q.LeaderAddr] = append(seen[q.LeaderAddr], n.Host)
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
