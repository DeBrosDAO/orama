package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestReplaceClusterNode_evalN1DoesNotHuntForASpare(t *testing.T) {
	db := &recoveryMockDB{}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cluster := &NamespaceCluster{
		ID:              "c-eval",
		NamespaceName:   "solo",
		RQLiteNodeCount: 1,
		Status:          ClusterStatusReady,
	}

	err := cm.ReplaceClusterNode(context.Background(), cluster, "only-node")
	if err != ErrEvalClusterNoReplacement {
		t.Fatalf("ReplaceClusterNode N=1 = %v, want ErrEvalClusterNoReplacement", err)
	}

	for _, q := range db.getQueryCalls() {
		if strings.Contains(q.Query, "dns_nodes") && strings.Contains(q.Query, "last_seen") {
			continue // nodeIsLive
		}
		if strings.Contains(strings.ToLower(q.Query), "insert") {
			t.Errorf("N=1 replace must not insert a new member: %s", q.Query)
		}
	}
}

func TestRepairCluster_evalN1WithMemberUpDoesNotDemandThree(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_clusters") && strings.Contains(query, "namespace_name"):
			appendToSlice(dest, map[string]any{
				"ID":              "c-eval",
				"NamespaceName":   "solo",
				"Status":          ClusterStatusReady,
				"RQLiteNodeCount": 1,
			})
		case strings.Contains(query, "FROM namespace_cluster_nodes"):
			appendToSlice(dest, map[string]any{
				"NodeID": "only-node",
				"Status": NodeStatusRunning,
			})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop(), provisioning: map[string]bool{}}

	if err := cm.RepairCluster(context.Background(), "solo"); err != nil {
		t.Fatalf("RepairCluster N=1 with member running: %v", err)
	}

	for _, q := range db.getQueryCalls() {
		if strings.Contains(q.Query, "SelectReplacement") || strings.Contains(strings.ToLower(q.Query), "insert into namespace_cluster_nodes") {
			t.Errorf("repair of a healthy N=1 cluster must not add members: %s", q.Query)
		}
	}
}

func TestPruneStaleClusterNodes_skipsEvalN1(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "FROM namespace_clusters") {
			appendToSlice(dest, map[string]any{
				"ID":              "c-eval",
				"NamespaceName":   "solo",
				"RQLiteNodeCount": 1,
			})
			return nil
		}
		if query == staleClusterNodeSQL {
			t.Errorf("N=1 eval must not run the stale-member sweep")
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	removed, err := cm.pruneStaleClusterNodes(context.Background(), "c-eval")
	if err != nil {
		t.Fatalf("prune N=1: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("N=1 prune must not delete membership: %+v", db.getExecCalls())
	}
}
