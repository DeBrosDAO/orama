//go:build e2e_fleet

package namespacescapacity

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	codeQuota = "NAMESPACE_QUOTA"
	// refusalBudget is how long an over-capacity namespace may take to be
	// reported failed when the refusal is not immediate.
	refusalBudget = ns.ReadyBudget
	pollEvery     = 10 * time.Second
)

var capLine = regexp.MustCompile(`(?m)^\s*max-namespaces-per-wallet:\s*(\d+)\s*$`)

// walletCap reads the per-wallet cap from `orama cluster settings show`.
func walletCap(t testing.TB, f *fleet.Fleet) int {
	t.Helper()
	out := oramacli.ForState(f.State, f.Recorder()).For(t).MustOK(t, "cluster", "settings", "show").Stdout
	m := capLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("`orama cluster settings show` printed no max-namespaces-per-wallet:\n%s", out)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// allocations counts the tenant port blocks the cluster has allocated on
// each node, by node id: namespace_port_allocations is what the provisioner
// allocates from and checks the per-node cap against (core/pkg/namespace
// cluster_manager.go), so it counts what the cap counts, directories or not.
func allocations(t testing.TB, f *fleet.Fleet) map[string]int {
	t.Helper()
	q := infra.IndexQuery(t, f, f.State.Nodes[0], "SELECT node_id, COUNT(*) FROM namespace_port_allocations GROUP BY node_id")
	out := map[string]int{}
	for _, row := range q.Values {
		id, _ := row[0].(string)
		c, ok := row[1].(float64)
		if id == "" || !ok {
			t.Fatalf("namespace_port_allocations answered an unreadable row %v", row)
		}
		out[id] = int(c)
	}
	return out
}

// freeSlots is how many more namespaces the fullest node has room for.
func freeSlots(t testing.TB, f *fleet.Fleet) int {
	t.Helper()
	most := 0
	for _, c := range allocations(t, f) {
		most = max(most, c)
	}
	return tenancy.MaxPerNode - most
}

// createAll posts count creations as owner and adopts each (waiting until it
// serves; they provision concurrently, having all been posted first).
func createAll(t testing.TB, f *fleet.Fleet, owner *gw.User, count int) []*ns.Namespace {
	t.Helper()
	var created []tenancy.Created
	for i := range count {
		resp := tenancy.Create(t, owner, ns.UniqueName(fmt.Sprintf("%s-%d", t.Name(), i)))
		var c tenancy.Created
		if err := resp.Expect(t, http.StatusAccepted).Decode(&c); err != nil {
			t.Fatal(err)
		}
		created = append(created, c)
	}
	out := make([]*ns.Namespace, 0, count)
	for _, c := range created {
		out = append(out, tenancy.Adopt(t, f, owner, c))
	}
	return out
}

// TestNamespaceCapacity_walletQuota: a wallet at its cap is refused
// NAMESPACE_QUOTA before anything is provisioned (docs/API_SURFACE.md
// "Namespace management": per-wallet cap defaults to 10).
func TestNamespaceCapacity_walletQuota(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	limit, free := walletCap(t, f), freeSlots(t, f)
	if limit > free {
		harness.SkipNotApplicable(t, fmt.Sprintf("the per-wallet cap (%d) is above the free capacity (%d); "+
			"lower it with `orama cluster settings set max-namespaces-per-wallet` or run on an emptier fleet", limit, free))
	}
	owner := tenancy.Creator(t, f)
	createAll(t, f, owner, limit)
	resp := tenancy.Create(t, owner, ns.UniqueName(t.Name()+"-over"))
	if resp.Status != http.StatusForbidden || resp.ErrorCode() != codeQuota {
		t.Fatalf("a wallet at its cap of %d: want 403 %s, got %d %s", limit, codeQuota, resp.Status, resp.Body)
	}
}

// TestNamespaceCapacity_perNodeCapRefusesCleanly fills every node to its
// twenty tenant blocks (core/pkg/namespace MaxNamespacesPerNode), then asks
// for one more: it must be refused or reported failed, never provisioned into
// a port outside the tenant range, and every existing namespace keeps serving.
func TestNamespaceCapacity_perNodeCapRefusesCleanly(t *testing.T) {
	f := harness.Fleet(t)
	infra.HealthyAround(t)
	free, limit := freeSlots(t, f), walletCap(t, f)
	var all []*ns.Namespace
	for len(all) < free {
		all = append(all, createAll(t, f, tenancy.Creator(t, f), min(limit, free-len(all)))...)
	}
	allocs := allocations(t, f)
	if len(allocs) != len(f.State.Nodes) {
		t.Errorf("tenant blocks are allocated on %d nodes after filling, want all %d: %v", len(allocs), len(f.State.Nodes), allocs)
	}
	for id, got := range allocs {
		if got != tenancy.MaxPerNode {
			t.Errorf("node %s has %d tenant blocks allocated after filling, want %d", id, got, tenancy.MaxPerNode)
		}
	}
	for _, node := range f.State.Nodes {
		for _, n := range all {
			block := tenancy.PortBlock(t, f, node, n.Name)
			if block[0] < tenancy.PortRangeStart || block[len(block)-1] > tenancy.PortRangeEnd {
				t.Errorf("%s: %s was given %v, outside the tenant range", node.Name, n.Name, block)
			}
		}
	}
	overOwner := tenancy.Creator(t, f)
	resp := tenancy.Create(t, overOwner, ns.UniqueName(t.Name()+"-over"))
	assertNotProvisioned(t, f, overOwner, resp)
	for _, n := range all {
		tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1"}).Expect(t, http.StatusOK)
	}
}

// assertNotProvisioned accepts an immediate refusal, a creation that says its
// cluster was not started, or a cluster that ends "failed" — and nothing that
// ends "ready".
func assertNotProvisioned(t testing.TB, f *fleet.Fleet, owner *gw.User, resp *gw.Response) {
	t.Helper()
	if resp.Status >= http.StatusBadRequest {
		return
	}
	var c tenancy.Created
	if err := resp.Decode(&c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		n := &ns.Namespace{Name: c.Name, ClusterID: c.ClusterID, URL: gw.NamespaceURL(f.State, c.Name)}
		n.Client, n.Owner = owner.Client.WithBase(n.URL), &gw.User{Wallet: owner.Wallet, Namespace: c.Name, Client: owner.Client}
		if err := tenancy.DeleteIfPresent(n); err != nil {
			t.Errorf("cleanup: the over-capacity namespace %s may leak: %v", c.Name, err)
		}
	})
	if c.ClusterID == "" {
		if c.Cluster == "" {
			t.Fatalf("an over-capacity creation reported no cluster and no reason: %s", resp.Body)
		}
		return
	}
	eventually.Require(t, pollEvery, refusalBudget, "the over-capacity cluster to be reported failed", func() (bool, error) {
		st, err := tenancy.Status(t.Context(), owner.Client, c.ClusterID)
		if err != nil {
			return false, err
		}
		switch st.Status {
		case ns.StatusReady:
			return false, eventually.Stop(fmt.Errorf("a namespace beyond the per-node cap became ready"))
		case ns.StatusFailed:
			if st.Error == "" {
				return false, eventually.Stop(fmt.Errorf("failed with no reason"))
			}
			return true, nil
		}
		return false, fmt.Errorf("status %s", st.Status)
	})
}
