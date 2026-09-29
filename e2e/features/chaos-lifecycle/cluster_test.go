//go:build e2e_fleet

package chaoslifecycle

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// upgradeStepLine starts each node's step of `orama node upgrade`
	// (core/cmd/orama/internal/production/upgrade/remote.go).
	upgradeStepLine = "Upgrading "
	upgradeDone     = "Rolling upgrade complete"
	setupDone       = "setup complete"
	joiners         = 2
)

// TestChaosLifecycle_nodeKilledMidUpgrade: the node supervisor of the node
// being upgraded is SIGKILLed as its step starts. The upgrade command ends
// (whatever it reports), running the same upgrade again completes, the
// cluster converges, and the node answers with the release every other node
// runs (docs/CLI_REFERENCE.md "orama node upgrade").
func TestChaosLifecycle_nodeKilledMidUpgrade(t *testing.T) {
	f := harness.Fleet(t)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	cli := harness.CLI(t)
	args := []string{"node", "upgrade", "--env", f.State.Env, "--node", victim.PublicIP, "--yes"}
	p := start(t, cli, args...)
	waitLine(t, p, upgradeStepLine+victim.PublicIP)
	f.Kill(t, victim, nodeUnit)
	res := finish(t, p, infra.UpgradeBudget)
	t.Logf("the interrupted upgrade exited %d", res.Exit)
	infra.ExpectExit(t, infra.RunFor(t, cli, infra.UpgradeBudget, args...), infra.ExitOK, upgradeDone)
	healed(t, "the cluster after the interrupted upgrade")
	versions := map[string]string{}
	for _, n := range f.State.Nodes {
		var v struct {
			Version string `json:"version"`
		}
		if err := harness.GW(t).PinTo(n.PublicIP).MustSend(t, gw.Req{Path: "/v1/version"}).Expect(t, http.StatusOK).Decode(&v); err != nil {
			t.Fatal(err)
		}
		versions[n.Name] = v.Version
	}
	for name, v := range versions {
		if v != versions[victim.Name] {
			t.Errorf("%s runs %s but the re-upgraded %s runs %s", name, v, victim.Name, versions[victim.Name])
		}
	}
}

// TestChaosLifecycle_concurrentJoinsWhileDeploying: two fresh servers join
// at the same moment, each with its own invite, while a customer deploys an
// app. Both joins complete, the cluster converges with every member, and
// the app is served by name from the new nodes too; the cleanups remove both
// and the core cluster converges again (docs/DEV_DEPLOY.md;
// docs/DEPLOYMENT_GUIDE.md "Cross-Node Routing").
func TestChaosLifecycle_concurrentJoinsWhileDeploying(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	tn := realistic.NewTenant(t)
	archive := infra.RunningArchive(t, f)
	cli := harness.CLI(t)
	var setups [][]string
	var hosts []string
	for i := range joiners {
		extra := infra.NewExtra(t, fmt.Sprintf("extra-chaos-%d", i))
		t.Cleanup(func() { infra.RemoveIfMember(t, extra.PublicIP) })
		setups = append(setups, infra.SetupArgs(t, extra, archive))
		hosts = append(hosts, extra.PublicIP)
	}
	results := runConcurrently(t, cli, setups)
	u := tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: "joins"}, nil), "joins")
	for i, res := range results.wait() {
		infra.ExpectExit(t, res, infra.ExitOK, setupDone)
		t.Logf("join %d (%s) finished in %s", i, hosts[i], res.Duration)
	}
	infra.WaitConverged(t, len(f.State.Nodes)+joiners, infra.ConvergeBudget, "the cluster with both joiners")
	for _, h := range hosts {
		realistic.Serving(t, tn.App(u).PinTo(h), "/", "joins")
	}
}

// pending is a set of CLI runs in flight.
type pending struct {
	wg  sync.WaitGroup
	out []oramacli.Result
}

// runConcurrently starts every argument list at once, each under the
// install budget.
func runConcurrently(t *testing.T, cli *oramacli.Runner, runs [][]string) *pending {
	t.Helper()
	p := &pending{out: make([]oramacli.Result, len(runs))}
	for i, args := range runs {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			ctx, cancel := context.WithTimeout(t.Context(), infra.InstallBudget)
			defer cancel()
			res, err := cli.Run(ctx, args...)
			if err != nil {
				res.Exit, res.Stderr = -1, err.Error()
			}
			p.out[i] = res
		}()
	}
	return p
}

func (p *pending) wait() []oramacli.Result {
	p.wg.Wait()
	return p.out
}
