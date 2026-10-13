package node

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
)

// clusterGraphGolden is the cluster role's start-up graph, one component per
// line with its dependencies. Adding the global role must not change it: a
// cluster node that boots a different graph after an upgrade is a regression
// even when every component still works. A deliberate change to the cluster
// graph edits this list in the same commit.
const clusterGraphGolden = `data-dir:
legacy-layout: data-dir
wireguard: legacy-layout
libp2p: legacy-layout,wireguard
peer-info: libp2p
monitoring: libp2p
pubsub: libp2p
ipfs-cluster-config: legacy-layout
storage: legacy-layout
storage-watch: storage
cluster-discovery: libp2p
rqlite-local: wireguard,cluster-discovery,storage
nameserver: rqlite-local
gateway: rqlite-local,storage,pubsub
edge-serving: gateway
edge-aux: edge-serving
wireguard-sync: wireguard,rqlite-local
ipfs-swarm-sync: storage,rqlite-local
rqlite-cluster: rqlite-local
membership-record: rqlite-cluster
membership: rqlite-cluster
dns-registration: rqlite-cluster,gateway,nameserver,edge-serving
`

func describeGraph(components []boot.Component) string {
	var b strings.Builder
	for _, c := range components {
		fmt.Fprintln(&b, strings.TrimSpace(c.Name+": "+strings.Join(c.DependsOn, ",")))
	}
	return b.String()
}

func TestBootComponents_clusterGraphIsTheGoldenGraph(t *testing.T) {
	for _, role := range []string{"", string(boot.RoleCluster)} {
		n := newGraphNode(t)
		n.config.Node.Role = role
		if got := describeGraph(mustBootComponents(t, n)); got != clusterGraphGolden {
			t.Errorf("role %q: the cluster graph changed:\n%s\nwant:\n%s", role, got, clusterGraphGolden)
		}
	}
}

func TestBootComponents_bothRunsTheGoldenClusterGraph(t *testing.T) {
	old := verifyNetnsLayout
	verifyNetnsLayout = func(string) error { return nil }
	t.Cleanup(func() { verifyNetnsLayout = old })

	n := newGraphNode(t)
	n.config.Node.Role = string(boot.RoleBoth)
	if got := describeGraph(mustBootComponents(t, n)); got != clusterGraphGolden {
		t.Errorf("a co-located node's graph differs from the cluster graph:\n%s", got)
	}
}

func TestBootComponents_globalGraphIsDataDirOnly(t *testing.T) {
	n := newGraphNode(t)
	n.config.Node.Role = string(boot.RoleGlobal)
	if got, want := describeGraph(mustBootComponents(t, n)), "data-dir:\n"; got != want {
		t.Errorf("global graph:\n%s\nwant:\n%s", got, want)
	}
}

// A global node has no rqlite-local or gateway, so the cluster's serving core
// can never be ready on it. It leaves joining when its own graph has converged.
func TestNextLifecycleState_globalNodeBecomesActiveWhenItsGraphConverges(t *testing.T) {
	n := newGraphNode(t)
	n.config.Node.Role = string(boot.RoleGlobal)
	sup := boot.New(nil, boot.Options{})
	if err := n.registerComponents(sup); err != nil {
		t.Fatal(err)
	}

	pending := sup.Snapshot()
	if want, change := nextLifecycleState(lifecycle.StateJoining, pending); change {
		t.Fatalf("a global node whose data directory is not ready left joining for %q", want)
	}

	ready := boot.Snapshot{Components: []boot.ComponentStatus{{Name: compDataDir, Status: boot.StatusReady}}}
	want, change := nextLifecycleState(lifecycle.StateJoining, ready)
	if !change || want != lifecycle.StateActive {
		t.Fatalf("a converged global node moved to %q (change=%v), want active", want, change)
	}
}
