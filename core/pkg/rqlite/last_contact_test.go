package rqlite

import (
	"encoding/json"
	"strings"
	"testing"
)

// rqlite reports store.raft.last_contact with a role-dependent JSON type: a
// duration string on a follower, a bare number on the leader. Declaring the
// field as a plain string made the LEADER's /status fail to decode, which took
// out the whole RQLiteStatus parse and silently disabled the local-read
// freshness gate on that node.
func TestRQLiteStatus_lastContact_acceptsFollowerString(t *testing.T) {
	body := `{"store":{"raft":{"state":"Follower","last_contact":"30.204191ms","commit_index":10,"applied_index":10}}}`

	var st RQLiteStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("unmarshal follower status: %v", err)
	}
	if got := st.Store.Raft.LastContact.String(); got != "30.204191ms" {
		t.Errorf("LastContact = %q; want 30.204191ms", got)
	}
}

func TestRQLiteStatus_lastContact_acceptsLeaderNumber(t *testing.T) {
	// The exact shape a live leader emits.
	body := `{"store":{"raft":{"state":"Leader","last_contact":0,"commit_index":10,"applied_index":10}}}`

	var st RQLiteStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("unmarshal leader status: %v — this is the bug: a number here aborted the whole decode", err)
	}
	if st.Store.Raft.State != "Leader" {
		t.Errorf("State = %q; want Leader", st.Store.Raft.State)
	}
	if got := st.Store.Raft.LastContact.String(); got != "0s" {
		t.Errorf("LastContact = %q; want 0s (nanoseconds rendered as a duration)", got)
	}
}

func TestRQLiteStatus_lastContact_acceptsNonZeroNumber(t *testing.T) {
	body := `{"store":{"raft":{"state":"Follower","last_contact":1500000}}}` // 1.5ms in ns

	var st RQLiteStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := st.Store.Raft.LastContact.String(); got != "1.5ms" {
		t.Errorf("LastContact = %q; want 1.5ms", got)
	}
}

func TestRQLiteStatus_lastContact_acceptsNeverAndNull(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"never", `{"store":{"raft":{"last_contact":"never"}}}`, "never"},
		{"null", `{"store":{"raft":{"last_contact":null}}}`, ""},
		{"absent", `{"store":{"raft":{"state":"Leader"}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var st RQLiteStatus
			if err := json.Unmarshal([]byte(tc.body), &st); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := st.Store.Raft.LastContact.String(); got != tc.want {
				t.Errorf("LastContact = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRQLiteStatus_lastContact_rejectsUnexpectedShape(t *testing.T) {
	var st RQLiteStatus
	err := json.Unmarshal([]byte(`{"store":{"raft":{"last_contact":{"nested":true}}}}`), &st)
	if err == nil {
		t.Fatal("an object last_contact should be rejected, not silently accepted")
	}
	if !strings.Contains(err.Error(), "last_contact") {
		t.Errorf("error %q should name the offending field", err)
	}
}

// The freshness gate's whole point: a Leader is always fresh enough to serve a
// local read. That branch was unreachable while decoding failed first.
func TestParseLastContact_leaderZeroIsFresh(t *testing.T) {
	if got := parseLastContact("0s"); got != 0 {
		t.Errorf("parseLastContact(%q) = %v; want 0", "0s", got)
	}
	if got := parseLastContact("never"); got != staleNeverContact {
		t.Errorf("parseLastContact(never) = %v; want staleNeverContact", got)
	}
	if got := parseLastContact("garbage"); got != staleNeverContact {
		t.Errorf("parseLastContact(garbage) = %v; want staleNeverContact (fail-safe)", got)
	}
}
