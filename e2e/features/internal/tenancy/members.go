//go:build e2e_fleet

package tenancy

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	// NamespaceNodeCount is how many nodes host one namespace
	// (core/pkg/namespace DefaultRQLiteNodeCount): on a larger fleet the rest
	// hold none of its units, files or ports.
	NamespaceNodeCount = 3
	// membersBudget bounds the wait for the monitor to list every member.
	membersBudget = 2 * time.Minute
	// membersPoll paces that wait.
	membersPoll = 5 * time.Second
)

// ExpectedMembers is how many nodes host a namespace on a fleet of size nodes.
func ExpectedMembers(size int) int { return min(NamespaceNodeCount, size) }

// Members are the nodes namespace name is placed on, read from the nodes
// themselves (`orama monitor namespaces`: each node lists the namespaces it
// hosts), never assumed to be every node. It waits until the expected number
// of members report the namespace. Every per-namespace check on a node (units,
// files, ports, DNS records, faults) loops over Members; a check that
// something is gone loops over every node (f.State.Nodes) instead.
func Members(t testing.TB, f *fleet.Fleet, name string) []fleet.Node {
	t.Helper()
	return MembersOf(t, f, name)[name]
}

// MembersOf is Members for several namespaces from one read of the cluster,
// keyed by namespace name; each member list is sorted by node name.
func MembersOf(t testing.TB, f *fleet.Fleet, names ...string) map[string][]fleet.Node {
	t.Helper()
	want := ExpectedMembers(len(f.State.Nodes))
	var placed map[string][]fleet.Node
	eventually.Require(t, membersPoll, membersBudget, fmt.Sprintf("%d nodes to report each of %d namespaces", want, len(names)), func() (bool, error) {
		res, err := harness.CLI(t).Run(t.Context(), "status", "namespaces", "--env", f.State.Env, "--json")
		if err != nil {
			return false, err
		}
		if res.Exit != 0 {
			return false, fmt.Errorf("orama monitor namespaces exited %d: %s", res.Exit, res.Stderr)
		}
		var rows []monitor.NamespaceRow
		if err := oramacli.DecodeJSON(res, &rows); err != nil {
			return false, err
		}
		placed = map[string][]fleet.Node{}
		for _, name := range names {
			members, err := nodesOfHosts(f, name, monitor.HostsOf(rows, name))
			if err != nil {
				return false, eventually.Stop(err)
			}
			if len(members) < want {
				return false, fmt.Errorf("%d of %d members report %s", len(members), want, name)
			}
			placed[name] = members
		}
		return true, nil
	})
	return placed
}

// nodesOfHosts maps the hosts a namespace is reported on to the run's nodes,
// sorted by name.
func nodesOfHosts(f *fleet.Fleet, name string, hosts []string) ([]fleet.Node, error) {
	var members []fleet.Node
	for _, host := range hosts {
		node, ok := f.Lookup(host)
		if !ok {
			return nil, fmt.Errorf("the monitor lists %s hosting %s, which is no node of run %s", host, name, f.State.RunID)
		}
		members = append(members, node)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, nil
}
