package nodehealth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// statusJSON renders an rqlite /status body with the fields this package reads.
func statusJSON(state, leaderID string, applied, commit uint64) string {
	return fmt.Sprintf(`{"store":{"raft":{"state":%q,"leader_id":%q,"applied_index":%d,"commit_index":%d}}}`,
		state, leaderID, applied, commit)
}

// newNode serves an rqlite /status and a gateway /health from one test server.
func newNode(t *testing.T, body func() string, gatewayCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(body()))
		case "/health":
			w.WriteHeader(gatewayCode)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func target(srv *httptest.Server) Target {
	return Target{RQLiteBase: srv.URL, GatewayBase: srv.URL}
}

func TestStatus_Ready_healthy(t *testing.T) {
	s := Status{RaftState: "Follower", LeaderID: "n1", AppliedIndex: 100, CommitIndex: 100, GatewayOK: true}
	if err := s.Ready(Options{RequireLeaderKnown: true}); err != nil {
		t.Fatalf("a healthy follower was rejected: %v", err)
	}
}

// Candidate is the state a node sits in while an election is running. Treating
// it as ready is what let a rollout restart the next voter mid-election.
func TestStatus_Ready_rejects_candidate(t *testing.T) {
	s := Status{RaftState: "Candidate", GatewayOK: true}
	err := s.Ready(Options{})
	if err == nil {
		t.Fatal("Candidate reported ready")
	}
	if !strings.Contains(err.Error(), "Candidate") {
		t.Fatalf("error does not name the state: %v", err)
	}
}

// A Follower that trails the leader by tens of thousands of entries is not
// carrying reads. Restarting the next voter while it catches up leaves one
// usable copy of the data.
func TestStatus_Ready_rejects_index_lag(t *testing.T) {
	s := Status{RaftState: "Follower", LeaderID: "n1", AppliedIndex: 1_000, CommitIndex: 41_000, GatewayOK: true}
	err := s.Ready(Options{})
	if err == nil {
		t.Fatal("a follower 40000 entries behind reported ready")
	}
	if !strings.Contains(err.Error(), "40000") {
		t.Fatalf("error does not quantify the lag: %v", err)
	}
}

func TestStatus_Ready_allows_lag_within_limit(t *testing.T) {
	s := Status{RaftState: "Follower", LeaderID: "n1", AppliedIndex: 1_000, CommitIndex: 1_050, GatewayOK: true}
	if err := s.Ready(Options{MaxIndexLag: 100}); err != nil {
		t.Fatalf("a 50-entry lag under a 100 limit was rejected: %v", err)
	}
}

// A node can report Follower with no leader while the cluster has no quorum.
// For a rollout gate that must fail: continuing is the precise mistake.
func TestStatus_Ready_no_leader_when_required(t *testing.T) {
	s := Status{RaftState: "Follower", LeaderID: "", AppliedIndex: 5, CommitIndex: 5, GatewayOK: true}

	if err := s.Ready(Options{}); err != nil {
		t.Fatalf("without RequireLeaderKnown this should pass: %v", err)
	}
	err := s.Ready(Options{RequireLeaderKnown: true})
	if err == nil {
		t.Fatal("a leaderless node passed a gate that requires a leader")
	}
	if !strings.Contains(err.Error(), "quorum") {
		t.Fatalf("error does not explain the consequence: %v", err)
	}
}

func TestStatus_Ready_rejects_dead_gateway(t *testing.T) {
	s := Status{RaftState: "Leader", LeaderID: "n1", AppliedIndex: 9, CommitIndex: 9}
	err := s.Ready(Options{})
	if err == nil {
		t.Fatal("a node with a dead gateway reported ready")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("error does not name the gateway: %v", err)
	}
}

func TestObserve_reads_all_fields(t *testing.T) {
	srv := newNode(t, func() string { return statusJSON("Leader", "n1", 42, 44) }, 200)

	got, err := Observe(context.Background(), &http.Client{Timeout: time.Second}, target(srv))
	if err != nil {
		t.Fatal(err)
	}
	want := Status{RaftState: "Leader", LeaderID: "n1", AppliedIndex: 42, CommitIndex: 44, GatewayOK: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// An empty GatewayBase means "this node has no gateway to check", which is a
// caller's explicit statement — not a silent skip when the gateway is down.
func TestObserve_no_gateway_base_skips_the_check(t *testing.T) {
	srv := newNode(t, func() string { return statusJSON("Follower", "n1", 1, 1) }, 500)

	got, err := Observe(context.Background(), &http.Client{Timeout: time.Second},
		Target{RQLiteBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !got.GatewayOK {
		t.Fatal("an unset GatewayBase should not fail the gateway check")
	}
}

func TestObserve_gateway_non_200_is_not_ok(t *testing.T) {
	srv := newNode(t, func() string { return statusJSON("Follower", "n1", 1, 1) }, 503)

	got, err := Observe(context.Background(), &http.Client{Timeout: time.Second}, target(srv))
	if err != nil {
		t.Fatal(err)
	}
	if got.GatewayOK {
		t.Fatal("a 503 from /health was read as healthy")
	}
}

func TestWaitReady_succeeds_once_the_node_settles(t *testing.T) {
	var polls atomic.Int32
	srv := newNode(t, func() string {
		if polls.Add(1) < 3 {
			return statusJSON("Candidate", "", 0, 0)
		}
		return statusJSON("Follower", "n1", 10, 10)
	}, 200)

	if err := WaitReady(context.Background(), target(srv), Options{Budget: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if polls.Load() < 3 {
		t.Fatalf("returned after %d polls; it should have waited", polls.Load())
	}
}

// The timeout must carry the last observation, not the word "timeout". "Still
// Candidate" says what to do next; "timed out" does not.
func TestWaitReady_timeout_reports_the_last_observation(t *testing.T) {
	srv := newNode(t, func() string { return statusJSON("Candidate", "", 0, 0) }, 200)

	err := WaitReady(context.Background(), target(srv), Options{Budget: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("a node stuck in Candidate reported ready")
	}
	if !strings.Contains(err.Error(), "Candidate") {
		t.Fatalf("timeout error dropped the observation: %v", err)
	}
}

// An unreachable node is not ready. The gate this replaces queried a port
// nothing listened on, failed for two minutes, and let the rollout continue.
func TestWaitReady_unreachable_node_is_not_ready(t *testing.T) {
	err := WaitReady(context.Background(),
		Target{RQLiteBase: "http://127.0.0.1:1"},
		Options{Budget: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("an unreachable node reported ready")
	}
	if !strings.Contains(err.Error(), "rqlite status") {
		t.Fatalf("error does not identify what could not be read: %v", err)
	}
}

func TestWaitReady_cancellation(t *testing.T) {
	srv := newNode(t, func() string { return statusJSON("Candidate", "", 0, 0) }, 200)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := WaitReady(ctx, target(srv), Options{Budget: time.Minute}); err == nil {
		t.Fatal("want an error on a cancelled context")
	}
}
