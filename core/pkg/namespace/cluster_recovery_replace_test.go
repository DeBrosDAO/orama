package namespace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// replaceFixture is a cluster of three: a dead member, a survivor on an active
// node and one on a node that is no longer active, with one spare node to
// replace onto. Queries are answered by what they ask for.
type replaceFixture struct {
	db        *recoveryMockDB
	cm        *ClusterManager
	cluster   *NamespaceCluster
	raftCalls int
	deadIPs   []map[string]any
}

func newReplaceFixture(t *testing.T) *replaceFixture {
	t.Helper()
	f := &replaceFixture{db: &recoveryMockDB{}, cluster: &NamespaceCluster{ID: "c1", NamespaceName: "ns", RQLiteNodeCount: 3}}
	f.deadIPs = []map[string]any{{"InternalIP": "10.0.0.9", "IPAddress": "1.2.3.9"}}
	survivor := func(id, ip string) map[string]any {
		return map[string]any{"NodeID": id, "InternalIP": ip, "RQLiteHTTPPort": 10000, "RQLiteRaftPort": 10001}
	}
	f.db.queryFunc = func(dest any, q string, args ...any) error {
		switch {
		case isLivenessQuery(q):
		case strings.Contains(q, "FROM namespace_cluster_nodes WHERE namespace_cluster_id"):
			for _, id := range []string{"dead", "live", "gone"} {
				appendToSlice(dest, map[string]any{"NodeID": id, "Role": NodeRoleRQLiteFollower, "RQLiteRaftPort": 10001})
			}
		case strings.Contains(q, "last_seen >"):
			appendToSlice(dest, map[string]any{"NodeID": "spare", "IPAddress": "1.2.3.4", "InternalIP": "10.0.0.4"})
		case strings.Contains(q, "pa.rqlite_http_port"):
			rows := []map[string]any{survivor("live", "10.0.0.1"), survivor("gone", "10.0.0.2")}
			if strings.Contains(q, "dn.status = 'active'") {
				rows = rows[:1] // the query leaves out the member whose node is not active
			}
			for _, r := range rows {
				appendToSlice(dest, r)
			}
		case strings.Contains(q, "as internal_ip, ip_address FROM dns_nodes"):
			for _, r := range f.deadIPs {
				appendToSlice(dest, r)
			}
		}
		return nil
	}
	f.cm = &ClusterManager{
		db: f.db, logger: zap.NewNop(), localNodeID: "live", baseDataDir: t.TempDir(),
		portAllocator: NewNamespacePortAllocator(f.db, zap.NewNop()),
		raftRemoveFn:  func(context.Context, survivingNodePorts, string) error { f.raftCalls++; return nil },
	}
	f.cm.nodeSelector = NewClusterNodeSelector(f.db, f.cm.portAllocator, zap.NewNop())
	return f
}

func (f *replaceFixture) execs(substr string) int {
	n := 0
	for _, ec := range f.db.getExecCalls() {
		if strings.Contains(ec.Query, substr) {
			n++
		}
	}
	return n
}

func (f *replaceFixture) lastStatusMessage() string {
	msg := ""
	for _, ec := range f.db.getExecCalls() {
		if strings.Contains(ec.Query, "UPDATE namespace_clusters SET status = ?, error_message") {
			msg, _ = ec.Args[1].(string)
		}
	}
	return msg
}

// A second member whose node is not active is no voter: one voter would remain
// of three, so the removal cannot commit and must be refused. Counting it let
// guardRaftRemoval pass on a cluster whose quorum was already lost.
func TestReplaceClusterNode_secondDeadMemberIsNotASurvivingVoter(t *testing.T) {
	f := newReplaceFixture(t)
	err := f.cm.ReplaceClusterNode(context.Background(), f.cluster, "dead")
	if err == nil || !strings.Contains(err.Error(), "lost quorum") {
		t.Fatalf("ReplaceClusterNode = %v, want a refusal for lost quorum", err)
	}
	if f.raftCalls != 0 {
		t.Errorf("a raft removal was issued %d times on a cluster without quorum", f.raftCalls)
	}
}

// A failed raft removal gives the replacement's port block back and says the
// replacement was aborted, not that it is in progress.
func TestReplaceClusterNode_raftFailureRollsBackAndSaysAborted(t *testing.T) {
	f := newReplaceFixture(t)
	f.cm.raftRemoveFn = func(context.Context, survivingNodePorts, string) error {
		f.raftCalls++
		return errors.New("rqlite unreachable")
	}
	// Two active survivors keep quorum, so the removal itself is attempted.
	inner := f.db.queryFunc
	f.db.queryFunc = func(dest any, q string, args ...any) error {
		if strings.Contains(q, "pa.rqlite_http_port") {
			appendToSlice(dest, map[string]any{"NodeID": "live", "InternalIP": "10.0.0.1", "RQLiteHTTPPort": 10000, "RQLiteRaftPort": 10001})
			appendToSlice(dest, map[string]any{"NodeID": "gone", "InternalIP": "10.0.0.2", "RQLiteHTTPPort": 10000, "RQLiteRaftPort": 10001})
			return nil
		}
		return inner(dest, q, args...)
	}
	err := f.cm.ReplaceClusterNode(context.Background(), f.cluster, "dead")
	if err == nil || !strings.Contains(err.Error(), "from raft") {
		t.Fatalf("ReplaceClusterNode = %v, want the raft removal's failure", err)
	}
	if f.raftCalls == 0 {
		t.Fatal("the raft removal was never attempted")
	}
	if f.execs("DELETE FROM namespace_port_allocations") != 1 {
		t.Error("the replacement's port block was not given back")
	}
	if msg := f.lastStatusMessage(); !strings.Contains(msg, "aborted") || strings.Contains(msg, "in progress") {
		t.Errorf("cluster message %q, want it to say the replacement was aborted", msg)
	}
}

// A dead node with no dns_nodes row cannot be addressed in raft: the abort is
// named in the status, with the reason.
func TestReplaceClusterNode_unaddressableDeadNodeSaysWhyInStatus(t *testing.T) {
	f := newReplaceFixture(t)
	f.deadIPs = nil
	if err := f.cm.ReplaceClusterNode(context.Background(), f.cluster, "dead"); err == nil {
		t.Fatal("a dead node that cannot be addressed was replaced")
	}
	msg := f.lastStatusMessage()
	if !strings.Contains(msg, "aborted") || !strings.Contains(msg, "dead") || !strings.Contains(msg, "not found in dns_nodes") {
		t.Errorf("cluster message %q, want the abort and its reason", msg)
	}
	if f.execs("DELETE FROM namespace_port_allocations") != 1 {
		t.Error("the replacement's port block was not given back")
	}
}

// A dead node without an internal_ip would be addressed by its public IP,
// which raft removal must refuse: the status says so.
func TestReplaceClusterNode_deadNodeWithoutInternalIPSaysWhyInStatus(t *testing.T) {
	f := newReplaceFixture(t)
	f.deadIPs = []map[string]any{{"InternalIP": "1.2.3.9", "IPAddress": "1.2.3.9"}}
	if err := f.cm.ReplaceClusterNode(context.Background(), f.cluster, "dead"); err == nil {
		t.Fatal("a node without an overlay address was removed from raft")
	}
	if msg := f.lastStatusMessage(); !strings.Contains(msg, "aborted") || !strings.Contains(msg, "WireGuard overlay") {
		t.Errorf("cluster message %q, want the abort and the overlay reason", msg)
	}
}

// With no spare node to replace onto, the replacement stops before it starts:
// the status says it was aborted and why, not that recovery is in progress.
func TestReplaceClusterNode_noSpareNodeSaysAbortedInStatus(t *testing.T) {
	f := newReplaceFixture(t)
	query := f.db.queryFunc
	f.db.queryFunc = func(dest any, q string, args ...any) error {
		if strings.Contains(q, "last_seen >") {
			return nil // no node is free to take the dead one's place
		}
		return query(dest, q, args...)
	}
	if err := f.cm.ReplaceClusterNode(context.Background(), f.cluster, "dead"); err == nil {
		t.Fatal("a replacement with no spare node reported success")
	}
	if msg := f.lastStatusMessage(); !strings.Contains(msg, "aborted") || strings.Contains(msg, "in progress") {
		t.Errorf("cluster message %q, want it to say the replacement was aborted", msg)
	}
	if f.raftCalls != 0 {
		t.Errorf("raft removal ran %d times for a replacement that never started", f.raftCalls)
	}
}
