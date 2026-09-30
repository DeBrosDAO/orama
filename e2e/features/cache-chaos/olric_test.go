//go:build e2e_fleet

package cachechaos

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// servingBudget is how long the survivors may take to serve again after a
// member leaves: memberlist marks it dead and partitions move.
const servingBudget = 2 * reconnectBudget

// TestOlricChaos_memberLossServedAndRejoined stops one node's Olric: the other
// nodes' gateways keep serving, the tenant reconciler starts the member again
// (docs/ARCHITECTURE.md "The tenant plane converges"), and the rejoined node's
// gateway serves what was written while it was away.
func TestOlricChaos_memberLossServedAndRejoined(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	victim := f.State.Nodes[1]
	survivors := []int{0, 2}
	f.StopService(t, victim, tenancy.UnitOlric(n.Name))
	for _, i := range survivors {
		c := n.Client.PinTo(f.State.Nodes[i].PublicIP)
		key := "during-" + f.State.Nodes[i].Name
		eventually.Require(t, pollEvery, servingBudget, f.State.Nodes[i].Name+" to serve with a member down", func() (bool, error) {
			resp, err := cachePut(t, c, n, key, "v")
			return wantStatus(resp, err, http.StatusOK)
		})
	}
	eventually.Require(t, pollEvery, reconcileBudget, "the reconciler to start "+tenancy.UnitOlric(n.Name)+" again", func() (bool, error) {
		if s := f.Unit(t, victim, tenancy.UnitOlric(n.Name)); s != "active" {
			return false, fmt.Errorf("%s is %s", tenancy.UnitOlric(n.Name), s)
		}
		return true, nil
	})
	rejoined := n.Client.PinTo(victim.PublicIP)
	for _, i := range survivors {
		key := "during-" + f.State.Nodes[i].Name
		eventually.Require(t, pollEvery, servingBudget, victim.Name+" to read "+key, func() (bool, error) {
			resp, err := cacheGet(t, rejoined, n, key)
			return wantStatus(resp, err, http.StatusOK)
		})
	}
}

// TestOlricChaos_unreachableCacheAnswers503ThenReconnects freezes Olric on
// every node: each gateway drops its client after three failed probes and
// answers 503, then reconnects on its own once Olric answers again, without
// the gateway restarting (docs/ARCHITECTURE.md "Olric is supervised").
func TestOlricChaos_unreachableCacheAnswers503ThenReconnects(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	started := map[string]string{}
	for _, node := range f.State.Nodes {
		started[node.Name] = tenancy.ActiveSince(t, f, node, tenancy.UnitGateway(n.Name))
	}
	for _, node := range f.State.Nodes {
		tenancy.Freeze(t, f, node, tenancy.UnitOlric(n.Name))
	}
	for _, node := range f.State.Nodes {
		c := n.Client.PinTo(node.PublicIP)
		eventually.Require(t, pollEvery, dropBudget, node.Name+" to answer 503 without a cache", func() (bool, error) {
			resp, err := cachePut(t, c, n, "k", "v")
			return wantStatus(resp, err, http.StatusServiceUnavailable)
		})
		if resp := c.MustSend(t, reqHealth(n)); resp.Status != http.StatusServiceUnavailable {
			t.Errorf("%s: cache health answered %d while Olric is unreachable", node.Name, resp.Status)
		}
	}
	for _, node := range f.State.Nodes {
		tenancy.Thaw(t, f, node, tenancy.UnitOlric(n.Name))
	}
	for _, node := range f.State.Nodes {
		c := n.Client.PinTo(node.PublicIP)
		eventually.Require(t, pollEvery, reconnectBudget, node.Name+" to reconnect to Olric", func() (bool, error) {
			resp, err := cachePut(t, c, n, "after-"+node.Name, "v")
			return wantStatus(resp, err, http.StatusOK)
		})
		if now := tenancy.ActiveSince(t, f, node, tenancy.UnitGateway(n.Name)); now != started[node.Name] {
			t.Errorf("%s: the gateway restarted (active since %s, was %s); it must reconnect in place", node.Name, now, started[node.Name])
		}
	}
}

// TestOlricChaos_memoryOnly: Olric is in-memory only (docs/ARCHITECTURE.md
// "Olric v0.7.0 is in-memory only"): its config names no data directory, and
// crashing every member loses every entry.
func TestOlricChaos_memoryOnly(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	n := ns.New(t, f, ns.Options{})
	c := n.Client.For(t)
	for _, node := range f.State.Nodes {
		assertNoDataDir(t, f, node, n)
	}
	put, err := cachePut(t, c, n, "volatile", "v")
	mustAnswer(t, put, err).Expect(t, http.StatusOK)
	for _, node := range f.State.Nodes {
		f.Kill(t, node, tenancy.UnitOlric(n.Name))
	}
	eventually.Require(t, pollEvery, reconcileBudget, "the cache to serve again after every member crashed", func() (bool, error) {
		resp, err := cachePut(t, c, n, "probe", "v")
		return wantStatus(resp, err, http.StatusOK)
	})
	got, err := cacheGet(t, c, n, "volatile")
	mustAnswer(t, got, err).Expect(t, http.StatusNotFound)
}

// olricConfigLine is OLRIC_SERVER_CONFIG in the unit's root-owned env file
// (core/pkg/systemd/manager.go envFilePath: /var/lib/orama-unit-env/<ns>/olric.env).
var olricConfigLine = regexp.MustCompile(`(?m)^OLRIC_SERVER_CONFIG=(\S+)$`)

// olricTopLevelKeys is everything the spawner writes (core/pkg/namespace
// systemd_spawner.go olricConfig): no storage or data directory.
var olricTopLevelKeys = map[string]bool{"server": true, "memberlist": true, "partitionCount": true}

func assertNoDataDir(t testing.TB, f *fleet.Fleet, node fleet.Node, n *ns.Namespace) {
	t.Helper()
	env := string(f.ReadFile(t, node, "/var/lib/orama-unit-env/"+n.Name+"/olric.env"))
	m := olricConfigLine.FindStringSubmatch(env)
	if m == nil {
		t.Fatalf("%s: olric.env names no OLRIC_SERVER_CONFIG", node.Name)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(f.ReadFile(t, node, m[1]), &cfg); err != nil {
		t.Fatalf("%s: %s is not YAML: %v", node.Name, m[1], err)
	}
	for k := range cfg {
		if !olricTopLevelKeys[k] || strings.Contains(strings.ToLower(k), "dir") {
			t.Errorf("%s: Olric config has %q; the cache must be memory-only", node.Name, k)
		}
	}
}

func reqHealth(n *ns.Namespace) gw.Req {
	return gw.Req{Path: pathHealth, Bearer: n.Owner.Token()}
}
