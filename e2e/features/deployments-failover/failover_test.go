//go:build e2e_fleet

package deploymentsfailover

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// memHogApp is the app that exhausts its memory limit.
	memHogApp = "memhog"
	// oomKillResult is how systemd records a unit the OOM killer ended
	// ("Failed with result 'oom-kill'").
	oomKillResult = "oom-kill"
	unitInactive  = "inactive"
)

// TestDeployFailover_replicaServesWhenANodeStops: a dynamic app runs on its
// home node and a replica (core/pkg/deployments DefaultReplicaCount); with
// the unit stopped on one of them, every node still serves the app by name.
func TestDeployFailover_replicaServesWhenANodeStops(t *testing.T) {
	infra.HealthyAround(t)
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "fo"), "failover")
	serving(t, tn.app(u), "/health", "")
	unit := "orama-deploy-go@" + tn.instance("failover") + ".service"
	nodes := unitNodes(t, tn.f, unit)
	if len(nodes) < 2 {
		t.Fatalf("the app runs on %d node(s); there is no replica to fail over to", len(nodes))
	}
	tn.f.StopService(t, nodes[0], unit)
	for _, nc := range tenancy.PerNode(t, tn.f, tn.app(u)) {
		serving(t, nc.Client, "/version", "fo")
		// Served by the replica only while the stopped copy stays stopped.
		if s := tn.f.Unit(t, nodes[0], unit); s != unitInactive {
			t.Fatalf("%s is %s on %s when %s served the app: the failover was not exercised", unit, s, nodes[0].Name, nc.Node.Name)
		}
	}
}

// TestDeployFailover_oomKilledAppRestarts: an app that outgrows its memory
// limit is killed by the kernel and restarted by its unit (docs/whitepaper/technical-reference/vol1/11-app-deployments.md:
// MemoryMax comes from the deployment's recorded limits). The app's name has
// no "oom" in it, so only systemd's own verdict matches the journal search.
func TestDeployFailover_oomKilledAppRestarts(t *testing.T) {
	infra.HealthyAround(t)
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "mem"), memHogApp)
	serving(t, tn.app(u), "/health", "")
	unit := "orama-deploy-go@" + tn.instance(memHogApp) + ".service"
	before := restarts(t, tn, unit)
	tn.app(u).MustSend(t, gw.Req{Path: "/oom"})
	eventually.Require(t, pollEvery, startBudget, "the OOM-killed app to be restarted", func() (bool, error) {
		if now := restarts(t, tn, unit); now <= before {
			return false, fmt.Errorf("NRestarts %d, was %d", now, before)
		}
		return true, nil
	})
	serving(t, tn.app(u), "/health", "")
	oom := false
	for _, node := range unitNodes(t, tn.f, unit) {
		if strings.Contains(tn.f.Exec(t, node, "journalctl -u "+unit+" --no-pager -n 200").Stdout, oomKillResult) {
			oom = true
		}
	}
	if !oom {
		t.Errorf("no node's journal records systemd's %q for %s", oomKillResult, unit)
	}
}

// restarts sums NRestarts of unit over the core nodes.
func restarts(t testing.TB, tn *tenant, unit string) int {
	t.Helper()
	total := 0
	for _, node := range tn.f.State.Nodes {
		out := strings.TrimSpace(tn.f.Exec(t, node, "systemctl show -p NRestarts --value "+unit).Stdout)
		if n, err := strconv.Atoi(out); err == nil {
			total += n
		}
	}
	return total
}
