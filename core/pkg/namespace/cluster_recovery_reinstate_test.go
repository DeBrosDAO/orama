package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #279. A node that merely restarted was treated as permanently dead:
// ReplaceClusterNode committed the node to `failed` and the cluster to `degraded`,
// then looked for a replacement EXCLUDING every current member. On a cluster
// spanning the whole fleet there is no such node, so it returned an error — leaving
// the cluster wedged in `degraded` with a healthy node marked failed. Combined with
// bugboard #278 that 404'd the entire namespace at the edge until the rows were
// corrected by hand.
//
// These tests pin the two halves of the fix: a live node is reinstated rather than
// replaced, and a genuinely dead one still follows the replacement path.

func isLivenessQuery(q string) bool {
	return strings.Contains(q, "FROM dns_nodes WHERE id = ?") && strings.Contains(q, "last_seen >")
}

// TestNodeIsLive_trueForActiveHeartbeatingNode: the DB returning a row means alive.
func TestNodeIsLive_trueForActiveHeartbeatingNode(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if !isLivenessQuery(query) {
			t.Fatalf("unexpected query: %s", query)
		}
		appendToSlice(dest, map[string]any{"ID": "node-back"})
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	if !cm.nodeIsLive(context.Background(), "node-back") {
		t.Error("nodeIsLive = false for an active, heartbeating node")
	}
}

// TestNodeIsLive_falseForGoneNode: no row means gone.
func TestNodeIsLive_falseForGoneNode(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error { return nil }
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	if cm.nodeIsLive(context.Background(), "node-gone") {
		t.Error("nodeIsLive = true for a node with no active row")
	}
}

// TestNodeIsLive_falseWhenQueryFails: a transient DB error must not be read as
// "alive" — that would skip recovery for a genuinely dead node.
func TestNodeIsLive_falseWhenQueryFails(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		return context.DeadlineExceeded
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	if cm.nodeIsLive(context.Background(), "node-x") {
		t.Error("nodeIsLive = true when the liveness query failed — recovery would be skipped for a dead node")
	}
}

// TestReplaceClusterNode_reinstatesLiveNodeInsteadOfDegrading is the incident itself:
// the "dead" node is alive again, so the cluster must NOT be marked degraded and the
// node must NOT be marked failed.
func TestReplaceClusterNode_reinstatesLiveNodeInsteadOfDegrading(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if isLivenessQuery(query) {
			appendToSlice(dest, map[string]any{"ID": "node-restarted"})
			return nil
		}
		// getClusterNodes — all three members running after the reinstate.
		for _, id := range []string{"node-1", "node-2", "node-restarted"} {
			appendToSlice(dest, map[string]any{
				"NodeID": id, "Role": NodeRoleGateway, "Status": NodeStatusRunning,
			})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{ID: "cluster-1", NamespaceName: "anchat-test", RQLiteNodeCount: 3}

	if err := cm.ReplaceClusterNode(context.Background(), cluster, "node-restarted"); err != nil {
		t.Fatalf("ReplaceClusterNode: %v", err)
	}

	var sawFailed, sawDegraded, sawRunning, sawReady bool
	for _, ec := range db.getExecCalls() {
		for _, a := range ec.Args {
			s, ok := a.(string)
			if !ok {
				if ns, ok2 := a.(NodeStatus); ok2 {
					s = string(ns)
				} else if cs, ok3 := a.(ClusterStatus); ok3 {
					s = string(cs)
				} else {
					continue
				}
			}
			switch s {
			case string(NodeStatusFailed):
				sawFailed = true
			case string(ClusterStatusDegraded):
				sawDegraded = true
			case string(NodeStatusRunning):
				sawRunning = true
			case string(ClusterStatusReady):
				sawReady = true
			}
		}
	}

	if sawFailed {
		t.Error("node was marked failed even though it is alive again")
	}
	if sawDegraded {
		t.Error("cluster was marked degraded for a node that is alive — this is what wedged the cluster and 404'd the namespace")
	}
	if !sawRunning {
		t.Error("node was not reinstated to running")
	}
	if !sawReady {
		t.Error("cluster was not settled back to ready")
	}
}

// TestSettleClusterStatus_readyWhenAllMembersRunning: the stale-degraded clearing
// that RepairCluster used to skip on its early return.
func TestSettleClusterStatus_readyWhenAllMembersRunning(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		for _, id := range []string{"n1", "n2", "n3"} {
			appendToSlice(dest, map[string]any{
				"NodeID": id, "Role": NodeRoleGateway, "Status": NodeStatusRunning,
			})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{ID: "c", NamespaceName: "ns", RQLiteNodeCount: 3}

	if err := cm.settleClusterStatus(context.Background(), cluster); err != nil {
		t.Fatalf("settleClusterStatus: %v", err)
	}
	if !execArgsContain(db, string(ClusterStatusReady)) {
		t.Error("a fully-running cluster was not settled to ready — a stale 'degraded' would keep the namespace 404ing")
	}
}

// TestSettleClusterStatus_degradedWhenMembersMissing: don't paper over a real gap.
func TestSettleClusterStatus_degradedWhenMembersMissing(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		appendToSlice(dest, map[string]any{
			"NodeID": "n1", "Role": NodeRoleGateway, "Status": NodeStatusRunning,
		})
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{ID: "c", NamespaceName: "ns", RQLiteNodeCount: 3}

	if err := cm.settleClusterStatus(context.Background(), cluster); err != nil {
		t.Fatalf("settleClusterStatus: %v", err)
	}
	if !execArgsContain(db, string(ClusterStatusDegraded)) {
		t.Error("a cluster missing members should still be degraded")
	}
}

func execArgsContain(db *recoveryMockDB, want string) bool {
	for _, ec := range db.getExecCalls() {
		for _, a := range ec.Args {
			switch v := a.(type) {
			case string:
				if v == want {
					return true
				}
			case ClusterStatus:
				if string(v) == want {
					return true
				}
			case NodeStatus:
				if string(v) == want {
					return true
				}
			}
		}
	}
	return false
}
