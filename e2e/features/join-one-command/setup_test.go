//go:build e2e_fleet

package joinonecommand

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// TestSetup_oneNodeThenTwoMore is a newcomer's whole join. Each step needs the
// one before it, so a failed step stops the test.
func TestSetup_oneNodeThenTwoMore(t *testing.T) {
	f := harness.Fleet(t)
	harness.RequireChain(t)
	cli := isolatedCLI(t)
	net := network(t, cli)
	extras := []harness.Extra{
		harness.ExtraNode(t, "join-one-1", extraLocation),
		harness.ExtraNode(t, "join-one-2", extraLocation),
		harness.ExtraNode(t, "join-one-3", extraLocation),
	}
	env := net + "-" + nodeName
	fundOperator(t, f, cli)

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"oneFreshServerBecomesAFullNode", func(t *testing.T) { oneFreshServerBecomesAFullNode(t, f, cli, net, env, extras[0]) }},
		{"theSameCommandAgainChangesNothing", func(t *testing.T) { theSameCommandAgainChangesNothing(t, cli, net, env, extras[0]) }},
		{"twoMoreJoinTheSameCluster", func(t *testing.T) { twoMoreJoinTheSameCluster(t, f, cli, net, env, extras) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			t.Fatalf("step %s failed; the later steps depend on it", s.name)
		}
	}
}

// fundOperator gives the throwaway RootWallet's operator account the ORAMA the
// bonds and the validator's self-bond need, from the fleet's chain faucet, so
// the unattended run is not stopped by an unfunded account.
func fundOperator(t *testing.T, f *fleet.Fleet, cli *oramacli.Runner) {
	t.Helper()
	acct, err := rwagent.New(cli.AgentSock).OramaAccount(t.Context())
	if err != nil {
		t.Fatalf("read the run wallet's orama account: %v", err)
	}
	res := infra.Run(t, harness.CLI(t), "chain", "faucet", acct.Address, "--env", f.State.Env, "--amount", fundingNorama)
	infra.ExpectExit(t, res, infra.ExitOK)
}

// oneFreshServerBecomesAFullNode: `orama setup` on one fresh server installs
// the cluster node (it creates the cluster) and the global layer beside it and
// registers the operator; `orama status --json` is healthy, with the chain
// answering and caught up and the node a validator.
func oneFreshServerBecomesAFullNode(t *testing.T, f *fleet.Fleet, cli *oramacli.Runner, net, env string, extra harness.Extra) {
	t.Helper()
	args := append(setupArgs(t, net, env, extra), "--name", nodeName)
	res := infra.RunFor(t, cli, infra.InstallBudget, args...)
	infra.ExpectExit(t, res, infra.ExitOK, "Done.", "orama status --env "+env)
	for _, unit := range []string{infra.NodeUnit, infra.ChainUnit} {
		if out := f.Exec(t, extra.Node, "systemctl is-active "+unit); out.Exit != 0 {
			t.Errorf("%s is not active on %s after setup:\n%s", unit, extra.PublicIP, out.Stdout)
		}
	}
	doc := requireHealthy(t, cli, env, 1)
	chain := doc.Nodes[0].Chain
	if chain == nil || !chain.Responsive || chain.CatchingUp || !chain.Validator {
		t.Errorf("the node's chain: %+v, want an answering validator that has caught up", chain)
	}
	if doc.Operator == nil || !strings.HasPrefix(doc.Operator.Address, "orama1") {
		t.Errorf("orama status shows no operator account though setup recorded it: %+v", doc.Operator)
	}
	validatorCountsTowardItsOperator(t, cli, extra)
}

// validatorCountsTowardItsOperator: setup bound the validator's consensus key to
// the operator, so x/power attributes the validator to the operator's account and
// not to the shared "unlinked" bucket.
func validatorCountsTowardItsOperator(t *testing.T, cli *oramacli.Runner, extra harness.Extra) {
	t.Helper()
	acct, err := rwagent.New(cli.AgentSock).OramaAccount(t.Context())
	if err != nil {
		t.Fatalf("read the run wallet's orama account: %v", err)
	}
	valoper, err := clusterreg.ValidatorAddress(acct.Address)
	if err != nil {
		t.Fatalf("the validator address of %s: %v", acct.Address, err)
	}
	var power struct {
		Operator string `json:"operator"`
	}
	chain.New(t).Query(t, extra.Node, &power, "power", "validator-power", valoper)
	if power.Operator != acct.Address {
		t.Errorf("the validator counts toward %q, want the operator %s: setup binds the consensus key of the validator's node", power.Operator, acct.Address)
	}
}

// theSameCommandAgainChangesNothing: setup reads each server before it changes
// it, so the same command on a finished server skips every step it has and
// leaves the cluster healthy.
func theSameCommandAgainChangesNothing(t *testing.T, cli *oramacli.Runner, net, env string, extra harness.Extra) {
	t.Helper()
	args := append(setupArgs(t, net, env, extra), "--name", nodeName)
	res := infra.RunFor(t, cli, infra.InstallBudget, args...)
	infra.ExpectExit(t, res, infra.ExitOK, "cluster skipped", "global skipped")
	for _, installed := range []string{"cluster running", "global running"} {
		if strings.Contains(res.Stdout, installed) {
			t.Errorf("the second run installed again:\n%s", res.Stdout)
		}
	}
	requireHealthy(t, cli, env, 1)
}

// twoMoreJoinTheSameCluster: the same environment with two more addresses adds
// them to the cluster (the invites are minted over SSH on a node already in),
// and the three nodes are healthy together.
func twoMoreJoinTheSameCluster(t *testing.T, f *fleet.Fleet, cli *oramacli.Runner, net, env string, extras []harness.Extra) {
	t.Helper()
	args := append(setupArgs(t, net, env, extras[1], extras[2]), "--name", nodeName+"-more")
	res := infra.RunFor(t, cli, infra.InstallBudget, args...)
	infra.ExpectExit(t, res, infra.ExitOK, "Done.")
	for _, e := range extras[1:] {
		if !strings.Contains(res.Stdout, "["+e.PublicIP+"] cluster done") {
			t.Errorf("%s did not join the cluster:\n%s", e.PublicIP, res.Stdout)
		}
	}
	doc := requireHealthy(t, cli, env, 3)
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		seen[n.Host] = true
	}
	for _, e := range extras {
		if !seen[e.PublicIP] {
			t.Errorf("orama status does not list %s", e.PublicIP)
		}
	}
}

// TestSetup_clusterOnlyInstallsNoGlobalLayer: --cluster-only gives a fresh
// server the cluster node and none of the chain, storage or relay; the cluster
// is healthy, and no operator is registered or recorded.
func TestSetup_clusterOnlyInstallsNoGlobalLayer(t *testing.T) {
	f := harness.Fleet(t)
	cli := isolatedCLI(t)
	net := network(t, cli)
	extra := harness.ExtraNode(t, "join-one-cluster", extraLocation)
	env := net + "-clusteronly"

	args := append(setupArgs(t, net, env, extra), "--cluster-only")
	res := infra.RunFor(t, cli, infra.InstallBudget, args...)
	infra.ExpectExit(t, res, infra.ExitOK, "Done.")
	if out := f.Exec(t, extra.Node, "systemctl is-active "+infra.NodeUnit); out.Exit != 0 {
		t.Errorf("%s is not active after a cluster-only setup:\n%s", infra.NodeUnit, out.Stdout)
	}
	for _, absent := range []string{infra.ChainUnit, "orama-global-netns.service"} {
		if out := f.Exec(t, extra.Node, "test -e /etc/systemd/system/"+absent); out.Exit == 0 {
			t.Errorf("a cluster-only setup installed %s", absent)
		}
	}
	doc := requireHealthy(t, cli, env, 1)
	if doc.Nodes[0].Chain != nil || doc.Operator != nil {
		t.Errorf("a cluster-only node has a chain or an operator in its status: %+v", doc)
	}
	if strings.Contains(res.Stdout, "Operator account") {
		t.Errorf("a cluster-only run printed an operator account:\n%s", res.Stdout)
	}
}
