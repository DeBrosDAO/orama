//go:build e2e_fleet

package bootstrap

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// verdictOperational is the verdict of a cluster with nothing degraded
// (core/pkg/telemetry/cluster/components.go StateOperational).
const verdictOperational = "operational"

// verdictView is the part of `orama monitor report --json` the harness type
// leaves out: the verdict and the per-component states.
type verdictView struct {
	Summary struct {
		Verdict struct {
			State        string `json:"state"`
			Headline     string `json:"headline"`
			Critical     int    `json:"critical"`
			NodesHealthy int    `json:"nodes_healthy"`
			NodesTotal   int    `json:"nodes_total"`
		} `json:"verdict"`
	} `json:"summary"`
	Components []struct {
		ID, State, Summary string
	} `json:"components"`
}

// TestBootstrap_clusterConverged: the fresh cluster, read the way the
// operator reads it (`orama monitor report`), has every core node, quorum,
// one leader every node agrees on, a full mesh and no critical alert
// (docs/MONITORING.md).
func TestBootstrap_clusterConverged(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the fresh cluster to converge")
	if r.Meta.Environment != f.State.Env {
		t.Errorf("the report is for environment %q, want %q", r.Meta.Environment, f.State.Env)
	}
	if r.Meta.NodeCount != len(f.State.Nodes) || r.Meta.HealthyCount != len(f.State.Nodes) || r.Meta.FailedCount != 0 {
		t.Errorf("meta says %d nodes, %d healthy, %d failed; want %d, %d, 0",
			r.Meta.NodeCount, r.Meta.HealthyCount, r.Meta.FailedCount, len(f.State.Nodes), len(f.State.Nodes))
	}
	_, subnet, err := net.ParseCIDR(infra.WireGuardSubnet)
	if err != nil {
		t.Fatal(err)
	}
	seenWG := map[string]string{}
	for _, n := range r.Nodes {
		node, err := infra.NodeByHost(f, n.Host)
		if err != nil {
			t.Errorf("the report lists %s, which is not a node of this run", n.Host)
			continue
		}
		wg := n.Report.WGIP
		if ip := net.ParseIP(wg); ip == nil || !subnet.Contains(ip) {
			t.Errorf("%s: WireGuard address %q is not in %s (docs/SECURITY.md CIDR validation)", node.Name, wg, infra.WireGuardSubnet)
		}
		if other, dup := seenWG[wg]; dup {
			t.Errorf("%s and %s share the WireGuard address %s", node.Name, other, wg)
		}
		seenWG[wg] = node.Name
		if node.WGIP != "" && node.WGIP != wg {
			t.Errorf("%s: the run recorded WG IP %s, the node reports %s", node.Name, node.WGIP, wg)
		}
	}
}

// TestBootstrap_verdictOperational: the verdict on top of the report is
// "operational" with no component degraded (docs/MONITORING.md: every view
// starts with the verdict).
func TestBootstrap_verdictOperational(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the fresh cluster to converge")
	res := harness.CLI(t).MustOK(t, "monitor", "report", "--env", f.State.Env, "--json")
	var v verdictView
	if err := json.Unmarshal([]byte(res.Stdout), &v); err != nil {
		t.Fatalf("monitor report is not JSON: %v", err)
	}
	vd := v.Summary.Verdict
	if vd.State != verdictOperational || vd.Critical != 0 {
		t.Errorf("verdict %q (%d critical): %s", vd.State, vd.Critical, vd.Headline)
	}
	if vd.NodesHealthy != len(f.State.Nodes) || vd.NodesTotal != len(f.State.Nodes) {
		t.Errorf("verdict counts %d of %d nodes healthy, want %d", vd.NodesHealthy, vd.NodesTotal, len(f.State.Nodes))
	}
	for _, c := range v.Components {
		if c.State != verdictOperational {
			t.Errorf("component %s is %s: %s", c.ID, c.State, c.Summary)
		}
	}
}

// TestBootstrap_everyNodeRunsTheCLIRelease: every node's gateway, as the
// report sees it, runs the release of the CLI under test.
func TestBootstrap_everyNodeRunsTheCLIRelease(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var v struct {
		Version string `json:"version"`
	}
	if err := harness.GW(t).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
		t.Fatal(err)
	}
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	for _, n := range r.Nodes {
		if n.Report == nil {
			t.Errorf("%s has no report: %s", n.Host, n.Error)
			continue
		}
		if n.Report.Version != v.Version {
			t.Errorf("%s runs %q, the public gateway says %q", n.Host, n.Report.Version, v.Version)
		}
		if g := n.Report.Gateway; g == nil || g.Version != v.Version {
			t.Errorf("%s: its gateway reports version %+v, want %s", n.Host, g, v.Version)
		}
	}
}

// statusEntry is one row of `orama status --json`.
type statusEntry struct {
	Host, Role, Status, Error string
}

// TestBootstrap_statusEveryNodeHealthy: `orama status` gives the same
// verdict as the monitor, from the same snapshot (docs/CLI_REFERENCE.md
// "orama status"): every core node healthy, with a role.
func TestBootstrap_statusEveryNodeHealthy(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the fresh cluster to converge")
	res := harness.CLI(t).MustOK(t, "status", "--env", f.State.Env, "--json")
	var rows []statusEntry
	if err := oramacli.DecodeJSON(res, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(f.State.Nodes) {
		t.Fatalf("orama status lists %d nodes, want %d: %s", len(rows), len(f.State.Nodes), res.Stdout)
	}
	for _, row := range rows {
		if _, err := infra.NodeByHost(f, row.Host); err != nil {
			t.Errorf("status lists %s: %v", row.Host, err)
		}
		if row.Status != infra.Healthy || row.Role == "" {
			t.Errorf("%s: status %q role %q (%s)", row.Host, row.Status, row.Role, row.Error)
		}
	}
	table := harness.CLI(t).MustOK(t, "status", "--env", f.State.Env).Stdout
	if !strings.Contains(table, "nodes healthy") {
		t.Errorf("the status table has no healthy count:\n%s", table)
	}
}

// TestBootstrap_monitorClusterRows: the one-shot cluster view has one OK row
// per node with the raft state settled and wg0 up.
func TestBootstrap_monitorClusterRows(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, "monitor", "cluster", "--env", f.State.Env, "--json")
	var rows []struct {
		Host   string `json:"host"`
		RQLite string `json:"rqlite_state"`
		WGUp   bool   `json:"wg_up"`
		Status string `json:"status"`
	}
	if err := oramacli.DecodeJSON(res, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(f.State.Nodes) {
		t.Fatalf("monitor cluster lists %d rows, want %d", len(rows), len(f.State.Nodes))
	}
	leaders := 0
	for _, row := range rows {
		if row.Status != monitor.StatusOK || !row.WGUp {
			t.Errorf("%s: status %q wg_up %v", row.Host, row.Status, row.WGUp)
		}
		switch row.RQLite {
		case monitor.RaftLeader:
			leaders++
		case monitor.RaftFollower:
		default:
			t.Errorf("%s: raft state %q", row.Host, row.RQLite)
		}
	}
	if leaders != 1 {
		t.Errorf("%d leaders in the cluster view, want 1", leaders)
	}
}
