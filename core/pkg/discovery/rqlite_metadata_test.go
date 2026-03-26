package discovery

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEffectiveLifecycleState(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{"empty defaults to active", "", "active"},
		{"explicit active", "active", "active"},
		{"joining", "joining", "joining"},
		{"maintenance", "maintenance", "maintenance"},
		{"draining", "draining", "draining"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &RQLiteNodeMetadata{LifecycleState: tt.state}
			if got := m.EffectiveLifecycleState(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsInMaintenance(t *testing.T) {
	m := &RQLiteNodeMetadata{LifecycleState: "maintenance"}
	if !m.IsInMaintenance() {
		t.Fatal("expected maintenance")
	}

	m.LifecycleState = "active"
	if m.IsInMaintenance() {
		t.Fatal("expected not maintenance")
	}

	// Empty state (old node) should not be maintenance
	m.LifecycleState = ""
	if m.IsInMaintenance() {
		t.Fatal("empty state should not be maintenance")
	}
}

func TestIsAvailable(t *testing.T) {
	m := &RQLiteNodeMetadata{LifecycleState: "active"}
	if !m.IsAvailable() {
		t.Fatal("expected available")
	}

	// Empty state (old node) defaults to active → available
	m.LifecycleState = ""
	if !m.IsAvailable() {
		t.Fatal("empty state should be available (backward compat)")
	}

	m.LifecycleState = "maintenance"
	if m.IsAvailable() {
		t.Fatal("maintenance should not be available")
	}
}

func TestIsMaintenanceExpired(t *testing.T) {
	// Expired
	m := &RQLiteNodeMetadata{
		LifecycleState: "maintenance",
		MaintenanceTTL: time.Now().Add(-1 * time.Minute),
	}
	if !m.IsMaintenanceExpired() {
		t.Fatal("expected expired")
	}

	// Not expired
	m.MaintenanceTTL = time.Now().Add(5 * time.Minute)
	if m.IsMaintenanceExpired() {
		t.Fatal("expected not expired")
	}

	// Zero TTL in maintenance
	m.MaintenanceTTL = time.Time{}
	if m.IsMaintenanceExpired() {
		t.Fatal("zero TTL should not be considered expired")
	}

	// Not in maintenance
	m.LifecycleState = "active"
	m.MaintenanceTTL = time.Now().Add(-1 * time.Minute)
	if m.IsMaintenanceExpired() {
		t.Fatal("active state should not report expired")
	}
}

// TestBackwardCompatibility verifies that old metadata (without new fields)
// unmarshals correctly — new fields get zero values, helpers return sane defaults.
func TestBackwardCompatibility(t *testing.T) {
	oldJSON := `{
		"node_id": "10.0.0.1:7001",
		"raft_address": "10.0.0.1:7001",
		"http_address": "10.0.0.1:5001",
		"node_type": "node",
		"raft_log_index": 42,
		"cluster_version": "1.0"
	}`

	var m RQLiteNodeMetadata
	if err := json.Unmarshal([]byte(oldJSON), &m); err != nil {
		t.Fatalf("unmarshal old metadata: %v", err)
	}

	// Existing fields preserved
	if m.NodeID != "10.0.0.1:7001" {
		t.Fatalf("expected node_id 10.0.0.1:7001, got %s", m.NodeID)
	}
	if m.RaftLogIndex != 42 {
		t.Fatalf("expected raft_log_index 42, got %d", m.RaftLogIndex)
	}

	// New fields default to zero values
	if m.PeerID != "" {
		t.Fatalf("expected empty PeerID, got %q", m.PeerID)
	}
	if m.LifecycleState != "" {
		t.Fatalf("expected empty LifecycleState, got %q", m.LifecycleState)
	}
	if m.Services != nil {
		t.Fatal("expected nil Services")
	}

	// Helpers return correct defaults
	if m.EffectiveLifecycleState() != "active" {
		t.Fatalf("expected effective state 'active', got %q", m.EffectiveLifecycleState())
	}
	if !m.IsAvailable() {
		t.Fatal("old metadata should be available")
	}
	if m.IsInMaintenance() {
		t.Fatal("old metadata should not be in maintenance")
	}
}

// TestNewFieldsRoundTrip verifies that new fields marshal/unmarshal correctly.
func TestNewFieldsRoundTrip(t *testing.T) {
	original := &RQLiteNodeMetadata{
		NodeID:         "10.0.0.1:7001",
		RaftAddress:    "10.0.0.1:7001",
		HTTPAddress:    "10.0.0.1:5001",
		NodeType:       "node",
		RaftLogIndex:   100,
		ClusterVersion: "1.0",
		PeerID:         "QmPeerID123",
		WireGuardIP:    "10.0.0.1",
		LifecycleState: "maintenance",
		MaintenanceTTL: time.Now().Add(10 * time.Minute).Truncate(time.Millisecond),
		BinaryVersion:  "1.2.3",
		Services: map[string]*ServiceStatus{
			"rqlite": {Name: "rqlite", Running: true, Healthy: true, Message: "leader"},
		},
		Namespaces: map[string]*NamespaceStatus{
			"myapp": {Name: "myapp", Status: "healthy"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RQLiteNodeMetadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.PeerID != original.PeerID {
		t.Fatalf("PeerID: got %q, want %q", decoded.PeerID, original.PeerID)
	}
	if decoded.WireGuardIP != original.WireGuardIP {
		t.Fatalf("WireGuardIP: got %q, want %q", decoded.WireGuardIP, original.WireGuardIP)
	}
	if decoded.LifecycleState != original.LifecycleState {
		t.Fatalf("LifecycleState: got %q, want %q", decoded.LifecycleState, original.LifecycleState)
	}
	if decoded.BinaryVersion != original.BinaryVersion {
		t.Fatalf("BinaryVersion: got %q, want %q", decoded.BinaryVersion, original.BinaryVersion)
	}
	if decoded.Services["rqlite"] == nil || !decoded.Services["rqlite"].Running {
		t.Fatal("expected rqlite service to be running")
	}
	if decoded.Namespaces["myapp"] == nil || decoded.Namespaces["myapp"].Status != "healthy" {
		t.Fatal("expected myapp namespace to be healthy")
	}
}

// TestOldNodeReadsNewMetadata simulates an old node (that doesn't know about new fields)
// reading metadata from a new node. Go's JSON unmarshalling silently ignores unknown fields.
func TestOldNodeReadsNewMetadata(t *testing.T) {
	newJSON := `{
		"node_id": "10.0.0.1:7001",
		"raft_address": "10.0.0.1:7001",
		"http_address": "10.0.0.1:5001",
		"node_type": "node",
		"raft_log_index": 42,
		"cluster_version": "1.0",
		"peer_id": "QmSomePeerID",
		"wireguard_ip": "10.0.0.1",
		"lifecycle_state": "maintenance",
		"maintenance_ttl": "2025-01-01T00:00:00Z",
		"binary_version": "2.0.0",
		"services": {"rqlite": {"name": "rqlite", "running": true, "healthy": true}},
		"namespaces": {"app": {"name": "app", "status": "healthy"}},
		"some_future_field": "unknown"
	}`

	// Simulate "old" struct with only original fields
	type OldMetadata struct {
		NodeID         string `json:"node_id"`
		RaftAddress    string `json:"raft_address"`
		HTTPAddress    string `json:"http_address"`
		NodeType       string `json:"node_type"`
		RaftLogIndex   uint64 `json:"raft_log_index"`
		ClusterVersion string `json:"cluster_version"`
	}

	var old OldMetadata
	if err := json.Unmarshal([]byte(newJSON), &old); err != nil {
		t.Fatalf("old node should unmarshal new metadata without error: %v", err)
	}

	if old.NodeID != "10.0.0.1:7001" || old.RaftLogIndex != 42 {
		t.Fatal("old fields should be preserved")
	}
}
