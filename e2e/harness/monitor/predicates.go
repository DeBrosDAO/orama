package monitor

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/oramaunit"
)

// HasLeader reports whether the summary names an rqlite leader.
func (r *Report) HasLeader() bool {
	return r.Summary.RQLiteLeader != "" && r.Summary.RQLiteLeader != NoLeader
}

// Converged reports whether the cluster has settled, and says why not.
// expectNodes is how many nodes should be present: after a node is removed a
// converged report with the old count is a failure, not a success.
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
		p = append(p, fmt.Sprintf("%s: %d wg peers, want %d", n.Host, len(wg.Peers), peers))
	}
	return append(p, serviceProblems(n.Host, n.Report.Services)...)
}

func rqliteProblems(host string, q *RQLite) []string {
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

func serviceProblems(host string, s *Services) []string {
	if s == nil {
		return nil
	}
	var p []string
	for _, svc := range s.Services {
		if svc.RestartLoopRisk {
			p = append(p, fmt.Sprintf("%s: %s is crash-looping (%d restarts)", host, svc.Name, svc.NRestarts))
		}
	}
	if failed := oramaunit.Filter(s.FailedUnits); len(failed) > 0 {
		p = append(p, fmt.Sprintf("%s: failed units %v", host, failed))
	}
	return p
}

func gatewayStatus(g *Gateway) int {
	if g == nil {
		return 0
	}
	return g.HTTPStatus
}

func (r *Report) criticalMessages() []string {
	var msgs []string
	for _, a := range r.Alerts {
		if a.Severity == AlertCritical {
			msgs = append(msgs, fmt.Sprintf("%s/%s: %s", a.Node, a.Subsystem, a.Message))
		}
	}
	return msgs
}

// LeaderAgreement reports whether every responsive node names the same
// leader, by its raft address. Split brain is failed by name: two nodes each
// leading their own half both look healthy from inside.
func (r *Report) LeaderAgreement() error {
	seen := map[string][]string{}
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.RQLite == nil {
			continue
		}
		q := n.Report.RQLite
		if !q.Responsive || q.LeaderAddr == "" {
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
