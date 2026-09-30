//go:build e2e_fleet

package bootlifecycle

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// dnsBudget: a stale answer that takes longer than a resolver's timeout is
// no answer (lifecycle TestIndexRQLiteDown_dnsServesStale).
const dnsBudget = 5 * time.Second

// systemd active states of a running or starting unit.
const (
	unitActive     = "active"
	unitActivating = "activating"
)

// resolveAt asks the nameserver at ip, and only it, for name's A records.
func resolveAt(ctx context.Context, ip, name string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: dnsBudget}).DialContext(ctx, network, net.JoinHostPort(ip, "53"))
	}}
	ctx, cancel := context.WithTimeout(ctx, dnsBudget)
	defer cancel()
	return r.LookupHost(ctx, name)
}

func nameservers(f *fleet.Fleet) []fleet.Node {
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if n.Role == fleet.RoleNameserver {
			out = append(out, n)
		}
	}
	return out
}

// TestIndexRQLiteDown_dnsServesStale: with the index rqlite stopped on every
// node, every nameserver still answers for the zone at once, from its stale
// cache, instead of SERVFAIL for every name (lifecycle
// TestIndexRQLiteDown_dnsServesStale; docs/ARCHITECTURE.md CoreDNS reads
// dns_records from the index rqlite). The cleanup starts rqlite again and
// waits for the cluster.
func TestIndexRQLiteDown_dnsServesStale(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	nss := nameservers(f)
	if len(nss) == 0 {
		harness.SkipNotApplicable(t, "the run has no nameserver node (every node's role is node)")
	}
	name := f.State.BaseDomain
	want := map[string][]string{}
	for _, ns := range nss {
		got, err := resolveAt(t.Context(), ns.PublicIP, name)
		if err != nil || len(got) == 0 {
			t.Fatalf("precondition: %s does not answer for %s: %v", ns.Name, name, err)
		}
		want[ns.Name] = got
	}
	t.Cleanup(func() {
		infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster after rqlite came back")
	})
	for _, n := range f.State.Nodes {
		f.StopService(t, n, infra.IndexRQLiteUnit)
	}
	requireRQLiteDown(t, f, "before resolving")
	for _, ns := range nss {
		got, err := resolveAt(t.Context(), ns.PublicIP, name)
		if err != nil || len(got) == 0 {
			t.Errorf("%s stopped answering for %s with the index rqlite down: %v", ns.Name, name, err)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want[ns.Name]) {
			t.Logf("%s answers %v, before %v", ns.Name, got, want[ns.Name])
		}
	}
	requireRQLiteDown(t, f, "after resolving")
}

// requireRQLiteDown fails unless the index rqlite is stopped on every node:
// the supervisor starting it again would mean the answers were not served
// with rqlite down.
func requireRQLiteDown(t testing.TB, f *fleet.Fleet, when string) {
	t.Helper()
	for _, n := range f.State.Nodes {
		if st := f.Unit(t, n, infra.IndexRQLiteUnit); st == unitActive || st == unitActivating {
			t.Fatalf("%s: %s is %s %s: DNS was not observed with the index rqlite down", n.Name, infra.IndexRQLiteUnit, st, when)
		}
	}
}
