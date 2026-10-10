//go:build e2e_fleet

package monitoring

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// reservedAddr is an address no node has (TEST-NET-3, RFC 5737).
const reservedAddr = "203.0.113.250"

// TestMonitor_usageErrorsExitTwo: an --interval outside 2s-60s, --config
// without --ssh and an --interval under 15s with --ssh are refused as usage
// before anything is contacted (docs/MONITORING.md "Flags"; core/cmd/orama/
// internal/monitor/source.go ResolveInterval; clierr CodeUsage).
func TestMonitor_usageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cases := map[string][]string{
		"interval under 2s":      monitorArgs(t, "live", "--interval", "1s"),
		"interval over 60s":      monitorArgs(t, "live", "--interval", "61s"),
		"root live interval 0":   monitorArgs(t, "", "--interval", "0s"),
		"ssh interval under 15s": monitorArgs(t, "live", "--ssh", "--interval", "5s"),
		"config without ssh":     monitorArgs(t, "cluster", "--config", "/nonexistent/nodes.conf"),
		"unknown environment":    {"status", "cluster", "--env", "e2e-no-such-env"},
	}
	for what, args := range cases {
		if res := infra.Run(t, cli, args...); res.Exit != infra.ExitUsage {
			t.Errorf("%s: exit %d, want %d (usage)\n%s%s", what, res.Exit, infra.ExitUsage, res.Stdout, res.Stderr)
		}
	}
	if res := infra.Run(t, cli, "status", "cluster"); res.Exit == infra.ExitOK {
		t.Errorf("monitor without --env succeeded (the flag is required)")
	}
}

// TestMonitor_noCredentialsExitThree: a machine with the environment but no
// session is an auth error, and says how to sign in (docs/MONITORING.md: "A
// missing credential or an ended session is an auth error (3)").
func TestMonitor_noCredentialsExitThree(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).Isolated(t)
	for _, view := range []string{"cluster", "report", "alerts"} {
		infra.ExpectExit(t, infra.Run(t, cli, monitorArgs(t, view)...), infra.ExitAuth, "orama auth login")
	}
	infra.ExpectExit(t, infra.Run(t, cli, "status", "--env", harness.Fleet(t).State.Env), infra.ExitAuth)
}

// TestMonitor_unknownNodeExitFour: --node naming a host not in the snapshot
// is not found and lists the nodes that are (docs/MONITORING.md "--node").
func TestMonitor_unknownNodeExitFour(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := infra.Run(t, harness.CLI(t), monitorArgs(t, "cluster", "--node", reservedAddr)...)
	infra.ExpectExit(t, res, infra.ExitNotFound, reservedAddr, f.State.Nodes[0].PublicIP)
}

// TestMonitor_nodeFilterByPublicAndOverlayAddress: --node narrows the report
// to one node, by its public or its WireGuard address.
func TestMonitor_nodeFilterByPublicAndOverlayAddress(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[1]
	for _, addr := range []string{n.PublicIP, n.WGIP} {
		res := harness.CLI(t).MustOK(t, monitorArgs(t, "report", "--node", addr)...)
		r, err := monitor.Parse([]byte(res.Stdout))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Nodes) != 1 || r.Nodes[0].Host != n.PublicIP {
			t.Errorf("--node %s: report lists %d nodes %+v, want only %s", addr, len(r.Nodes), r.Nodes, n.PublicIP)
		}
	}
	var alerts []struct{ Node string }
	monitorJSON(t, "alerts", &alerts, "--node", n.PublicIP)
	for _, a := range alerts {
		if a.Node != n.PublicIP && a.Node != "cluster" {
			t.Errorf("--node %s shows an alert about %s", n.PublicIP, a.Node)
		}
	}
}

// TestMonitor_reportIsTheDocumentedEnvelope: `report` is JSON without
// --json, names the environment, counts every node, and its summary carries
// the documented fields (docs/MONITORING.md "Report format": fields are only
// added, never renamed or removed; the harness's decoder is pinned by
// harness/monitor's drift test).
func TestMonitor_reportIsTheDocumentedEnvelope(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := harness.CLI(t).MustOK(t, monitorArgs(t, "report")...)
	r, err := monitor.Parse([]byte(res.Stdout))
	if err != nil {
		t.Fatalf("report without --json is not the JSON document: %v", err)
	}
	if r.Meta.Environment != f.State.Env || r.Meta.NodeCount != len(f.State.Nodes) || r.Meta.HealthyCount != len(f.State.Nodes) || r.Meta.FailedCount != 0 {
		t.Errorf("meta %+v, want env %s and %d healthy nodes", r.Meta, f.State.Env, len(f.State.Nodes))
	}
	var raw struct {
		Summary    map[string]any   `json:"summary"`
		Components []map[string]any `json:"components"`
	}
	if err := oramacli.DecodeJSON(res, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rqlite_leader", "rqlite_quorum", "wg_mesh_status", "service_health", "critical_alerts", "warning_alerts", "verdict"} {
		if _, ok := raw.Summary[k]; !ok {
			t.Errorf("summary lacks %q", k)
		}
	}
	if len(raw.Components) == 0 {
		t.Error("the report lists no components, so none of their fields is checked")
	}
	for _, c := range raw.Components {
		for _, k := range []string{"id", "name", "state", "healthy", "total", "summary"} {
			if _, ok := c[k]; !ok {
				t.Errorf("component %v lacks %q", c["id"], k)
			}
		}
	}
	if err := r.Converged(len(f.State.Nodes)); err != nil {
		t.Errorf("the healthy cluster does not read converged: %v", err)
	}
}
