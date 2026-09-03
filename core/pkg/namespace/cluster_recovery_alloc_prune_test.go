package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #280. pruneStaleClusterNodes (#173) removed a departed node's row from
// namespace_cluster_nodes but left its namespace_port_allocations row untouched —
// and the allocations table is what cluster-state.json, and therefore the namespace
// gateway's olric_servers and rqlite join list, are generated from. So a namespace
// kept naming removed nodes forever.
//
// On devnet anchat-test had five allocations, two on permanently-removed nodes.
// Olric discovery hit a dead node first and timed out: the namespace reported
// `olric: unavailable` on all three gateways, and every gateway restart stalled for
// minutes before binding — during a rolling upgrade that left a node serving nothing
// while DNS still sent it a third of the traffic.

// TestPruneStaleClusterNodes_alsoDropsPortAllocation is the fix: both rows go.
func TestPruneStaleClusterNodes_alsoDropsPortAllocation(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if query != staleClusterNodeSQL {
			t.Fatalf("unexpected query: %s", query)
		}
		appendToSlice(dest, map[string]any{"NodeID": "peer-gone"})
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	removed, err := cm.pruneStaleClusterNodes(context.Background(), "cluster-1")
	if err != nil {
		t.Fatalf("pruneStaleClusterNodes: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want 1", removed)
	}

	var sawMemberDelete, sawAllocDelete bool
	for _, ec := range db.getExecCalls() {
		if strings.Contains(ec.Query, "DELETE FROM namespace_cluster_nodes") {
			sawMemberDelete = true
		}
		if strings.Contains(ec.Query, "DELETE FROM namespace_port_allocations") {
			sawAllocDelete = true
			if len(ec.Args) < 2 || ec.Args[1] != "peer-gone" {
				t.Errorf("allocation delete targeted %v, want peer-gone", ec.Args)
			}
		}
	}
	if !sawMemberDelete {
		t.Error("namespace_cluster_nodes row was not deleted")
	}
	if !sawAllocDelete {
		t.Error("namespace_port_allocations row was NOT deleted — the departed node stays in olric_servers and the rqlite join list forever")
	}
}

// A cluster with no stale members must not delete anything.
func TestPruneStaleClusterNodes_noStaleMembersDeletesNoAllocations(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error { return nil }
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	if _, err := cm.pruneStaleClusterNodes(context.Background(), "cluster-1"); err != nil {
		t.Fatalf("pruneStaleClusterNodes: %v", err)
	}
	for _, ec := range db.getExecCalls() {
		if strings.Contains(ec.Query, "DELETE FROM namespace_port_allocations") {
			t.Errorf("deleted an allocation with no stale members: %+v", ec)
		}
	}
}

// TestRegenerateClusterState_usesOnlyLiveAllocations: after pruning, the rebuilt
// state must contain exactly the surviving nodes.
func TestRegenerateClusterState_usesOnlyLiveAllocations(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if !strings.Contains(query, "FROM namespace_port_allocations") {
			return nil
		}
		for _, n := range []struct{ id, ip string }{
			{"node-1", "10.0.0.1"},
			{"node-2", "10.0.0.2"},
			{"node-3", "10.0.0.17"},
		} {
			appendToSlice(dest, map[string]any{
				"NodeID": n.id, "InternalIP": n.ip, "IPAddress": "203.0.113.1",
				"RQLiteHTTPPort": 10000, "RQLiteRaftPort": 10001,
				"OlricHTTPPort": 10002, "OlricMemberlistPort": 10003,
				"GatewayHTTPPort": 10004,
			})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{ID: "cluster-1", NamespaceName: "anchat-test", RQLiteNodeCount: 3}

	nodes, blocks, err := cm.clusterStateInputs(context.Background(), cluster)
	if err != nil {
		t.Fatalf("clusterStateInputs: %v", err)
	}
	if len(nodes) != 3 || len(blocks) != 3 {
		t.Fatalf("got %d nodes / %d port blocks, want 3/3", len(nodes), len(blocks))
	}
	gotIPs := map[string]bool{}
	for i, n := range nodes {
		gotIPs[n.InternalIP] = true
		if blocks[i].NodeID != n.NodeID {
			t.Errorf("block %d is for %q but node is %q — nodes and port blocks must stay index-aligned", i, blocks[i].NodeID, n.NodeID)
		}
		if blocks[i].OlricHTTPPort != 10002 {
			t.Errorf("block %d OlricHTTPPort = %d, want 10002", i, blocks[i].OlricHTTPPort)
		}
	}
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.17"} {
		if !gotIPs[want] {
			t.Errorf("rebuilt membership is missing %s", want)
		}
	}
}

// TestRegenerateClusterState_errorsWhenNoAllocations: refuse to write an empty
// membership rather than silently producing a state file with no nodes.
func TestRegenerateClusterState_errorsWhenNoAllocations(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error { return nil }
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{ID: "cluster-1", NamespaceName: "ns", RQLiteNodeCount: 3}

	if _, _, err := cm.clusterStateInputs(context.Background(), cluster); err == nil {
		t.Error("clusterStateInputs succeeded with zero allocations — regeneration would write a membership-less state file")
	}
}
