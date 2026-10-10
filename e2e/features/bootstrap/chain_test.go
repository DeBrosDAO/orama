//go:build e2e_fleet

package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// blockBudget: the run's chain is deployed with short epochs; a few blocks
// land well inside a minute on a healthy chain.
const blockBudget = 2 * time.Minute

// chainView is `orama monitor chain --json`.
type chainView struct {
	ChainID    string `json:"chain_id"`
	Height     int64  `json:"height"`
	Validators []struct {
		Address     string `json:"address"`
		VotingPower int64  `json:"voting_power"`
	} `json:"validators"`
	Nodes []struct {
		Host string `json:"host"`
	} `json:"nodes"`
}

// TestBootstrap_chainValidatorsCoHosted: every core node runs the chain unit,
// is a validator of the run's chain and is in sync (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).
func TestBootstrap_chainValidatorsCoHosted(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if s := f.Unit(t, n, infra.ChainUnit); s != "active" {
			t.Errorf("%s: %s is %s", n.Name, infra.ChainUnit, s)
		}
	}
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	for _, n := range r.Nodes {
		c := n.Report.Chain
		switch {
		case c == nil:
			t.Errorf("%s reports no chain", n.Host)
		case !c.ServiceActive || !c.Responsive:
			t.Errorf("%s: chain active=%v responsive=%v: %s", n.Host, c.ServiceActive, c.Responsive, c.Error)
		case c.ChainID != f.State.ChainID:
			t.Errorf("%s runs chain %q, want %q", n.Host, c.ChainID, f.State.ChainID)
		case !c.IsValidator || c.VotingPower <= 0 || c.CatchingUp:
			t.Errorf("%s: validator=%v power=%d catching_up=%v", n.Host, c.IsValidator, c.VotingPower, c.CatchingUp)
		}
	}
}

// TestBootstrap_chainHeightAdvances: the chain's height, as the operator's
// chain view reports it, grows while we watch, and every node's view of it
// grows too (a stalled validator is invisible in one sample).
func TestBootstrap_chainHeightAdvances(t *testing.T) {
	t.Parallel()
	harness.RequireChain(t)
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	first := readChain(t, cli, f.State.Env)
	if first.ChainID != f.State.ChainID {
		t.Fatalf("monitor chain names %q, want %q", first.ChainID, f.State.ChainID)
	}
	if len(first.Validators) != len(f.State.Nodes) {
		t.Errorf("%d validators, want one per core node (%d)", len(first.Validators), len(f.State.Nodes))
	}
	start := map[string]int64{}
	for _, n := range monitor.Fetch(t, cli, f.State.Env).Nodes {
		start[n.Host] = n.Report.Chain.LatestHeight
	}
	eventually.Require(t, infra.PollEvery, blockBudget, "every node to see new blocks", func() (bool, error) {
		r, err := monitor.Get(t.Context(), cli.For(t), f.State.Env)
		if err != nil {
			return false, err
		}
		for _, n := range r.Nodes {
			if n.Report == nil || n.Report.Chain == nil || n.Report.Chain.LatestHeight <= start[n.Host] {
				return false, fmt.Errorf("%s is still at height %d", n.Host, start[n.Host])
			}
		}
		return true, nil
	})
	if last := readChain(t, cli, f.State.Env); last.Height <= first.Height {
		t.Errorf("chain height went from %d to %d", first.Height, last.Height)
	}
}

func readChain(t testing.TB, cli *oramacli.Runner, env string) chainView {
	t.Helper()
	var v chainView
	if err := oramacli.DecodeJSON(cli.MustOK(t, "status", "chain", "--env", env, "--json"), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestBootstrap_monitorRefusals: the monitor's refusals are the documented
// ones: no --env is a usage error, a caller with no credentials is an auth
// error (only operators read telemetry), a node not in the cluster is not
// found, and --config without --ssh is a usage error.
func TestBootstrap_monitorRefusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	infra.ExpectRefused(t, infra.Run(t, cli.Isolated(t), "status", "report", "--json"), "active environment")
	infra.ExpectExit(t, infra.Run(t, cli.Isolated(t), "status", "report", "--env", f.State.Env, "--json"),
		infra.ExitAuth, "no usable credentials")
	infra.ExpectExit(t, infra.Run(t, cli, "status", "report", "--env", f.State.Env, "--node", "192.0.2.1", "--json"),
		infra.ExitNotFound, "192.0.2.1")
	infra.ExpectExit(t, infra.Run(t, cli, "status", "report", "--env", f.State.Env, "--config", "/nonexistent"),
		infra.ExitUsage, "--ssh")
	infra.ExpectRefused(t, infra.Run(t, cli, "status", "report", "--env", "e2e-no-such-environment", "--json"),
		"not found")
}

// TestBootstrap_monitorOneNode: --node narrows the report to that node, by
// public or WireGuard address.
func TestBootstrap_monitorOneNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	full := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	n := f.State.Nodes[len(f.State.Nodes)-1]
	entry, err := infra.ReportFor(full, n)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{n.PublicIP, entry.Report.WGIP} {
		var r monitor.Report
		res := harness.CLI(t).MustOK(t, "status", "report", "--env", f.State.Env, "--node", key, "--json")
		if err := oramacli.DecodeJSON(res, &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Nodes) != 1 || r.Nodes[0].Report == nil || r.Nodes[0].Report.WGIP != entry.Report.WGIP {
			t.Errorf("--node %s gave %d nodes, want only %s", key, len(r.Nodes), n.Name)
		}
	}
}
