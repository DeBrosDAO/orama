//go:build e2e_fleet

package namespaceschaos

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pollEvery = 5 * time.Second
	// dnsFlipBudget: three consecutive 30s probes must agree before a node
	// withdraws itself or comes back (core/pkg/gateway/namespace_health.go),
	// plus the probe in flight and NXDOMAIN/answer caching (30s).
	dnsFlipBudget = 4 * time.Minute
	// reconcileBudget: the tenant reconciler sweeps every 60s
	// (docs/ARCHITECTURE.md "The tenant plane converges"); two sweeps and a
	// restart.
	reconcileBudget = 3 * time.Minute
	// driftedPartitions is a partitionCount the spawner never writes (it
	// writes 12, core/pkg/namespace systemd_spawner.go olricPartitionCount).
	driftedPartitions = "13"
)

// advertised is an eventually probe: does every nameserver answer host with
// ip present (want) or absent (!want)?
func advertised(t testing.TB, f *fleet.Fleet, host, ip string, want bool) func() (bool, error) {
	return func() (bool, error) {
		for _, nsNode := range tenancy.Nameservers(f) {
			addrs, err := tenancy.ResolveAt(t.Context(), nsNode.PublicIP, host)
			if err != nil {
				return false, fmt.Errorf("%s: %v", nsNode.Name, err)
			}
			if slices.Contains(addrs, ip) != want {
				return false, fmt.Errorf("%s answers %v", nsNode.Name, addrs)
			}
		}
		return true, nil
	}
}

// TestNamespaceHealth_dnsWithdrawAndRestore: a node whose namespace gateway
// hangs withdraws itself from ns-<name> after three failed probes, and comes
// back after three healthy ones once the gateway answers again
// (docs/ARCHITECTURE.md; core/pkg/gateway/namespace_health.go).
func TestNamespaceHealth_dnsWithdrawAndRestore(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	host, victim := tenancy.NamespaceHost(f, n.Name), f.State.Nodes[1]
	eventually.Require(t, pollEvery, dnsFlipBudget, "every node advertised", advertised(t, f, host, victim.PublicIP, true))
	tenancy.Freeze(t, f, victim, tenancy.UnitGateway(n.Name))
	eventually.Require(t, pollEvery, dnsFlipBudget, victim.Name+" withdrawn from "+host, advertised(t, f, host, victim.PublicIP, false))
	for _, other := range f.State.Nodes {
		if other.Name != victim.Name {
			eventually.Require(t, pollEvery, dnsFlipBudget, other.Name+" still advertised", advertised(t, f, host, other.PublicIP, true))
		}
	}
	// Pinned to a survivor: a resolver may still hold the victim's record.
	tenancy.Get(t, n.Client.PinTo(f.State.Nodes[0].PublicIP), "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
	tenancy.Thaw(t, f, victim, tenancy.UnitGateway(n.Name))
	eventually.Require(t, pollEvery, dnsFlipBudget, victim.Name+" advertised again", advertised(t, f, host, victim.PublicIP, true))
}

// TestNamespaceHealth_lastRecordNeverWithdrawn: with every node's namespace
// gateway hung, at least one record stays: a withdrawal never removes the last
// active record for a name (namespace_health.go withdrawNamespaceHostRecordSQL).
func TestNamespaceHealth_lastRecordNeverWithdrawn(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	host := tenancy.NamespaceHost(f, n.Name)
	for _, node := range f.State.Nodes {
		tenancy.Freeze(t, f, node, tenancy.UnitGateway(n.Name))
	}
	// Every node withdraws itself except the one that would leave the name
	// with no answer: the steady state is exactly one record.
	eventually.Require(t, pollEvery, dnsFlipBudget, "all but one node withdrawn, never all", func() (bool, error) {
		for _, nsNode := range tenancy.Nameservers(f) {
			addrs, err := tenancy.ResolveAt(t.Context(), nsNode.PublicIP, host)
			if err != nil {
				return false, err
			}
			if len(addrs) == 0 {
				return false, eventually.Stop(fmt.Errorf("%s answers nothing for %s: the last record was withdrawn", nsNode.Name, host))
			}
			if len(addrs) != 1 {
				return false, fmt.Errorf("%s answers %v", nsNode.Name, addrs)
			}
		}
		return true, nil
	})
	for _, node := range f.State.Nodes {
		tenancy.Thaw(t, f, node, tenancy.UnitGateway(n.Name))
	}
	for _, node := range f.State.Nodes {
		eventually.Require(t, pollEvery, dnsFlipBudget, node.Name+" advertised again", advertised(t, f, host, node.PublicIP, true))
	}
}

// TestNamespaceReconciler_startsStoppedUnits: a stopped tenant service is
// started again by the reconciler within a sweep or two, and the namespace
// serves meanwhile and afterwards.
func TestNamespaceReconciler_startsStoppedUnits(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	victim := f.State.Nodes[2]
	for _, unit := range tenancy.TenantUnits(n.Name) {
		f.StopService(t, victim, unit)
	}
	for _, unit := range tenancy.TenantUnits(n.Name) {
		eventually.Require(t, pollEvery, reconcileBudget, "the reconciler to start "+unit, func() (bool, error) {
			if s := f.Unit(t, victim, unit); s != "active" {
				return false, fmt.Errorf("%s is %s", unit, s)
			}
			return true, nil
		})
	}
	c := n.Client.PinTo(victim.PublicIP)
	eventually.Require(t, pollEvery, reconcileBudget, victim.Name+" to serve the namespace again", func() (bool, error) {
		r := tenancy.Post(t, c, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"})
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("HTTP %d", r.Status)
		}
		return true, nil
	})
}

// listenLine is the gateway YAML's listen address; it is the one line read,
// because the rest of that file holds credentials.
var listenLine = regexp.MustCompile(`(?m)^listen_addr:\s*(\S+)\s*$`)

// TestNamespaceReconciler_rewritesDriftedConfig: the reconciler rewrites a
// gateway config that drifted and restarts the gateway onto it; it rewrites a
// drifted Olric config without restarting Olric, which is clustered and
// stateful (docs/ARCHITECTURE.md "The tenant plane converges").
func TestNamespaceReconciler_rewritesDriftedConfig(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	node := f.State.Nodes[0]
	cfg := tenancy.NamespacesDir + "/" + n.Name + "/configs"
	gwYAML, olricYAML := cfg+"/gateway-*.yaml", cfg+"/olric-*.yaml"
	orig := listenLine.FindStringSubmatch(f.MustExec(t, node, "grep -h '^listen_addr:' "+gwYAML).Stdout)
	if orig == nil {
		t.Fatal("the gateway YAML has no listen_addr")
	}
	gwSince := tenancy.ActiveSince(t, f, node, tenancy.UnitGateway(n.Name))
	olricSince := tenancy.ActiveSince(t, f, node, tenancy.UnitOlric(n.Name))
	t.Cleanup(func() { undrift(t, f, node, gwYAML, olricYAML, orig[1]) })
	f.MustExec(t, node, "sed -i 's/^listen_addr:.*/listen_addr: "+node.WGIP+":1/' "+gwYAML+
		" && sed -i 's/^partitionCount:.*/partitionCount: "+driftedPartitions+"/' "+olricYAML)
	eventually.Require(t, pollEvery, reconcileBudget, "the gateway config to be rewritten", func() (bool, error) {
		got := listenLine.FindStringSubmatch(f.MustExec(t, node, "grep -h '^listen_addr:' "+gwYAML).Stdout)
		if got == nil || got[1] != orig[1] {
			return false, fmt.Errorf("listen_addr is %v", got)
		}
		return true, nil
	})
	eventually.Require(t, pollEvery, reconcileBudget, "the Olric config to be rewritten", func() (bool, error) {
		if out := f.MustExec(t, node, "grep -h '^partitionCount:' "+olricYAML).Stdout; strings.Contains(out, driftedPartitions) {
			return false, fmt.Errorf("still %s", strings.TrimSpace(out))
		}
		return true, nil
	})
	if tenancy.ActiveSince(t, f, node, tenancy.UnitGateway(n.Name)) == gwSince {
		t.Error("the gateway was not restarted onto its rewritten config")
	}
	if tenancy.ActiveSince(t, f, node, tenancy.UnitOlric(n.Name)) != olricSince {
		t.Error("Olric was restarted by the reconciler; its config must apply at its next deliberate restart")
	}
	pinned := n.Client.PinTo(node.PublicIP)
	eventually.Require(t, pollEvery, reconcileBudget, node.Name+" to serve after the gateway restart", func() (bool, error) {
		r, err := pinned.Send(t.Context(), gw.Req{Path: "/health"})
		if err != nil {
			return false, err
		}
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("HTTP %d", r.Status)
		}
		return true, nil
	})
}

// undrift puts the original lines back if the reconciler did not, and
// restarts what it changed, so the next test finds the node as it was.
func undrift(t testing.TB, f *fleet.Fleet, node fleet.Node, gwYAML, olricYAML, listen string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	cmd := "grep -q '^partitionCount: " + driftedPartitions + "' " + olricYAML + " && sed -i 's/^partitionCount:.*/partitionCount: 12/' " + olricYAML + "; " +
		"grep -q '^listen_addr: " + listen + "$' " + gwYAML + " || sed -i 's/^listen_addr:.*/listen_addr: " + listen + "/' " + gwYAML + "; true"
	if out, err := f.SSH(ctx, node).Run(ctx, cmd); err != nil || out.Exit != 0 {
		t.Errorf("cleanup: could not restore the configs on %s: %v %s", node.Name, err, out.Stderr)
	}
}
