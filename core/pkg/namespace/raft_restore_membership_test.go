package namespace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

func peersAt(addrs ...string) []rqlite.RaftPeer {
	out := make([]rqlite.RaftPeer, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, rqlite.RaftPeer{ID: a, Address: a})
	}
	return out
}

// peers.json is force-recovery. It is written only when this node's recorded
// membership differs from the live one — not on every restart of a namespace
// that is live elsewhere, and not when nothing is recorded.
func TestNeedsPeersRecovery(t *testing.T) {
	live := peersAt("10.0.0.1:10200", "10.0.0.2:10200", "10.0.0.3:10200")
	same := &rqlite.ClusterMembership{Members: []string{"10.0.0.3:10200", "10.0.0.1:10200", "10.0.0.2:10200"}}
	stale := &rqlite.ClusterMembership{Members: []string{"10.0.0.1:10200", "10.0.0.2:10200", "10.0.0.9:10200"}}

	if needsPeersRecovery(nil, live) {
		t.Error("no record: the node's configuration is unknown and it must restart on it")
	}
	if needsPeersRecovery(same, live) {
		t.Error("a recorded membership equal to the live one forced a recovery")
	}
	if !needsPeersRecovery(stale, live) {
		t.Error("a stale membership (a removed member) was not recovered")
	}
	if needsPeersRecovery(stale, nil) {
		t.Error("an empty live membership would erase the configuration")
	}
}

var threeMembers = []restoreMember{
	{nodeID: "node-b", raftAddr: "10.0.0.2:10201"},
	{nodeID: "node-a", raftAddr: "10.0.0.1:10201"},
	{nodeID: "node-c", raftAddr: "10.0.0.3:10201"},
}

// A node that has been a member and lost its data joins the others — even
// when it has the lowest id, which used to make it bootstrap an empty cluster
// next to the members holding the data.
func TestRestoreJoinPlan_formerMemberWithoutStateJoins(t *testing.T) {
	record := &rqlite.ClusterMembership{Members: []string{"10.0.0.1:10201"}}
	join, leader, err := restoreJoinPlan("acme", "node-a", threeMembers, record, "/x/cluster-membership.json")
	if err != nil {
		t.Fatal(err)
	}
	if leader {
		t.Fatal("a former member without state bootstrapped")
	}
	if want := []string{"10.0.0.2:10201", "10.0.0.3:10201"}; !reflect.DeepEqual(join, want) {
		t.Errorf("join = %v, want %v", join, want)
	}
}

func TestRestoreJoinPlan_formerSoleMemberRefuses(t *testing.T) {
	record := &rqlite.ClusterMembership{}
	_, _, err := restoreJoinPlan("acme", "node-a", threeMembers[1:2], record, "/x/cluster-membership.json")
	if err == nil || !strings.Contains(err.Error(), "/x/cluster-membership.json") {
		t.Fatalf("err = %v, want a refusal naming the record", err)
	}
}

// Never a member: the deterministic election is unchanged.
func TestRestoreJoinPlan_newNodesElect(t *testing.T) {
	join, leader, err := restoreJoinPlan("acme", "node-a", threeMembers, nil, "")
	if err != nil || !leader || len(join) != 0 {
		t.Errorf("lowest id: join %v, leader %v, err %v", join, leader, err)
	}
	join, leader, err = restoreJoinPlan("acme", "node-c", threeMembers, nil, "")
	if err != nil || leader || !reflect.DeepEqual(join, []string{"10.0.0.1:10201"}) {
		t.Errorf("other id: join %v, leader %v, err %v", join, leader, err)
	}
}

// A torn record does not keep a node with raft state down; without state it
// is still evidence, and the node refuses.
func TestReadNamespaceMembership_tornRecord(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "acme", "rqlite", "node-a")
	path := rqlite.ClusterMembershipPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"first`), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec, err := readNamespaceMembership(dataDir, true); err != nil || rec != nil {
		t.Errorf("with state: %v, %v", rec, err)
	}
	if _, err := readNamespaceMembership(dataDir, false); err == nil {
		t.Error("without state a torn record was ignored")
	}
}

// End to end for a stopped member with state: the recorded membership decides
// whether peers.json is written.
func TestPlanNamespaceRQLiteStart_peersJSONOnlyWhenStale(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), localNodeID: "node-a"}
	live := peersAt("10.0.0.1:10201", "10.0.0.2:10201")
	for name, tc := range map[string]struct {
		recorded []string
		want     bool
	}{
		"no record":       {nil, false},
		"same membership": {[]string{"10.0.0.1:10201", "10.0.0.2:10201"}, false},
		"stale":           {[]string{"10.0.0.1:10201", "10.0.0.9:10201"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "acme", "rqlite", "node-a")
			if tc.recorded != nil {
				if err := rqlite.WriteClusterMembership(rqlite.ClusterMembershipPath(dataDir),
					rqlite.ClusterMembership{FirstSeen: time.Now(), Members: tc.recorded}); err != nil {
					t.Fatal(err)
				}
			}
			join, leader, err := cm.planNamespaceRQLiteStart("acme", dataDir, true, threeMembers, live)
			if err != nil || leader || len(join) != 0 {
				t.Fatalf("join %v, leader %v, err %v", join, leader, err)
			}
			_, statErr := os.Stat(filepath.Join(dataDir, "raft", "peers.json"))
			if wrote := statErr == nil; wrote != tc.want {
				t.Errorf("peers.json written = %v, want %v", wrote, tc.want)
			}
		})
	}
}
