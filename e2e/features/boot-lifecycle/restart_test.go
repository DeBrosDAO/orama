//go:build e2e_fleet

package bootlifecycle

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// activeSince is a unit's ActiveEnterTimestampMonotonic (microseconds since
// boot): it orders the starts of units within one boot.
func activeSince(t testing.TB, f *fleet.Fleet, n fleet.Node, unit string) int64 {
	t.Helper()
	v := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p ActiveEnterTimestampMonotonic --value "+unit).Stdout)
	us, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		t.Fatalf("%s: %s start time %q: %v", n.Name, unit, v, err)
	}
	return us
}

// TestReboot_oneNodeQuorumIntact: one follower reboots while the other two
// hold quorum; it comes back on its own (no CLI), in dependency order
// (overlay, then rqlite, then the gateway), and the cluster reconverges with
// every node agreeing on the leader (core/e2e/lifecycle
// TestReboot_oneNode_quorumIntact; website/src/docs/contributor/architecture-reference.mdx unit ordering).
func TestReboot_oneNodeQuorumIntact(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	n := infra.Followers(t, r)[0]
	infra.Reboot(t, f, n)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" to rejoin after a reboot")
	wg := activeSince(t, f, n, infra.WireGuardUnit)
	rq := activeSince(t, f, n, infra.IndexRQLiteUnit)
	gwStart := activeSince(t, f, n, infra.IndexGatewayUnit)
	if !(wg <= rq && rq <= gwStart) {
		t.Errorf("%s started out of order: wireguard %d, rqlite %d, gateway %d (µs since boot)", n.Name, wg, rq, gwStart)
	}
}

// TestNodeRestart_oneNodeReconverges: `orama node restart` on a follower, the
// documented way to restart a node's services, passes its quorum check and
// the node reconverges (lifecycle TestReboot_oneNode_quorumIntact drove the
// same through a --node flag the CLI does not have: restart runs on the node).
func TestNodeRestart_oneNodeReconverges(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	n := infra.Followers(t, r)[1]
	out := infra.OnNode(t, f, n, "node", "restart")
	if out.Exit != infra.ExitOK || !strings.Contains(out.Stdout, "Quorum check") {
		t.Fatalf("orama node restart on %s: exit %d\n%s", n.Name, out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" after orama node restart")
}

// TestColdStart_servesBeforeQuorum: every node's services stop (forced: the
// quorum check refuses the later ones, as it should) and start again at
// once. The public surfaces, gateways and CoreDNS, serve again, and the
// cluster reconverges from the cold start (lifecycle
// TestReboot_allNodes_serveBeforeQuorum).
func TestColdStart_servesBeforeQuorum(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	t.Cleanup(func() {
		startAll(t, f)
		infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster after the cold start")
	})
	for _, n := range f.State.Nodes {
		if out := infra.OnNode(t, f, n, "node", "stop", "--force"); out.Exit != 0 {
			t.Fatalf("orama node stop --force on %s: exit %d\n%s", n.Name, out.Exit, f.Redact(out.Stderr))
		}
	}
	startAll(t, f)
	eventually.Require(t, infra.PollEvery, infra.ServingBudget, "every gateway and nameserver to serve", func() (bool, error) {
		rep, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		return true, rep.Serving()
	})
	infra.WaitConverged(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster to reconverge from a cold start")
}

// startAll runs `orama node start` on every core node at once: each start
// waits until its node serves, which needs the others.
func startAll(t testing.TB, f *fleet.Fleet) {
	var wg sync.WaitGroup
	for _, n := range f.State.Nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), infra.ColdStartBudget)
			defer cancel()
			out, err := f.SSH(ctx, n).Run(ctx, infra.OramaCommand("node", "start"))
			if err != nil || out.Exit != 0 {
				t.Errorf("orama node start on %s: exit %d: %v\n%s", n.Name, out.Exit, err, f.Redact(out.Stdout+out.Stderr))
			}
		}()
	}
	wg.Wait()
}
