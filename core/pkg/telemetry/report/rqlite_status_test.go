package report

import (
	"encoding/json"
	"testing"
)

// rqlite v10's /status names the raft term "term"; "current_term" never
// existed, so the report's term read 0 and the cluster term checks never ran.
const rqliteStatusV10 = `{"store":{"node_id":"n1","leader":{"addr":"10.0.0.1:10101","node_id":"n1"},
"raft":{"state":"Leader","term":21,"last_snapshot_term":21,"applied_index":636707,"commit_index":636707,"num_peers":4,"voter":true}}}`

func TestApplyRQLiteStatus_readsTheTermsRQLiteReports(t *testing.T) {
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(rqliteStatusV10), &status); err != nil {
		t.Fatal(err)
	}
	r := &RQLiteReport{}
	applyRQLiteStatus(r, status)
	if r.Term != 21 || r.LastSnapshotTerm != 21 {
		t.Fatalf("term %d, last snapshot term %d, want 21 and 21", r.Term, r.LastSnapshotTerm)
	}
	if r.Applied != 636707 || r.RaftState != "Leader" || r.NumPeers != 4 || !r.Voter {
		t.Fatalf("other fields not carried: %+v", r)
	}
}

func TestApplyRQLiteStatus_missingRaftLeavesZeroes(t *testing.T) {
	r := &RQLiteReport{}
	applyRQLiteStatus(r, map[string]interface{}{})
	if r.Term != 0 || r.LastSnapshotTerm != 0 || r.RaftState != "" {
		t.Fatalf("a status without raft gave %+v", r)
	}
}
