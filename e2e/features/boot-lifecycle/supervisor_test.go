//go:build e2e_fleet

package bootlifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
)

// TestSupervisor_crashedDaemonsComeBack: a daemon killed with SIGKILL, the
// way a crash ends it, is running again without an operator, the node
// reconverges, and nothing is reported crash-looping afterwards (the node
// report's restart_loop_risk; docs/ARCHITECTURE.md "the supervisor").
func TestSupervisor_crashedDaemonsComeBack(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	n := infra.Followers(t, r)[0]
	for _, unit := range []string{infra.IndexGatewayUnit, infra.IndexOlricUnit, infra.IndexRQLiteUnit} {
		t.Run(unit, func(t *testing.T) {
			before := activeSince(t, f, n, unit)
			f.Kill(t, n, unit)
			eventually.Require(t, infra.PollEvery, fleet.UnitRecoverBudget, unit+" to run again on its own", func() (bool, error) {
				if s := f.Unit(t, n, unit); s != "active" {
					return false, fmt.Errorf("%s is %s", unit, s)
				}
				if activeSince(t, f, n, unit) == before {
					return false, fmt.Errorf("%s has not restarted yet", unit)
				}
				return true, nil
			})
			infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" after "+unit+" crashed")
		})
	}
}

// TestKillVoter_survivorsForgetIt: a fourth voter is destroyed with no clean
// shutdown. The three survivors keep quorum, and every membership view
// forgets it: gone from the node list and from every WireGuard peer set, so
// it cannot come back as a phantom (lifecycle
// TestKillVoter_survivorsKeepWritingAndForgetIt, played on a joined extra so
// the run keeps its three core nodes).
func TestKillVoter_survivorsForgetIt(t *testing.T) {
	f := harness.Fleet(t)
	extra := infra.NewJoinedExtra(t, "extra-victim")
	rep := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	wg, err := infra.WGIPOf(rep, extra.PublicIP)
	if err != nil {
		t.Fatal(err)
	}
	st := *f.State
	st.Extras = []fleet.Node{extra.Node}
	ctx, cancel := context.WithTimeout(t.Context(), infra.InstallBudget)
	defer cancel()
	if err := provision.DestroyNode(ctx, &st, extra.PublicIP); err != nil {
		t.Fatalf("destroy %s: %v", extra.Name, err)
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the survivors to keep quorum without the destroyed voter")
	eventually.Require(t, infra.PollEvery, infra.ConvergeBudget, "every view to forget "+wg, func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		return true, r.Forgotten(wg)
	})
}
