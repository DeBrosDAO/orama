//go:build e2e_fleet

package cache

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// crossNodeBudget is how long a write through one node's gateway may take to
// be visible through another's. Olric is one cluster per namespace
// (docs/ARCHITECTURE.md "Olric"), so this is the time of one routed read.
const crossNodeBudget = 10 * time.Second

// TestCacheConsistency_writeOnOneNodeReadOnEvery writes through each node's
// gateway in turn and reads through every other node's.
func TestCacheConsistency_writeOnOneNodeReadOnEvery(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	for i, writer := range nodes {
		key := "from-" + writer.Node.Name
		put(t, writer.Client, tenancy.Owner(n), "xnode", key, float64(i), "").Expect(t, http.StatusOK)
		for _, reader := range nodes {
			eventually.Require(t, pollEvery, crossNodeBudget, reader.Node.Name+" to read "+key, func() (bool, error) {
				resp := get(t, reader.Client, tenancy.Owner(n), "xnode", key)
				if resp.Status != http.StatusOK {
					return false, fmt.Errorf("HTTP %d", resp.Status)
				}
				return true, nil
			})
		}
	}
}

// TestCacheConsistency_deleteOnOneNodeGoneOnEvery deletes through the last
// node what the first wrote, and checks no gateway still serves it.
func TestCacheConsistency_deleteOnOneNodeGoneOnEvery(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	first, last := nodes[0], nodes[len(nodes)-1]
	put(t, first.Client, tenancy.Owner(n), "xnode", "doomed", "v", "").Expect(t, http.StatusOK)
	mustGet(t, last.Client, tenancy.Owner(n), "xnode", "doomed")
	tenancy.Post(t, last.Client, pathDelete, tenancy.Owner(n), map[string]any{"dmap": "xnode", "key": "doomed"}).Expect(t, http.StatusOK)
	for _, nc := range nodes {
		eventually.Require(t, pollEvery, crossNodeBudget, nc.Node.Name+" to stop serving the deleted key", func() (bool, error) {
			if resp := get(t, nc.Client, tenancy.Owner(n), "xnode", "doomed"); resp.Status != http.StatusNotFound {
				return false, fmt.Errorf("HTTP %d", resp.Status)
			}
			return true, nil
		})
	}
}

// TestCacheConsistency_scanSeesEveryNodesWrites: keys written through
// different nodes are one map.
func TestCacheConsistency_scanSeesEveryNodesWrites(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	for _, nc := range nodes {
		put(t, nc.Client, tenancy.Owner(n), "xscan", nc.Node.Name, 1, "").Expect(t, http.StatusOK)
	}
	eventually.Require(t, pollEvery, crossNodeBudget, "one scan to list every node's key", func() (bool, error) {
		if keys := scanKeys(t, n, "xscan", ""); len(keys) != len(nodes) {
			return false, fmt.Errorf("scan listed %v", keys)
		}
		return true, nil
	})
}
