package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckRQLite_Unresponsive(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{Responsive: false}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)

	expectStatus(t, results, "rqlite.responsive", inspector.StatusFail)
	// Should return early — no raft_state check
	if findCheck(results, "rqlite.raft_state") != nil {
		t.Error("should not check raft_state when unresponsive")
	}
}

func TestCheckRQLite_HealthyLeader(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{
		Responsive: true,
		StrongRead: true,
		Readyz:     &inspector.RQLiteReadyz{Ready: true, Node: "ready", Leader: "ready", Store: "ready"},
		Status: &inspector.RQLiteStatus{
			RaftState:      "Leader",
			LeaderNodeID:   "node1",
			Voter:          true,
			NumPeers:       2,
			Term:           5,
			CommitIndex:    1000,
			AppliedIndex:   1000,
			FsmPending:     0,
			LastLogTerm:    5,
			DBAppliedIndex: 1000,
			FsmIndex:       1000,
			LastSnapshot:   995,
			DBSizeFriendly: "1.2MB",
			Goroutines:     50,
			HeapAlloc:      100 * 1024 * 1024, // 100MB
			Version:        "8.0.0",
		},
		Nodes: map[string]*inspector.RQLiteNode{
			"node1:5001": {Addr: "node1:5001", Reachable: true, Leader: true, Voter: true},
			"node2:5001": {Addr: "node2:5001", Reachable: true, Leader: false, Voter: true},
			"node3:5001": {Addr: "node3:5001", Reachable: true, Leader: false, Voter: true},
		},
		DebugVars: &inspector.RQLiteDebugVars{},
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)

	expectStatus(t, results, "rqlite.responsive", inspector.StatusPass)
	expectStatus(t, results, "rqlite.readyz", inspector.StatusPass)
	expectStatus(t, results, "rqlite.raft_state", inspector.StatusPass)
	expectStatus(t, results, "rqlite.leader_known", inspector.StatusPass)
	expectStatus(t, results, "rqlite.voter", inspector.StatusPass)
	expectStatus(t, results, "rqlite.commit_applied_gap", inspector.StatusPass)
	expectStatus(t, results, "rqlite.fsm_pending", inspector.StatusPass)
	expectStatus(t, results, "rqlite.db_fsm_sync", inspector.StatusPass)
	expectStatus(t, results, "rqlite.strong_read", inspector.StatusPass)
	expectStatus(t, results, "rqlite.all_reachable", inspector.StatusPass)
	expectStatus(t, results, "rqlite.goroutines", inspector.StatusPass)
	expectStatus(t, results, "rqlite.memory", inspector.StatusPass)
	expectStatus(t, results, "rqlite.query_errors", inspector.StatusPass)
	expectStatus(t, results, "rqlite.execute_errors", inspector.StatusPass)
	expectStatus(t, results, "rqlite.leader_not_found", inspector.StatusPass)
	expectStatus(t, results, "rqlite.snapshot_errors", inspector.StatusPass)
	expectStatus(t, results, "rqlite.client_health", inspector.StatusPass)
}

func TestCheckRQLite_RaftStates(t *testing.T) {
	tests := []struct {
		state  string
		status inspector.Status
	}{
		{"Leader", inspector.StatusPass},
		{"Follower", inspector.StatusPass},
		{"Candidate", inspector.StatusWarn},
		{"Shutdown", inspector.StatusFail},
		{"Unknown", inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.RQLite = &inspector.RQLiteData{
				Responsive: true,
				Status: &inspector.RQLiteStatus{
					RaftState:    tt.state,
					LeaderNodeID: "node1",
					Voter:        true,
				},
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckRQLite(data)
			expectStatus(t, results, "rqlite.raft_state", tt.status)
		})
	}
}

func TestCheckRQLite_ReadyzFail(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{
		Responsive: true,
		Readyz:     &inspector.RQLiteReadyz{Ready: false, Node: "ready", Leader: "not ready", Store: "ready"},
		Status:     &inspector.RQLiteStatus{RaftState: "Follower", LeaderNodeID: "n1", Voter: true},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.readyz", inspector.StatusFail)
}

func TestCheckRQLite_CommitAppliedGap(t *testing.T) {
	tests := []struct {
		name    string
		commit  uint64
		applied uint64
		status  inspector.Status
	}{
		{"no gap", 1000, 1000, inspector.StatusPass},
		{"small gap", 1002, 1000, inspector.StatusPass},
		{"lagging", 1050, 1000, inspector.StatusWarn},
		{"severely behind", 2000, 1000, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.RQLite = &inspector.RQLiteData{
				Responsive: true,
				Status: &inspector.RQLiteStatus{
					RaftState:    "Follower",
					LeaderNodeID: "n1",
					Voter:        true,
					CommitIndex:  tt.commit,
					AppliedIndex: tt.applied,
				},
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckRQLite(data)
			expectStatus(t, results, "rqlite.commit_applied_gap", tt.status)
		})
	}
}

func TestCheckRQLite_FsmPending(t *testing.T) {
	tests := []struct {
		name    string
		pending uint64
		status  inspector.Status
	}{
		{"zero", 0, inspector.StatusPass},
		{"small", 5, inspector.StatusWarn},
		{"backlog", 100, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.RQLite = &inspector.RQLiteData{
				Responsive: true,
				Status: &inspector.RQLiteStatus{
					RaftState:    "Follower",
					LeaderNodeID: "n1",
					Voter:        true,
					FsmPending:   tt.pending,
				},
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckRQLite(data)
			expectStatus(t, results, "rqlite.fsm_pending", tt.status)
		})
	}
}

func TestCheckRQLite_StrongReadFail(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{
		Responsive: true,
		StrongRead: false,
		Status:     &inspector.RQLiteStatus{RaftState: "Follower", LeaderNodeID: "n1", Voter: true},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.strong_read", inspector.StatusFail)
}

func TestCheckRQLite_DebugVarsErrors(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{
		Responsive: true,
		Status:     &inspector.RQLiteStatus{RaftState: "Leader", LeaderNodeID: "n1", Voter: true},
		DebugVars: &inspector.RQLiteDebugVars{
			QueryErrors:    5,
			ExecuteErrors:  3,
			LeaderNotFound: 1,
			SnapshotErrors: 2,
			ClientRetries:  10,
			ClientTimeouts: 1,
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)

	expectStatus(t, results, "rqlite.query_errors", inspector.StatusWarn)
	expectStatus(t, results, "rqlite.execute_errors", inspector.StatusWarn)
	expectStatus(t, results, "rqlite.leader_not_found", inspector.StatusFail)
	expectStatus(t, results, "rqlite.snapshot_errors", inspector.StatusFail)
	expectStatus(t, results, "rqlite.client_health", inspector.StatusWarn)
}

func TestCheckRQLite_Goroutines(t *testing.T) {
	tests := []struct {
		name       string
		goroutines int
		status     inspector.Status
	}{
		{"healthy", 50, inspector.StatusPass},
		{"elevated", 500, inspector.StatusWarn},
		{"high", 2000, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.RQLite = &inspector.RQLiteData{
				Responsive: true,
				Status: &inspector.RQLiteStatus{
					RaftState:    "Leader",
					LeaderNodeID: "n1",
					Voter:        true,
					Goroutines:   tt.goroutines,
				},
			}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckRQLite(data)
			expectStatus(t, results, "rqlite.goroutines", tt.status)
		})
	}
}

// --- Cross-node tests ---

func makeRQLiteCluster(leaderHost string, states map[string]string, term uint64) *inspector.ClusterData {
	nodes := map[string]*inspector.NodeData{}
	rqliteNodes := map[string]*inspector.RQLiteNode{}
	for host := range states {
		rqliteNodes[host+":5001"] = &inspector.RQLiteNode{
			Addr: host + ":5001", Reachable: true, Voter: true,
			Leader: states[host] == "Leader",
		}
	}

	for host, state := range states {
		nd := makeNodeData(host, "node")
		nd.RQLite = &inspector.RQLiteData{
			Responsive: true,
			Status: &inspector.RQLiteStatus{
				RaftState:    state,
				LeaderNodeID: leaderHost,
				Voter:        true,
				Term:         term,
				AppliedIndex: 1000,
				CommitIndex:  1000,
				Version:      "8.0.0",
				DBSize:       4096,
			},
			Nodes: rqliteNodes,
		}
		nodes[host] = nd
	}
	return makeCluster(nodes)
}

func TestCheckRQLite_CrossNode_SingleLeader(t *testing.T) {
	data := makeRQLiteCluster("1.1.1.1", map[string]string{
		"1.1.1.1": "Leader",
		"2.2.2.2": "Follower",
		"3.3.3.3": "Follower",
	}, 5)

	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.single_leader", inspector.StatusPass)
	expectStatus(t, results, "rqlite.term_consistent", inspector.StatusPass)
	expectStatus(t, results, "rqlite.leader_agreement", inspector.StatusPass)
	expectStatus(t, results, "rqlite.index_convergence", inspector.StatusPass)
	expectStatus(t, results, "rqlite.version_consistent", inspector.StatusPass)
	expectStatus(t, results, "rqlite.quorum", inspector.StatusPass)
}

func TestCheckRQLite_CrossNode_NoLeader(t *testing.T) {
	data := makeRQLiteCluster("", map[string]string{
		"1.1.1.1": "Candidate",
		"2.2.2.2": "Candidate",
		"3.3.3.3": "Candidate",
	}, 5)
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.single_leader", inspector.StatusFail)
}

func TestCheckRQLite_CrossNode_SplitBrain(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		state := "Follower"
		leaderID := "1.1.1.1"
		if host == "1.1.1.1" || host == "2.2.2.2" {
			state = "Leader"
			leaderID = host
		}
		nd.RQLite = &inspector.RQLiteData{
			Responsive: true,
			Status: &inspector.RQLiteStatus{
				RaftState:    state,
				LeaderNodeID: leaderID,
				Voter:        true,
				Term:         5,
				AppliedIndex: 1000,
			},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.single_leader", inspector.StatusFail)
}

func TestCheckRQLite_CrossNode_TermDivergence(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	terms := map[string]uint64{"1.1.1.1": 5, "2.2.2.2": 5, "3.3.3.3": 6}
	for host, term := range terms {
		nd := makeNodeData(host, "node")
		nd.RQLite = &inspector.RQLiteData{
			Responsive: true,
			Status: &inspector.RQLiteStatus{
				RaftState:    "Follower",
				LeaderNodeID: "1.1.1.1",
				Voter:        true,
				Term:         term,
				AppliedIndex: 1000,
			},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.term_consistent", inspector.StatusFail)
}

func TestCheckRQLite_CrossNode_IndexLagging(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	applied := map[string]uint64{"1.1.1.1": 1000, "2.2.2.2": 1000, "3.3.3.3": 500}
	for host, idx := range applied {
		nd := makeNodeData(host, "node")
		state := "Follower"
		if host == "1.1.1.1" {
			state = "Leader"
		}
		nd.RQLite = &inspector.RQLiteData{
			Responsive: true,
			Status: &inspector.RQLiteStatus{
				RaftState:    state,
				LeaderNodeID: "1.1.1.1",
				Voter:        true,
				Term:         5,
				AppliedIndex: idx,
				CommitIndex:  idx,
			},
		}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.index_convergence", inspector.StatusWarn)
}

func TestCheckRQLite_CrossNode_SkipSingleNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.RQLite = &inspector.RQLiteData{
		Responsive: true,
		Status:     &inspector.RQLiteStatus{RaftState: "Leader", LeaderNodeID: "n1", Voter: true, Term: 5, AppliedIndex: 1000},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)
	expectStatus(t, results, "rqlite.cross_node", inspector.StatusSkip)
}

func TestCheckRQLite_NilRQLiteData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	// nd.RQLite is nil — no per-node checks, but cross-node skip is expected
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckRQLite(data)
	// Should only have the cross-node skip (not enough nodes)
	for _, r := range results {
		if r.Status != inspector.StatusSkip {
			t.Errorf("unexpected non-skip result: %s (status=%s)", r.ID, r.Status)
		}
	}
}
