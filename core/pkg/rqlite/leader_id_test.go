package rqlite

import (
	"encoding/json"
	"testing"
)

// rqlite 8.43 leaves store.raft.leader_id empty and reports the leader as
// store.leader. A gate that only reads the old field never sees a leader.
func TestRaftLeaderID_readsTheRqlite8Object(t *testing.T) {
	const body = `{"store":{"node_id":"self","addr":"10.0.0.1:10101","leader":{"node_id":"self","addr":"10.0.0.1:10101"},"raft":{"state":"Leader","applied_index":8,"commit_index":8}}}`
	var st RQLiteStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.RaftLeaderID() != "self" {
		t.Fatalf("RaftLeaderID = %q, want self", st.RaftLeaderID())
	}
}

func TestRaftLeaderID_keepsTheOlderField(t *testing.T) {
	const body = `{"store":{"raft":{"state":"Follower","leader_id":"other"}}}`
	var st RQLiteStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.RaftLeaderID() != "other" {
		t.Fatalf("RaftLeaderID = %q, want other", st.RaftLeaderID())
	}
}
