//go:build e2e_fleet

package dnstlschaos

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// withdrawBudget: the reaper marks a silent node inactive after 120s and
// drops its base records on the next 30s sweep; then the plugin's 30s cache.
const withdrawBudget = edge.ReapAfter + edge.PluginCacheTTL + time.Minute

// inBase reports, for every other nameserver, whether the base name still
// resolves to victim's address.
func inBase(t *testing.T, f *fleet.Fleet, victim fleet.Node) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range edge.Nameservers(f) {
		if n.Name == victim.Name {
			continue
		}
		a, err := edge.Query(t.Context(), "udp", n.PublicIP, f.State.BaseDomain, dnsmessage.TypeA)
		if err != nil {
			return nil, err
		}
		out[n.Name] = slices.Contains(a.Values(dnsmessage.TypeA), victim.PublicIP)
	}
	return out, nil
}

// victim is a nameserver that is not the rqlite leader, so the test never
// moves leadership.
func victim(t *testing.T) fleet.Node {
	t.Helper()
	r := infra.RequireHealthy(t)
	for _, n := range infra.Followers(t, r) {
		if n.Role == fleet.RoleNameserver || len(edge.Workers(harness.Fleet(t))) == 0 {
			return n
		}
	}
	t.Fatal("no follower nameserver to disturb")
	return fleet.Node{}
}

// TestEdgePromise_caddyDownLeavesTheRoundRobin: a dns_nodes row saying
// active is a promise that the node terminates TLS; a node whose Caddy is
// down must leave the base round-robin, or clients resolving the base name
// are sent to a closed port (core/pkg/node/components.go compDNSRegistration:
// "A dns_nodes row saying `active` is a promise"; docs/MONITORING.md "DNS").
// The heartbeat today is gated on Caddy only at start-up, so a Caddy that
// stops later keeps its record: this test fails until that holds at runtime.
func TestEdgePromise_caddyDownLeavesTheRoundRobin(t *testing.T) {
	f := harness.Fleet(t)
	v := victim(t)
	t.Cleanup(func() { requireBackInBase(t, f, v) })
	f.HoldDown(t, v, edge.CaddyUnit)
	pinned := harness.GW(t).PinTo(v.PublicIP)
	if resp, err := pinned.Send(t.Context(), gw.Req{Path: "/health"}); err == nil && resp.Status == http.StatusOK {
		t.Fatalf("%s still serves HTTPS with Caddy stopped", v.Name)
	}
	eventually.Require(t, edge.PollEvery, withdrawBudget, v.Name+" withdrawn from the base round-robin", func() (bool, error) {
		seen, err := inBase(t, f, v)
		if err != nil {
			return false, err
		}
		for ns, has := range seen {
			if has {
				return false, fmt.Errorf("%s still answers %s for the base name", ns, v.PublicIP)
			}
		}
		return true, nil
	})
}

// TestEdgePromise_auxUnitsDownKeepTheNode: Tor and ntfy terminate nothing
// for the node, so stopping them degrades it without taking it out of DNS:
// for longer than the reap window the base name keeps its address and it
// keeps serving HTTPS (core/pkg/node/index_host.go startIndexEdgeAux).
func TestEdgePromise_auxUnitsDownKeepTheNode(t *testing.T) {
	f := harness.Fleet(t)
	v := victim(t)
	stopped := 0
	for _, unit := range []string{edge.TorUnit, edge.NtfyUnit} {
		if f.Unit(t, v, unit) == "active" {
			f.HoldDown(t, v, unit)
			stopped++
		}
	}
	if stopped == 0 {
		t.Fatalf("%s runs neither %s nor %s: nothing to stop", v.Name, edge.TorUnit, edge.NtfyUnit)
	}
	pinned := harness.GW(t).PinTo(v.PublicIP)
	edge.Hold(t, edge.PollEvery, edge.ReapAfter, v.Name+" stays in DNS and serves", func() (bool, error) {
		seen, err := inBase(t, f, v)
		if err != nil {
			return false, err
		}
		for ns, has := range seen {
			if !has {
				return false, fmt.Errorf("%s dropped %s from the base name", ns, v.PublicIP)
			}
		}
		resp, err := pinned.Send(t.Context(), gw.Req{Path: "/health"})
		if err != nil || resp.Status != http.StatusOK {
			return false, fmt.Errorf("%s /health: %v %v", v.Name, err, resp)
		}
		return true, nil
	})
}

// requireBackInBase waits, from a cleanup, until every other nameserver
// answers the base name with v's address again, so the next test starts
// from the round-robin the cluster had.
func requireBackInBase(t *testing.T, f *fleet.Fleet, v fleet.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), withdrawBudget)
	defer cancel()
	err := eventually.Poll(ctx, edge.PollEvery, withdrawBudget, v.Name+" back in the base round-robin", func() (bool, error) {
		for _, n := range edge.Nameservers(f) {
			if n.Name == v.Name {
				continue
			}
			a, err := edge.Query(ctx, "udp", n.PublicIP, f.State.BaseDomain, dnsmessage.TypeA)
			if err != nil {
				return false, err
			}
			if !slices.Contains(a.Values(dnsmessage.TypeA), v.PublicIP) {
				return false, fmt.Errorf("%s does not answer %s yet", n.Name, v.PublicIP)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("cleanup: %v", err)
	}
}
