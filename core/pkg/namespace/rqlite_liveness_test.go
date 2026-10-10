package namespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

func TestWritePeersJSON_clearsRecoveryLeftovers(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"recovery.db-wal", "recovery.db-shm"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cm := &ClusterManager{logger: zap.NewNop()}

	if err := cm.writePeersJSON(dir, []rqlite.RaftPeer{{ID: "10.0.0.1:10046", Address: "10.0.0.1:10046"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery.db-wal")); !os.IsNotExist(err) {
		t.Fatal("recovery.db-wal survived: rqlite would fail with 'existing WAL file present' and crash-loop")
	}
	if _, err := os.Stat(filepath.Join(dir, "raft", "peers.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDecideRQLiteLiveness(t *testing.T) {
	const me = "node-a"
	mine := rqliteDownMessage(me)
	cases := []struct {
		name   string
		down   int
		status ClusterStatus
		msg    string
		want   rqliteLivenessAction
	}{
		{"restarting briefly stays ready", rqliteDownSweepsBeforeDegraded - 1, ClusterStatusReady, "", rqliteLivenessNone},
		{"down for the threshold degrades", rqliteDownSweepsBeforeDegraded, ClusterStatusReady, "", rqliteLivenessDegrade},
		{"already reported by me", rqliteDownSweepsBeforeDegraded + 5, ClusterStatusDegraded, mine, rqliteLivenessNone},
		{"degraded for another reason is claimed", rqliteDownSweepsBeforeDegraded, ClusterStatusDegraded, "Node x is dead", rqliteLivenessDegrade},
		{"running again settles my report", 0, ClusterStatusDegraded, mine, rqliteLivenessSettle},
		{"running does not clear another node's report", 0, ClusterStatusDegraded, rqliteDownMessage("node-b"), rqliteLivenessNone},
		{"running and ready is nothing", 0, ClusterStatusReady, "", rqliteLivenessNone},
		{"failed cluster is not touched", rqliteDownSweepsBeforeDegraded, ClusterStatusFailed, "x", rqliteLivenessNone},
	}
	for _, c := range cases {
		if got, _ := decideRQLiteLiveness(c.down, c.status, c.msg, me, nil); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func livenessDB(status ClusterStatus, msg string) *recoveryMockDB {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_cluster_nodes cn"):
			appendToSlice(dest, map[string]any{"ClusterID": "c1", "NamespaceName": "acme"})
		case strings.Contains(query, "FROM namespace_clusters WHERE id"):
			appendToSlice(dest, map[string]any{"ID": "c1", "NamespaceName": "acme", "Status": status, "ErrorMessage": msg, "RQLiteNodeCount": 1})
		}
		return nil
	}
	return db
}

func statusWrites(db *recoveryMockDB) []string {
	var out []string
	for _, c := range db.execCalls {
		if strings.Contains(c.Query, "UPDATE namespace_clusters SET status") {
			out = append(out, string(c.Args[0].(ClusterStatus)))
		}
	}
	return out
}

func TestReconcileRQLiteLiveness_crashLoopingUnitDegradesReadyCluster(t *testing.T) {
	db := livenessDB(ClusterStatusReady, "")
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a"}
	down := func(string) (bool, bool) { return false, true }

	for i := 1; i < rqliteDownSweepsBeforeDegraded; i++ {
		if err := cm.reconcileRQLiteLiveness(context.Background(), down); err != nil {
			t.Fatal(err)
		}
	}
	if got := statusWrites(db); len(got) != 0 {
		t.Fatalf("status written before the threshold: %v", got)
	}
	if err := cm.reconcileRQLiteLiveness(context.Background(), down); err != nil {
		t.Fatal(err)
	}
	if got := statusWrites(db); len(got) != 1 || got[0] != "degraded" {
		t.Fatalf("status writes = %v, want one degraded", got)
	}
}

func TestReconcileRQLiteLiveness_unknownStateChangesNothing(t *testing.T) {
	db := livenessDB(ClusterStatusReady, "")
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a"}
	for i := 0; i < rqliteDownSweepsBeforeDegraded+2; i++ {
		if err := cm.reconcileRQLiteLiveness(context.Background(), func(string) (bool, bool) { return false, false }); err != nil {
			t.Fatal(err)
		}
	}
	if len(db.execCalls) != 0 {
		t.Fatalf("exec calls on an unknown unit state: %v", db.execCalls)
	}
}

func TestReconcileRQLiteLiveness_runningAgainSettles(t *testing.T) {
	db := livenessDB(ClusterStatusDegraded, rqliteDownMessage("node-a"))
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a"}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_cluster_nodes cn"):
			appendToSlice(dest, map[string]any{"ClusterID": "c1", "NamespaceName": "acme"})
		case strings.Contains(query, "FROM namespace_clusters WHERE id"):
			appendToSlice(dest, map[string]any{"ID": "c1", "Status": ClusterStatusDegraded, "ErrorMessage": rqliteDownMessage("node-a"), "RQLiteNodeCount": 1})
		case strings.Contains(query, "namespace_cluster_nodes"):
			appendToSlice(dest, map[string]any{"NodeID": "node-a", "Status": NodeStatusRunning})
		}
		return nil
	}
	if err := cm.reconcileRQLiteLiveness(context.Background(), func(string) (bool, bool) { return true, true }); err != nil {
		t.Fatal(err)
	}
	if got := statusWrites(db); len(got) != 1 || got[0] != "ready" {
		t.Fatalf("status writes = %v, want one ready", got)
	}
}

// Two nodes with a dead rqlite used to overwrite each other's message every
// sweep, and the first to recover settled the cluster to ready while the other
// was still down.
func TestDecideRQLiteLiveness_twoNodesDown(t *testing.T) {
	members := []string{"node-a", "node-b", "node-c"}
	bDown := rqliteDownMessage("node-b")

	action, msg := decideRQLiteLiveness(rqliteDownSweepsBeforeDegraded, ClusterStatusDegraded, bDown, "node-a", members)
	if action != rqliteLivenessDegrade || msg != rqliteDownMessage("node-a", "node-b") {
		t.Fatalf("a second node going down: got %v %q, want a degrade naming both", action, msg)
	}

	both := rqliteDownMessage("node-a", "node-b")
	action, msg = decideRQLiteLiveness(0, ClusterStatusDegraded, both, "node-a", members)
	if action != rqliteLivenessDegrade || msg != rqliteDownMessage("node-b") {
		t.Fatalf("one of two recovering: got %v %q, want node-b still reported, cluster not settled", action, msg)
	}

	action, _ = decideRQLiteLiveness(0, ClusterStatusDegraded, rqliteDownMessage("node-b"), "node-b", members)
	if action != rqliteLivenessSettle {
		t.Fatalf("the last node recovering: got %v, want settle", action)
	}

	action, _ = decideRQLiteLiveness(rqliteDownSweepsBeforeDegraded+3, ClusterStatusDegraded, both, "node-a", members)
	if action != rqliteLivenessNone {
		t.Fatalf("an unchanged report must not be written again, got %v", action)
	}
}

// A node that left the cluster can never clear its own entry.
func TestDecideRQLiteLiveness_aDepartedNodeIsNotWaitedFor(t *testing.T) {
	action, _ := decideRQLiteLiveness(0, ClusterStatusDegraded, rqliteDownMessage("gone"), "node-a", []string{"node-a", "node-b"})
	if action != rqliteLivenessSettle {
		t.Fatalf("got %v, want settle: the only node reported down is no longer a member", action)
	}
}

func TestReconcileRQLiteLiveness_oneBrokenClusterDoesNotStopTheOthers(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, args ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_cluster_nodes cn"):
			appendToSlice(dest, map[string]any{"ClusterID": "bad", "NamespaceName": "broken"})
			appendToSlice(dest, map[string]any{"ClusterID": "c1", "NamespaceName": "acme"})
		case strings.Contains(query, "FROM namespace_clusters WHERE id"):
			if args[0] == "bad" {
				return errors.New("db unavailable")
			}
			appendToSlice(dest, map[string]any{"ID": "c1", "NamespaceName": "acme", "Status": ClusterStatusReady, "RQLiteNodeCount": 1})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a"}
	down := func(string) (bool, bool) { return false, true }

	var err error
	for i := 0; i < rqliteDownSweepsBeforeDegraded; i++ {
		err = cm.reconcileRQLiteLiveness(context.Background(), down)
	}
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("err = %v, want the failing namespace reported", err)
	}
	if got := statusWrites(db); len(got) != 1 || got[0] != "degraded" {
		t.Fatalf("status writes = %v, want acme degraded despite the other cluster failing", got)
	}
}

func TestReconcileRQLiteLiveness_missingClusterIsAnErrorNotANilWrap(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "FROM namespace_cluster_nodes cn") {
			appendToSlice(dest, map[string]any{"ClusterID": "c1", "NamespaceName": "acme"})
		}
		return nil
	}
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a"}
	err := cm.reconcileRQLiteLiveness(context.Background(), func(string) (bool, bool) { return true, true })
	if err == nil || strings.Contains(err.Error(), "%!w") || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("err = %v, want a message naming the missing cluster", err)
	}
}

func TestReconcileRQLiteLiveness_forgetsStreaksOfClustersNoLongerHosted(t *testing.T) {
	db := livenessDB(ClusterStatusReady, "")
	cm := &ClusterManager{db: db, logger: zap.NewNop(), localNodeID: "node-a", rqliteDownSweeps: map[string]int{"departed": 2}}
	if err := cm.reconcileRQLiteLiveness(context.Background(), func(string) (bool, bool) { return true, true }); err != nil {
		t.Fatal(err)
	}
	if _, ok := cm.rqliteDownSweeps["departed"]; ok {
		t.Fatal("the streak of a cluster this node no longer hosts was kept")
	}
}
