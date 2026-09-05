package lifecycle

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

func statusOf(state string, voter bool) *rqlite.RQLiteStatus {
	s := &rqlite.RQLiteStatus{}
	s.Store.Raft.State = state
	s.Store.Raft.Voter = voter
	return s
}

func voters(reachable, unreachable int) []rqliteNode {
	var out []rqliteNode
	for i := 0; i < reachable; i++ {
		out = append(out, rqliteNode{Voter: true, Reachable: true})
	}
	for i := 0; i < unreachable; i++ {
		out = append(out, rqliteNode{Voter: true, Reachable: false})
	}
	return out
}

// The guard exists to refuse a stop that would cost the cluster its quorum, and
// to refuse just as firmly when it cannot tell. Returning "" means "go ahead".
func TestEvaluateQuorumSafety(t *testing.T) {
	cases := []struct {
		name      string
		in        quorumInputs
		wantAllow bool
		wantText  string
	}{
		{
			name:      "healthy 3-voter cluster, this node a follower",
			in:        quorumInputs{status: statusOf("Follower", true), nodes: voters(3, 0)},
			wantAllow: true,
		},
		{
			name:      "3 voters but one already unreachable",
			in:        quorumInputs{status: statusOf("Follower", true), nodes: voters(2, 1)},
			wantAllow: false,
			wantText:  "would break RQLite quorum",
		},
		{
			name:      "leader of a healthy 3-voter cluster may still stop",
			in:        quorumInputs{status: statusOf("Leader", true), nodes: voters(3, 0)},
			wantAllow: true,
		},
		{
			name:      "leader of a 2-voter cluster may not",
			in:        quorumInputs{status: statusOf("Leader", true), nodes: voters(2, 0)},
			wantAllow: false,
			wantText:  "the LEADER",
		},
		{
			name:      "non-voter is always safe",
			in:        quorumInputs{status: statusOf("Follower", false)},
			wantAllow: true,
		},
		{
			// The regression this ticket is about: an unreadable status used to
			// return "safe".
			name:      "status unreadable while rqlited is running",
			in:        quorumInputs{statusErr: fmt.Errorf("connection reset"), rqliteRunning: true},
			wantAllow: false,
			wantText:  "Cannot verify quorum safety",
		},
		{
			name:      "status unreadable because rqlited is not running",
			in:        quorumInputs{statusErr: fmt.Errorf("connection refused"), rqliteRunning: false},
			wantAllow: true,
		},
		{
			name:      "voter but member list unreadable",
			in:        quorumInputs{status: statusOf("Follower", true), nodesErr: fmt.Errorf("timeout")},
			wantAllow: false,
			wantText:  "cluster member list could not be read",
		},
		{
			name:      "voter but member list reports no voters",
			in:        quorumInputs{status: statusOf("Follower", true), nodes: []rqliteNode{{Voter: false, Reachable: true}}},
			wantAllow: false,
			wantText:  "no voters at all",
		},
		{
			name:      "single-voter cluster: stopping it loses everything",
			in:        quorumInputs{status: statusOf("Leader", true), nodes: voters(1, 0)},
			wantAllow: false,
			wantText:  "would break RQLite quorum",
		},
		{
			name:      "5 voters, one already down, still safe",
			in:        quorumInputs{status: statusOf("Follower", true), nodes: voters(4, 1)},
			wantAllow: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateQuorumSafety(tc.in)
			if tc.wantAllow && got != "" {
				t.Fatalf("expected the stop to be allowed, got refusal: %s", got)
			}
			if !tc.wantAllow {
				if got == "" {
					t.Fatal("expected a refusal, the stop was allowed")
				}
				if tc.wantText != "" && !strings.Contains(got, tc.wantText) {
					t.Errorf("refusal = %q, want it to mention %q", got, tc.wantText)
				}
			}
		})
	}
}

// Non-voters must not inflate the voter total, or the quorum arithmetic is
// wrong in the dangerous direction.
func TestCountVotersIgnoresNonVoters(t *testing.T) {
	nodes := []rqliteNode{
		{Voter: true, Reachable: true},
		{Voter: true, Reachable: false},
		{Voter: false, Reachable: true},
		{Voter: false, Reachable: false},
	}
	reachable, total := countVoters(nodes)
	if reachable != 1 || total != 2 {
		t.Errorf("countVoters = (%d reachable, %d total), want (1, 2)", reachable, total)
	}
}

// The readers must parse what rqlite actually returns, and treat a non-200 as
// an error rather than as empty data.
func TestLocalReadersAgainstFakeRQLite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"store": map[string]any{"raft": map[string]any{"state": "Leader", "voter": true}},
			})
		case strings.HasPrefix(r.URL.Path, "/nodes"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"10.0.0.1:10101": map[string]any{"voter": true, "reachable": true},
				"10.0.0.2:10101": map[string]any{"voter": true, "reachable": false},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	orig := rqliteBaseURL
	rqliteBaseURL = func() string { return srv.URL }
	defer func() { rqliteBaseURL = orig }()

	status, err := localRQLiteStatus()
	if err != nil {
		t.Fatalf("localRQLiteStatus: %v", err)
	}
	if status.Store.Raft.State != "Leader" || !status.Store.Raft.Voter {
		t.Errorf("status = %+v, want Leader voter", status.Store.Raft)
	}

	nodes, err := localRQLiteNodes()
	if err != nil {
		t.Fatalf("localRQLiteNodes: %v", err)
	}
	reachable, total := countVoters(nodes)
	if reachable != 1 || total != 2 {
		t.Errorf("countVoters = (%d, %d), want (1, 2)", reachable, total)
	}
}

func TestQuorumGetTreatsNon200AsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	orig := rqliteBaseURL
	rqliteBaseURL = func() string { return srv.URL }
	defer func() { rqliteBaseURL = orig }()

	if _, err := localRQLiteStatus(); err == nil {
		t.Fatal("a 503 status response was accepted as a valid reading")
	}
}

// End to end through checkQuorumSafety: an unreachable RQLite that systemd
// reports as active must refuse.
func TestCheckQuorumSafetyRefusesWhenUnreadableButRunning(t *testing.T) {
	origURL := rqliteBaseURL
	origActive := serviceActive
	// A port nothing listens on.
	rqliteBaseURL = func() string { return "http://127.0.0.1:1" }
	serviceActive = func(string) (bool, error) { return true, nil }
	defer func() { rqliteBaseURL = origURL; serviceActive = origActive }()

	if got := checkQuorumSafety(); got == "" {
		t.Fatal("unreachable-but-running RQLite was reported safe to stop")
	}
}

func TestCheckQuorumSafetyAllowsWhenRQLiteStopped(t *testing.T) {
	origURL := rqliteBaseURL
	origActive := serviceActive
	rqliteBaseURL = func() string { return "http://127.0.0.1:1" }
	serviceActive = func(string) (bool, error) { return false, nil }
	defer func() { rqliteBaseURL = origURL; serviceActive = origActive }()

	if got := checkQuorumSafety(); got != "" {
		t.Fatalf("stopped RQLite should be safe to stop, got refusal: %s", got)
	}
}
