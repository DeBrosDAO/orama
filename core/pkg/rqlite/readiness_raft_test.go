package rqlite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// raftStatusServer serves /status with a swappable body so a test can make a node
// converge partway through the wait.
func raftStatusServer(t *testing.T, body *atomic.Value, code *atomic.Int32) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := code.Load(); c != 0 && int(c) != http.StatusOK {
			w.WriteHeader(int(c))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	i := strings.LastIndex(srv.URL, ":")
	port, err := strconv.Atoi(srv.URL[i+1:])
	if err != nil {
		t.Fatalf("port from %q: %v", srv.URL, err)
	}
	return port
}

func statusBody(state string) string {
	return `{"store":{"raft":{"state":"` + state + `"}}}`
}

// "The port answers" is not readiness. rqlited binds HTTP before it has joined
// anything, so only Leader or Follower means the node is participating.
func TestWaitForRaftReadyAcceptsOnlyParticipatingStates(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		ready bool
	}{
		{"leader", statusBody("Leader"), true},
		{"follower", statusBody("Follower"), true},
		{"lowercase leader is still a leader", statusBody("leader"), true},
		{"candidate is not ready", statusBody("Candidate"), false},
		{"shutdown is not ready", statusBody("Shutdown"), false},
		// The exact shape the old code mis-read: it looked for a top-level
		// "raft" key, and returned SUCCESS from the else branch when the
		// assertion failed.
		{"top-level raft key is not readiness", `{"raft":{"state":"Leader"}}`, false},
		{"empty object is not readiness", `{}`, false},
		{"malformed json is not readiness", `{not json`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body atomic.Value
			var code atomic.Int32
			body.Store(tc.body)
			port := raftStatusServer(t, &body, &code)

			err := WaitForRaftReady(context.Background(), port, 300*time.Millisecond)
			if tc.ready && err != nil {
				t.Fatalf("expected ready, got %v", err)
			}
			if !tc.ready && err == nil {
				t.Fatal("expected not-ready, got readiness")
			}
		})
	}
}

// A node that converges during the wait must be picked up, not timed out.
func TestWaitForRaftReadyConvergesMidWait(t *testing.T) {
	var body atomic.Value
	var code atomic.Int32
	body.Store(statusBody("Candidate"))
	port := raftStatusServer(t, &body, &code)

	go func() {
		time.Sleep(700 * time.Millisecond)
		body.Store(statusBody("Follower"))
	}()

	if err := WaitForRaftReady(context.Background(), port, 10*time.Second); err != nil {
		t.Fatalf("expected the converged node to be ready, got %v", err)
	}
}

// The timeout error must name the state actually observed, so an operator can
// tell "stuck as Candidate" from "never answered".
func TestWaitForRaftReadyReportsLastState(t *testing.T) {
	var body atomic.Value
	var code atomic.Int32
	body.Store(statusBody("Candidate"))
	port := raftStatusServer(t, &body, &code)

	err := WaitForRaftReady(context.Background(), port, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "Candidate") {
		t.Errorf("error = %v, want it to name the observed state", err)
	}
}

func TestWaitForRaftReadyReportsUnreachable(t *testing.T) {
	err := WaitForRaftReady(context.Background(), 1, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error against a closed port")
	}
	if !strings.Contains(err.Error(), "never reported a raft state") {
		t.Errorf("error = %v, want it to say no state was ever reported", err)
	}
}

// A non-200 is not a state reading.
func TestWaitForRaftReadyRejectsNon200(t *testing.T) {
	var body atomic.Value
	var code atomic.Int32
	body.Store(statusBody("Leader"))
	code.Store(http.StatusServiceUnavailable)
	port := raftStatusServer(t, &body, &code)

	if err := WaitForRaftReady(context.Background(), port, 300*time.Millisecond); err == nil {
		t.Fatal("HTTP 503 was accepted as readiness")
	}
}

func TestWaitForRaftReadyHonoursContext(t *testing.T) {
	var body atomic.Value
	var code atomic.Int32
	body.Store(statusBody("Candidate"))
	port := raftStatusServer(t, &body, &code)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()

	start := time.Now()
	if err := WaitForRaftReady(ctx, port, 30*time.Second); err == nil {
		t.Fatal("expected cancellation to return an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %s, want prompt return", elapsed)
	}
}
