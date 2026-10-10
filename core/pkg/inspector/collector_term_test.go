package inspector

import "testing"

func TestParseRQLiteStatus_readsTheRaftTerm(t *testing.T) {
	s := parseRQLiteStatus(`{"store":{"raft":{"state":"Follower","term":21,"last_log_term":21,"applied_index":5}}}`)
	if s == nil || s.Term != 21 {
		t.Fatalf("parsed %+v, want term 21 from rqlite's \"term\" field", s)
	}
}
