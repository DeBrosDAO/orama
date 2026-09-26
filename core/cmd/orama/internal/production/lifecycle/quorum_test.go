package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
		// rqlited always runs with -auth.
		if u, p, ok := r.BasicAuth(); !ok || u != testRQLiteUser || p != testRQLitePass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
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

	useRQLite(t, strings.TrimPrefix(srv.URL, "http://"))

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

	useRQLite(t, strings.TrimPrefix(srv.URL, "http://"))

	if _, err := localRQLiteStatus(); err == nil {
		t.Fatal("a 503 status response was accepted as a valid reading")
	}
}

// End to end through checkQuorumSafety: an unreachable RQLite that systemd
// reports as active must refuse.
func TestCheckQuorumSafetyRefusesWhenUnreadableButRunning(t *testing.T) {
	origActive := serviceActive
	// A port nothing listens on.
	useRQLite(t, "127.0.0.1:1")
	serviceActive = func(string) (bool, error) { return true, nil }
	defer func() { serviceActive = origActive }()

	if got := checkQuorumSafety(); got == "" {
		t.Fatal("unreachable-but-running RQLite was reported safe to stop")
	}
}

func TestCheckQuorumSafetyAllowsWhenRQLiteStopped(t *testing.T) {
	origActive := serviceActive
	useRQLite(t, "127.0.0.1:1")
	serviceActive = func(string) (bool, error) { return false, nil }
	defer func() { serviceActive = origActive }()

	if got := checkQuorumSafety(); got != "" {
		t.Fatalf("stopped RQLite should be safe to stop, got refusal: %s", got)
	}
}

const (
	testRQLiteUser = "orama"
	testRQLitePass = "0123456789abcdef"
)

// useRQLite points the quorum readers at hostPort with the test credentials
// for the duration of the test.
func useRQLite(t *testing.T, hostPort string) {
	t.Helper()
	ep, err := rqlite.NewEndpoint(hostPort, testRQLiteUser, testRQLitePass)
	if err != nil {
		t.Fatal(err)
	}
	orig := localRQLite
	localRQLite = func() (rqlite.Endpoint, error) { return ep, nil }
	t.Cleanup(func() { localRQLite = orig })
}

// A node whose node.yaml cannot be resolved (no address or credentials) has an
// unknown quorum position; with rqlited running that must refuse, not approve.
func TestCheckQuorumSafetyRefusesWhenEndpointUnresolvable(t *testing.T) {
	origEP, origActive := localRQLite, serviceActive
	localRQLite = func() (rqlite.Endpoint, error) {
		return rqlite.Endpoint{}, errors.New("node config /opt/orama/.orama/configs/node.yaml: rqlite advertise address is empty")
	}
	serviceActive = func(string) (bool, error) { return true, nil }
	defer func() { localRQLite, serviceActive = origEP, origActive }()

	got := checkQuorumSafety()
	if got == "" {
		t.Fatal("an unresolvable rqlite endpoint was reported safe to stop")
	}
	if !strings.Contains(got, "advertise address is empty") {
		t.Errorf("refusal %q does not carry the reason", got)
	}
}

// On 0.122.x the index rqlited was orama-node's child, not
// orama-namespace-rqlite@index. The upgrade from 0.122.x runs the hand-over on
// such a node (no rqlite template installed), so an unreadable status with
// orama-node active is "may be running" — refused — and only neither unit
// active is "not running".
func TestQuorumVerdict_legacySupervisorCountsOnTheLegacyLayout(t *testing.T) {
	origEP, origActive, origStat := localRQLite, serviceActive, statUnit
	localRQLite = func() (rqlite.Endpoint, error) { return rqlite.Endpoint{}, fmt.Errorf("no node.yaml") }
	statUnit = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() { localRQLite, serviceActive, statUnit = origEP, origActive, origStat }()

	serviceActive = func(unit string) (bool, error) { return unit == "orama-node", nil }
	if warning, running := quorumVerdict(); warning == "" || !running {
		t.Errorf("orama-node active: warning %q, running %v; want a refusal", warning, running)
	}

	serviceActive = func(string) (bool, error) { return false, nil }
	if warning, running := quorumVerdict(); warning != "" || running {
		t.Errorf("nothing active: warning %q, running %v; want safe and not running", warning, running)
	}

	serviceActive = func(string) (bool, error) { return false, fmt.Errorf("dbus down") }
	if _, running := quorumVerdict(); !running {
		t.Error("an unreadable systemd was taken for a stopped rqlite")
	}
}

// On this release orama-node is always active and never runs rqlited: a node
// whose rqlite@index is down stays stoppable (and restartable, and upgradable)
// without --force.
func TestQuorumVerdict_currentLayoutIgnoresTheSupervisor(t *testing.T) {
	origEP, origActive, origStat := localRQLite, serviceActive, statUnit
	localRQLite = func() (rqlite.Endpoint, error) { return rqlite.Endpoint{}, fmt.Errorf("connection refused") }
	statUnit = func(string) (os.FileInfo, error) { return nil, nil }
	serviceActive = func(unit string) (bool, error) { return unit == "orama-node", nil }
	defer func() { localRQLite, serviceActive, statUnit = origEP, origActive, origStat }()

	if warning, running := quorumVerdict(); warning != "" || running {
		t.Errorf("rqlite@index down on this release: warning %q, running %v", warning, running)
	}
}
