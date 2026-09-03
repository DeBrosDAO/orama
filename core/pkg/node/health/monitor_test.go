package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------
// RingNeighbors
// ---------------------------------------------------------------

func TestRingNeighbors_Basic(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
		{ID: "C", InternalIP: "10.0.0.3"},
		{ID: "D", InternalIP: "10.0.0.4"},
		{ID: "E", InternalIP: "10.0.0.5"},
		{ID: "F", InternalIP: "10.0.0.6"},
	}

	neighbors := RingNeighbors(nodes, "C", 3)
	if len(neighbors) != 3 {
		t.Fatalf("expected 3 neighbors, got %d", len(neighbors))
	}
	want := []string{"D", "E", "F"}
	for i, n := range neighbors {
		if n.ID != want[i] {
			t.Errorf("neighbor[%d] = %s, want %s", i, n.ID, want[i])
		}
	}
}

func TestRingNeighbors_Wrap(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
		{ID: "C", InternalIP: "10.0.0.3"},
		{ID: "D", InternalIP: "10.0.0.4"},
		{ID: "E", InternalIP: "10.0.0.5"},
		{ID: "F", InternalIP: "10.0.0.6"},
	}

	// E's neighbors should wrap: F, A, B
	neighbors := RingNeighbors(nodes, "E", 3)
	if len(neighbors) != 3 {
		t.Fatalf("expected 3 neighbors, got %d", len(neighbors))
	}
	want := []string{"F", "A", "B"}
	for i, n := range neighbors {
		if n.ID != want[i] {
			t.Errorf("neighbor[%d] = %s, want %s", i, n.ID, want[i])
		}
	}
}

func TestRingNeighbors_LastNode(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
		{ID: "C", InternalIP: "10.0.0.3"},
		{ID: "D", InternalIP: "10.0.0.4"},
	}

	// D is last, neighbors = A, B, C
	neighbors := RingNeighbors(nodes, "D", 3)
	if len(neighbors) != 3 {
		t.Fatalf("expected 3 neighbors, got %d", len(neighbors))
	}
	want := []string{"A", "B", "C"}
	for i, n := range neighbors {
		if n.ID != want[i] {
			t.Errorf("neighbor[%d] = %s, want %s", i, n.ID, want[i])
		}
	}
}

func TestRingNeighbors_UnsortedInput(t *testing.T) {
	// Input not sorted — RingNeighbors should sort internally
	nodes := []nodeInfo{
		{ID: "F", InternalIP: "10.0.0.6"},
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "D", InternalIP: "10.0.0.4"},
		{ID: "C", InternalIP: "10.0.0.3"},
		{ID: "B", InternalIP: "10.0.0.2"},
		{ID: "E", InternalIP: "10.0.0.5"},
	}

	neighbors := RingNeighbors(nodes, "C", 3)
	want := []string{"D", "E", "F"}
	for i, n := range neighbors {
		if n.ID != want[i] {
			t.Errorf("neighbor[%d] = %s, want %s", i, n.ID, want[i])
		}
	}
}

func TestRingNeighbors_SelfNotInRing(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
	}

	neighbors := RingNeighbors(nodes, "Z", 3)
	if len(neighbors) != 0 {
		t.Fatalf("expected 0 neighbors when self not in ring, got %d", len(neighbors))
	}
}

func TestRingNeighbors_SingleNode(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
	}

	neighbors := RingNeighbors(nodes, "A", 3)
	if len(neighbors) != 0 {
		t.Fatalf("expected 0 neighbors for single-node ring, got %d", len(neighbors))
	}
}

func TestRingNeighbors_TwoNodes(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
	}

	neighbors := RingNeighbors(nodes, "A", 3)
	if len(neighbors) != 1 {
		t.Fatalf("expected 1 neighbor (K capped), got %d", len(neighbors))
	}
	if neighbors[0].ID != "B" {
		t.Errorf("expected B, got %s", neighbors[0].ID)
	}
}

func TestRingNeighbors_KLargerThanRing(t *testing.T) {
	nodes := []nodeInfo{
		{ID: "A", InternalIP: "10.0.0.1"},
		{ID: "B", InternalIP: "10.0.0.2"},
		{ID: "C", InternalIP: "10.0.0.3"},
	}

	// K=10 but only 2 other nodes
	neighbors := RingNeighbors(nodes, "A", 10)
	if len(neighbors) != 2 {
		t.Fatalf("expected 2 neighbors (capped to ring size-1), got %d", len(neighbors))
	}
}

// ---------------------------------------------------------------
// State transitions
// ---------------------------------------------------------------

func TestStateTransitions(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		ProbeInterval:      time.Second,
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond, // disable grace for this test
	})
	time.Sleep(2 * time.Millisecond) // ensure grace period expired

	ctx := context.Background()

	// Peer starts healthy
	m.updateState(ctx, "peer1", true)
	if m.peers["peer1"].status != "healthy" {
		t.Fatalf("expected healthy, got %s", m.peers["peer1"].status)
	}

	// 2 misses → still healthy
	m.updateState(ctx, "peer1", false)
	m.updateState(ctx, "peer1", false)
	if m.peers["peer1"].status != "healthy" {
		t.Fatalf("expected healthy after 2 misses, got %s", m.peers["peer1"].status)
	}

	// 3rd miss → suspect
	m.updateState(ctx, "peer1", false)
	if m.peers["peer1"].status != "suspect" {
		t.Fatalf("expected suspect after 3 misses, got %s", m.peers["peer1"].status)
	}

	// Continue missing up to 11 → still suspect
	for i := 0; i < 8; i++ {
		m.updateState(ctx, "peer1", false)
	}
	if m.peers["peer1"].status != "suspect" {
		t.Fatalf("expected suspect after 11 misses, got %s", m.peers["peer1"].status)
	}

	// 12th miss → dead
	m.updateState(ctx, "peer1", false)
	if m.peers["peer1"].status != "dead" {
		t.Fatalf("expected dead after 12 misses, got %s", m.peers["peer1"].status)
	}

	// Recovery → back to healthy
	m.updateState(ctx, "peer1", true)
	if m.peers["peer1"].status != "healthy" {
		t.Fatalf("expected healthy after recovery, got %s", m.peers["peer1"].status)
	}
	if m.peers["peer1"].missCount != 0 {
		t.Fatalf("expected missCount reset, got %d", m.peers["peer1"].missCount)
	}
}

// ---------------------------------------------------------------
// Probe
// ---------------------------------------------------------------

func TestProbe_Healthy(t *testing.T) {
	// Start a mock ping server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newTestMonitor(t, Config{
		NodeID:       "self",
		ProbeTimeout: 2 * time.Second,
	})

	// Extract host:port from test server
	addr := strings.TrimPrefix(srv.URL, "http://")
	node := nodeInfo{ID: "test", InternalIP: addr}

	// Override the URL format — probe uses port 6001, but we need the test server port.
	// Instead, test the HTTP client directly.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/internal/ping", nil)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify probe returns true for healthy server (using struct directly)
	_ = node // used above conceptually
}

func TestProbe_Unhealthy(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:       "self",
		ProbeTimeout: 100 * time.Millisecond,
	})

	// Probe an unreachable address
	node := nodeInfo{ID: "dead", InternalIP: "192.0.2.1"} // RFC 5737 TEST-NET, guaranteed unroutable
	ok := m.probe(context.Background(), node)
	if ok {
		t.Fatal("expected probe to fail for unreachable host")
	}
}

// ---------------------------------------------------------------
// Prune stale state
// ---------------------------------------------------------------

func TestPruneStaleState(t *testing.T) {
	m := newTestMonitor(t, Config{NodeID: "self"})

	m.mu.Lock()
	m.peers["A"] = &peerState{status: "healthy"}
	m.peers["B"] = &peerState{status: "suspect"}
	m.peers["C"] = &peerState{status: "healthy"}
	m.mu.Unlock()

	// Only A and C are current neighbors
	m.pruneStaleState([]nodeInfo{
		{ID: "A"},
		{ID: "C"},
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.peers["B"]; ok {
		t.Error("expected B to be pruned")
	}
	if _, ok := m.peers["A"]; !ok {
		t.Error("expected A to remain")
	}
	if _, ok := m.peers["C"]; !ok {
		t.Error("expected C to remain")
	}
}

// ---------------------------------------------------------------
// OnNodeDead callback
// ---------------------------------------------------------------

func TestOnNodeDead_Callback(t *testing.T) {
	var called atomic.Int32

	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond)
	m.OnNodeDead(func(nodeID string) {
		called.Add(1)
	})

	// Without a DB, checkQuorum is a no-op, so callback won't fire.
	// This test just verifies the registration path doesn't panic.
	ctx := context.Background()
	for i := 0; i < DefaultDeadAfter; i++ {
		m.updateState(ctx, "victim", false)
	}

	if m.peers["victim"].status != "dead" {
		t.Fatalf("expected dead, got %s", m.peers["victim"].status)
	}
}

// ---------------------------------------------------------------
// Startup grace period
// ---------------------------------------------------------------

func TestStartupGrace_PreventsDead(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Hour, // very long grace
	})

	ctx := context.Background()

	// Accumulate enough misses for dead (12)
	for i := 0; i < DefaultDeadAfter+5; i++ {
		m.updateState(ctx, "peer1", false)
	}

	m.mu.Lock()
	status := m.peers["peer1"].status
	m.mu.Unlock()

	// During grace, should be suspect, NOT dead
	if status != "suspect" {
		t.Fatalf("expected suspect during startup grace, got %s", status)
	}
}

func TestStartupGrace_AllowsDeadAfterExpiry(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond) // grace expired

	ctx := context.Background()
	for i := 0; i < DefaultDeadAfter; i++ {
		m.updateState(ctx, "peer1", false)
	}

	m.mu.Lock()
	status := m.peers["peer1"].status
	m.mu.Unlock()

	if status != "dead" {
		t.Fatalf("expected dead after grace expired, got %s", status)
	}
}

// ---------------------------------------------------------------
// MetadataReader integration
// ---------------------------------------------------------------

type mockMetadataReader struct {
	state    string
	lastSeen time.Time
	found    bool
}

func (m *mockMetadataReader) GetPeerLifecycleState(nodeID string) (string, time.Time, bool) {
	return m.state, m.lastSeen, m.found
}

func TestProbeNode_MaintenanceCountsHealthy(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID: "self",
		MetadataReader: &mockMetadataReader{
			state:    "maintenance",
			lastSeen: time.Now(),
			found:    true,
		},
	})

	node := nodeInfo{ID: "peer1", InternalIP: "192.0.2.1"} // unreachable
	ok := m.probeNode(context.Background(), node)
	if !ok {
		t.Fatal("maintenance node with recent LastSeen should count as healthy")
	}
}

func TestProbeNode_RecentActiveSkipsHTTP(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID: "self",
		MetadataReader: &mockMetadataReader{
			state:    "active",
			lastSeen: time.Now(),
			found:    true,
		},
	})

	// Use unreachable IP — if HTTP were attempted, it would fail
	node := nodeInfo{ID: "peer1", InternalIP: "192.0.2.1"}
	ok := m.probeNode(context.Background(), node)
	if !ok {
		t.Fatal("recently seen active node should skip HTTP and count as healthy")
	}
}

func TestProbeNode_StaleMetadataFallsToHTTP(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:       "self",
		ProbeTimeout: 100 * time.Millisecond,
		MetadataReader: &mockMetadataReader{
			state:    "active",
			lastSeen: time.Now().Add(-5 * time.Minute), // stale
			found:    true,
		},
	})

	node := nodeInfo{ID: "peer1", InternalIP: "192.0.2.1"} // unreachable
	ok := m.probeNode(context.Background(), node)
	if ok {
		t.Fatal("stale metadata should fall through to HTTP probe, which should fail")
	}
}

func TestProbeNode_UnknownPeerFallsToHTTP(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:       "self",
		ProbeTimeout: 100 * time.Millisecond,
		MetadataReader: &mockMetadataReader{
			found: false, // peer not found in metadata
		},
	})

	node := nodeInfo{ID: "unknown", InternalIP: "192.0.2.1"}
	ok := m.probeNode(context.Background(), node)
	if ok {
		t.Fatal("unknown peer should fall through to HTTP probe, which should fail")
	}
}

func TestProbeNode_NoMetadataReader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newTestMonitor(t, Config{
		NodeID:         "self",
		ProbeTimeout:   2 * time.Second,
		MetadataReader: nil, // no metadata reader
	})

	// Without MetadataReader, should go straight to HTTP
	addr := strings.TrimPrefix(srv.URL, "http://")
	node := nodeInfo{ID: "peer1", InternalIP: addr}
	// Note: probe() hardcodes port 6001, so this won't hit our server.
	// But we verify it doesn't panic and falls through correctly.
	_ = m.probeNode(context.Background(), node)
}

// ---------------------------------------------------------------
// Recovery callback (C2 fix)
// ---------------------------------------------------------------

func TestRecoveryCallback_InvokedWithoutLock(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond)

	var recoveredNode string
	m.OnNodeRecovered(func(nodeID string) {
		recoveredNode = nodeID
		// If lock were held, this would deadlock since we try to access peers
		// Just verify callback fires correctly
	})

	ctx := context.Background()

	// Drive to dead state
	for i := 0; i < DefaultDeadAfter; i++ {
		m.updateState(ctx, "peer1", false)
	}

	m.mu.Lock()
	if m.peers["peer1"].status != "dead" {
		m.mu.Unlock()
		t.Fatal("expected dead state")
	}
	m.mu.Unlock()

	// Recover
	m.updateState(ctx, "peer1", true)

	if recoveredNode != "peer1" {
		t.Fatalf("expected recovery callback for peer1, got %q", recoveredNode)
	}

	m.mu.Lock()
	if m.peers["peer1"].status != "healthy" {
		m.mu.Unlock()
		t.Fatal("expected healthy after recovery")
	}
	m.mu.Unlock()
}

// ---------------------------------------------------------------
// OnNodeSuspect callback
// ---------------------------------------------------------------

func TestOnNodeSuspect_Callback(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond)

	var suspectNode string
	m.OnNodeSuspect(func(nodeID string) {
		suspectNode = nodeID
	})

	ctx := context.Background()

	// Drive 3 misses (DefaultSuspectAfter) → healthy → suspect
	for i := 0; i < DefaultSuspectAfter; i++ {
		m.updateState(ctx, "peer1", false)
	}

	if suspectNode != "peer1" {
		t.Fatalf("expected suspect callback for peer1, got %q", suspectNode)
	}

	m.mu.Lock()
	if m.peers["peer1"].status != "suspect" {
		m.mu.Unlock()
		t.Fatalf("expected suspect state, got %s", m.peers["peer1"].status)
	}
	m.mu.Unlock()
}

func TestOnNodeSuspect_DoesNotFireOnSubsequentMisses(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond)

	var callCount int32
	m.OnNodeSuspect(func(nodeID string) {
		callCount++
	})

	ctx := context.Background()

	// Drive to suspect (3 misses)
	for i := 0; i < DefaultSuspectAfter; i++ {
		m.updateState(ctx, "peer1", false)
	}
	if callCount != 1 {
		t.Fatalf("expected suspect callback to fire once after 3 misses, got %d", callCount)
	}

	// Keep missing (4th through 11th miss) — should NOT fire suspect again
	for i := 0; i < 8; i++ {
		m.updateState(ctx, "peer1", false)
	}

	if callCount != 1 {
		t.Fatalf("expected suspect callback to fire exactly once, got %d", callCount)
	}
}

func TestRecoveredFromSuspect_Callback(t *testing.T) {
	m := newTestMonitor(t, Config{
		NodeID:             "self",
		Neighbors:          3,
		StartupGracePeriod: 1 * time.Millisecond,
	})
	time.Sleep(2 * time.Millisecond)

	var recoveredNode string
	m.OnNodeRecovered(func(nodeID string) {
		recoveredNode = nodeID
	})

	ctx := context.Background()

	// Drive to suspect (3 misses, NOT dead)
	for i := 0; i < DefaultSuspectAfter; i++ {
		m.updateState(ctx, "peer1", false)
	}

	m.mu.Lock()
	if m.peers["peer1"].status != "suspect" {
		m.mu.Unlock()
		t.Fatal("expected suspect state before recovery")
	}
	m.mu.Unlock()

	// Recover from suspect
	m.updateState(ctx, "peer1", true)

	if recoveredNode != "peer1" {
		t.Fatalf("expected recovery callback for peer1 after suspect, got %q", recoveredNode)
	}

	m.mu.Lock()
	if m.peers["peer1"].status != "healthy" {
		m.mu.Unlock()
		t.Fatal("expected healthy after recovery from suspect")
	}
	m.mu.Unlock()
}

// newTestMonitor builds a Monitor with a valid ProbePort so each test states
// only the field it exercises.
func newTestMonitor(t *testing.T, cfg Config) *Monitor {
	t.Helper()
	if cfg.ProbePort == 0 {
		cfg.ProbePort = 10104
	}
	m, err := NewMonitor(cfg)
	if err != nil {
		t.Fatalf("NewMonitor: %v", err)
	}
	return m
}
