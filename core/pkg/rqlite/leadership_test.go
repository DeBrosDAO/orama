package rqlite

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeRQLite serves /status and /nodes, and records transfer attempts. state is
// swapped atomically so a test can make the node step down mid-poll.
type fakeRQLite struct {
	srv                *httptest.Server
	state              atomic.Value // string
	nodes              []map[string]any
	transfers          atomic.Int32
	transferStatus     int
	stepDownOnTransfer bool
}

func newFakeRQLite(t *testing.T, state string, nodes []map[string]any) *fakeRQLite {
	t.Helper()
	f := &fakeRQLite{nodes: nodes, transferStatus: http.StatusOK}
	f.state.Store(state)
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"store": map[string]any{"raft": map[string]any{
				"state": f.state.Load().(string), "leader_id": "self", "voter": true,
			}},
		})
	})
	// The real endpoint is queried with ver=2, which wraps the members in a
	// "nodes" array; a map body parses to nothing and silently yields no
	// transfer targets.
	mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"nodes": f.nodes})
	})
	mux.HandleFunc("/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/transfer-leadership") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.transfers.Add(1)
		if f.stepDownOnTransfer {
			f.state.Store("Follower")
		}
		w.WriteHeader(f.transferStatus)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRQLite) port(t *testing.T) int {
	t.Helper()
	_, portStr, err := splitHostPortForTest(f.srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return p
}

func splitHostPortForTest(url string) (string, string, error) {
	trimmed := strings.TrimPrefix(url, "http://")
	i := strings.LastIndex(trimmed, ":")
	if i < 0 {
		return "", "", errors.New("no port")
	}
	return trimmed[:i], trimmed[i+1:], nil
}

func voterNode(reachable bool, id string) map[string]any {
	return map[string]any{"id": id, "voter": true, "reachable": reachable, "addr": id}
}

// A follower has nothing to hand over, and must not POST a transfer.
func TestTransferLeadershipNoOpOnFollower(t *testing.T) {
	f := newFakeRQLite(t, "Follower", []map[string]any{voterNode(true, "a"), voterNode(true, "b")})
	if err := TransferLeadership(f.port(t), zap.NewNop()); err != nil {
		t.Fatalf("follower transfer returned error: %v", err)
	}
	if n := f.transfers.Load(); n != 0 {
		t.Errorf("follower issued %d transfer requests, want 0", n)
	}
}

// The happy path: leader hands over and actually stops leading.
func TestTransferLeadershipSucceedsWhenNodeStepsDown(t *testing.T) {
	f := newFakeRQLite(t, "Leader", []map[string]any{voterNode(true, "peer-1")})
	f.stepDownOnTransfer = true
	if err := TransferLeadership(f.port(t), zap.NewNop()); err != nil {
		t.Fatalf("transfer returned error: %v", err)
	}
	if n := f.transfers.Load(); n != 1 {
		t.Errorf("issued %d transfer requests, want 1", n)
	}
}

// The regression: a leader that never steps down used to be reported as
// success, so the caller restarted the leader anyway.
func TestTransferLeadershipFailsWhenStillLeader(t *testing.T) {
	f := newFakeRQLite(t, "Leader", []map[string]any{voterNode(true, "peer-1")})
	f.stepDownOnTransfer = false

	orig := transferStepDownTimeout
	transferStepDownTimeout = 1500 * time.Millisecond
	defer func() { transferStepDownTimeout = orig }()

	done := make(chan error, 1)
	go func() { done <- TransferLeadershipTo(f.port(t), "peer-1", zap.NewNop()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a node that never stepped down was reported as a successful transfer")
		}
		if !strings.Contains(err.Error(), "still leader") {
			t.Errorf("error = %v, want it to say the node is still leader", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("TransferLeadershipTo did not return within its own timeout")
	}
}

// A leader with no other reachable voter cannot hand over; that is a refusal,
// not a shrug.
func TestTransferLeadershipNoEligibleTarget(t *testing.T) {
	f := newFakeRQLite(t, "Leader", []map[string]any{voterNode(false, "peer-1")})
	err := TransferLeadership(f.port(t), zap.NewNop())
	if !errors.Is(err, ErrNoTransferTarget) {
		t.Fatalf("error = %v, want ErrNoTransferTarget", err)
	}
	if n := f.transfers.Load(); n != 0 {
		t.Errorf("issued %d transfer requests with no target, want 0", n)
	}
}

// A non-OK response is a failed handover, not a best-effort success.
func TestTransferLeadershipToPropagatesHTTPError(t *testing.T) {
	f := newFakeRQLite(t, "Leader", []map[string]any{voterNode(true, "peer-1")})
	f.transferStatus = http.StatusInternalServerError
	err := TransferLeadershipTo(f.port(t), "peer-1", zap.NewNop())
	if err == nil {
		t.Fatal("HTTP 500 from transfer-leadership was treated as success")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention the status code", err)
	}
}

// A build without the API is a capability gap, not a failure: the caller falls
// back to SIGTERM step-down.
func TestTransferLeadershipToToleratesMissingAPI(t *testing.T) {
	f := newFakeRQLite(t, "Leader", []map[string]any{voterNode(true, "peer-1")})
	f.transferStatus = http.StatusNotFound
	if err := TransferLeadershipTo(f.port(t), "peer-1", zap.NewNop()); err != nil {
		t.Fatalf("404 should be tolerated, got %v", err)
	}
}
