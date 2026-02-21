package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestWebRTCPortConstants_NoOverlap(t *testing.T) {
	// Verify WebRTC port ranges don't overlap with core namespace ports (10000-10099)
	ranges := []struct {
		name  string
		start int
		end   int
	}{
		{"core namespace", NamespacePortRangeStart, NamespacePortRangeEnd},
		{"SFU media", SFUMediaPortRangeStart, SFUMediaPortRangeEnd},
		{"SFU signaling", SFUSignalingPortRangeStart, SFUSignalingPortRangeEnd},
		{"TURN relay", TURNRelayPortRangeStart, TURNRelayPortRangeEnd},
	}

	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			a, b := ranges[i], ranges[j]
			if a.start <= b.end && b.start <= a.end {
				t.Errorf("Range overlap: %s (%d-%d) overlaps with %s (%d-%d)",
					a.name, a.start, a.end, b.name, b.start, b.end)
			}
		}
	}
}

func TestWebRTCPortConstants_Capacity(t *testing.T) {
	// SFU media: (29999-20000+1)/500 = 20 namespaces per node
	sfuMediaCapacity := (SFUMediaPortRangeEnd - SFUMediaPortRangeStart + 1) / SFUMediaPortsPerNamespace
	if sfuMediaCapacity < 20 {
		t.Errorf("SFU media capacity = %d, want >= 20", sfuMediaCapacity)
	}

	// SFU signaling: 30099-30000+1 = 100 ports → 100 namespaces per node
	sfuSignalingCapacity := SFUSignalingPortRangeEnd - SFUSignalingPortRangeStart + 1
	if sfuSignalingCapacity < 20 {
		t.Errorf("SFU signaling capacity = %d, want >= 20", sfuSignalingCapacity)
	}

	// TURN relay: (65535-49152+1)/800 = 20 namespaces per node
	turnRelayCapacity := (TURNRelayPortRangeEnd - TURNRelayPortRangeStart + 1) / TURNRelayPortsPerNamespace
	if turnRelayCapacity < 20 {
		t.Errorf("TURN relay capacity = %d, want >= 20", turnRelayCapacity)
	}
}

func TestWebRTCPortConstants_Values(t *testing.T) {
	if SFUMediaPortRangeStart != 20000 {
		t.Errorf("SFUMediaPortRangeStart = %d, want 20000", SFUMediaPortRangeStart)
	}
	if SFUMediaPortRangeEnd != 29999 {
		t.Errorf("SFUMediaPortRangeEnd = %d, want 29999", SFUMediaPortRangeEnd)
	}
	if SFUMediaPortsPerNamespace != 500 {
		t.Errorf("SFUMediaPortsPerNamespace = %d, want 500", SFUMediaPortsPerNamespace)
	}
	if SFUSignalingPortRangeStart != 30000 {
		t.Errorf("SFUSignalingPortRangeStart = %d, want 30000", SFUSignalingPortRangeStart)
	}
	if TURNRelayPortRangeStart != 49152 {
		t.Errorf("TURNRelayPortRangeStart = %d, want 49152", TURNRelayPortRangeStart)
	}
	if TURNRelayPortsPerNamespace != 800 {
		t.Errorf("TURNRelayPortsPerNamespace = %d, want 800", TURNRelayPortsPerNamespace)
	}
	if TURNDefaultPort != 3478 {
		t.Errorf("TURNDefaultPort = %d, want 3478", TURNDefaultPort)
	}
	if DefaultSFUNodeCount != 3 {
		t.Errorf("DefaultSFUNodeCount = %d, want 3", DefaultSFUNodeCount)
	}
	if DefaultTURNNodeCount != 2 {
		t.Errorf("DefaultTURNNodeCount = %d, want 2", DefaultTURNNodeCount)
	}
}

func TestNewWebRTCPortAllocator(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	if allocator == nil {
		t.Fatal("NewWebRTCPortAllocator returned nil")
	}
	if allocator.db != mockDB {
		t.Error("allocator.db not set correctly")
	}
}

func TestWebRTCPortAllocator_AllocateSFUPorts(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	block, err := allocator.AllocateSFUPorts(context.Background(), "node-1", "cluster-1")
	if err != nil {
		t.Fatalf("AllocateSFUPorts failed: %v", err)
	}

	if block == nil {
		t.Fatal("AllocateSFUPorts returned nil block")
	}

	if block.ServiceType != "sfu" {
		t.Errorf("ServiceType = %q, want %q", block.ServiceType, "sfu")
	}
	if block.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", block.NodeID, "node-1")
	}
	if block.NamespaceClusterID != "cluster-1" {
		t.Errorf("NamespaceClusterID = %q, want %q", block.NamespaceClusterID, "cluster-1")
	}

	// First allocation should get the first port in each range
	if block.SFUSignalingPort != SFUSignalingPortRangeStart {
		t.Errorf("SFUSignalingPort = %d, want %d", block.SFUSignalingPort, SFUSignalingPortRangeStart)
	}
	if block.SFUMediaPortStart != SFUMediaPortRangeStart {
		t.Errorf("SFUMediaPortStart = %d, want %d", block.SFUMediaPortStart, SFUMediaPortRangeStart)
	}
	if block.SFUMediaPortEnd != SFUMediaPortRangeStart+SFUMediaPortsPerNamespace-1 {
		t.Errorf("SFUMediaPortEnd = %d, want %d", block.SFUMediaPortEnd, SFUMediaPortRangeStart+SFUMediaPortsPerNamespace-1)
	}

	// TURN fields should be zero for SFU allocation
	if block.TURNListenPort != 0 {
		t.Errorf("TURNListenPort = %d, want 0 for SFU allocation", block.TURNListenPort)
	}
	if block.TURNRelayPortStart != 0 {
		t.Errorf("TURNRelayPortStart = %d, want 0 for SFU allocation", block.TURNRelayPortStart)
	}

	// Verify INSERT was called
	hasInsert := false
	for _, call := range mockDB.execCalls {
		if strings.Contains(call.Query, "INSERT INTO webrtc_port_allocations") {
			hasInsert = true
			break
		}
	}
	if !hasInsert {
		t.Error("expected INSERT INTO webrtc_port_allocations to be called")
	}
}

func TestWebRTCPortAllocator_AllocateTURNPorts(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	block, err := allocator.AllocateTURNPorts(context.Background(), "node-1", "cluster-1")
	if err != nil {
		t.Fatalf("AllocateTURNPorts failed: %v", err)
	}

	if block == nil {
		t.Fatal("AllocateTURNPorts returned nil block")
	}

	if block.ServiceType != "turn" {
		t.Errorf("ServiceType = %q, want %q", block.ServiceType, "turn")
	}
	if block.TURNListenPort != TURNDefaultPort {
		t.Errorf("TURNListenPort = %d, want %d", block.TURNListenPort, TURNDefaultPort)
	}
	if block.TURNTLSPort != TURNTLSPort {
		t.Errorf("TURNTLSPort = %d, want %d", block.TURNTLSPort, TURNTLSPort)
	}
	if block.TURNRelayPortStart != TURNRelayPortRangeStart {
		t.Errorf("TURNRelayPortStart = %d, want %d", block.TURNRelayPortStart, TURNRelayPortRangeStart)
	}
	if block.TURNRelayPortEnd != TURNRelayPortRangeStart+TURNRelayPortsPerNamespace-1 {
		t.Errorf("TURNRelayPortEnd = %d, want %d", block.TURNRelayPortEnd, TURNRelayPortRangeStart+TURNRelayPortsPerNamespace-1)
	}

	// SFU fields should be zero for TURN allocation
	if block.SFUSignalingPort != 0 {
		t.Errorf("SFUSignalingPort = %d, want 0 for TURN allocation", block.SFUSignalingPort)
	}
}

func TestWebRTCPortAllocator_DeallocateAll(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	err := allocator.DeallocateAll(context.Background(), "cluster-1")
	if err != nil {
		t.Fatalf("DeallocateAll failed: %v", err)
	}

	// Verify DELETE was called with correct cluster ID
	hasDelete := false
	for _, call := range mockDB.execCalls {
		if strings.Contains(call.Query, "DELETE FROM webrtc_port_allocations") &&
			strings.Contains(call.Query, "namespace_cluster_id") {
			hasDelete = true
			if len(call.Args) < 1 || call.Args[0] != "cluster-1" {
				t.Errorf("DELETE called with wrong cluster ID: %v", call.Args)
			}
		}
	}
	if !hasDelete {
		t.Error("expected DELETE FROM webrtc_port_allocations to be called")
	}
}

func TestWebRTCPortAllocator_DeallocateByNode(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	err := allocator.DeallocateByNode(context.Background(), "cluster-1", "node-1", "sfu")
	if err != nil {
		t.Fatalf("DeallocateByNode failed: %v", err)
	}

	// Verify DELETE was called with correct parameters
	hasDelete := false
	for _, call := range mockDB.execCalls {
		if strings.Contains(call.Query, "DELETE FROM webrtc_port_allocations") &&
			strings.Contains(call.Query, "service_type") {
			hasDelete = true
			if len(call.Args) != 3 {
				t.Fatalf("DELETE called with %d args, want 3", len(call.Args))
			}
			if call.Args[0] != "cluster-1" {
				t.Errorf("arg[0] = %v, want cluster-1", call.Args[0])
			}
			if call.Args[1] != "node-1" {
				t.Errorf("arg[1] = %v, want node-1", call.Args[1])
			}
			if call.Args[2] != "sfu" {
				t.Errorf("arg[2] = %v, want sfu", call.Args[2])
			}
		}
	}
	if !hasDelete {
		t.Error("expected DELETE FROM webrtc_port_allocations to be called")
	}
}

func TestWebRTCPortAllocator_NodeHasTURN(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	// Mock query returns empty results → no TURN on node
	hasTURN, err := allocator.NodeHasTURN(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("NodeHasTURN failed: %v", err)
	}
	if hasTURN {
		t.Error("expected NodeHasTURN = false for node with no allocations")
	}
}

func TestWebRTCPortAllocator_GetSFUPorts_NoAllocation(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	block, err := allocator.GetSFUPorts(context.Background(), "cluster-1", "node-1")
	if err != nil {
		t.Fatalf("GetSFUPorts failed: %v", err)
	}
	if block != nil {
		t.Error("expected nil block when no allocation exists")
	}
}

func TestWebRTCPortAllocator_GetTURNPorts_NoAllocation(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	block, err := allocator.GetTURNPorts(context.Background(), "cluster-1", "node-1")
	if err != nil {
		t.Fatalf("GetTURNPorts failed: %v", err)
	}
	if block != nil {
		t.Error("expected nil block when no allocation exists")
	}
}

func TestWebRTCPortAllocator_GetAllPorts_Empty(t *testing.T) {
	mockDB := newMockRQLiteClient()
	allocator := NewWebRTCPortAllocator(mockDB, testLogger())

	blocks, err := allocator.GetAllPorts(context.Background(), "cluster-1")
	if err != nil {
		t.Fatalf("GetAllPorts failed: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestWebRTCPortBlock_SFUFields(t *testing.T) {
	block := &WebRTCPortBlock{
		ID:                 "test-id",
		NodeID:             "node-1",
		NamespaceClusterID: "cluster-1",
		ServiceType:        "sfu",
		SFUSignalingPort:   30000,
		SFUMediaPortStart:  20000,
		SFUMediaPortEnd:    20499,
	}

	mediaRange := block.SFUMediaPortEnd - block.SFUMediaPortStart + 1
	if mediaRange != SFUMediaPortsPerNamespace {
		t.Errorf("SFU media range = %d, want %d", mediaRange, SFUMediaPortsPerNamespace)
	}
}

func TestWebRTCPortBlock_TURNFields(t *testing.T) {
	block := &WebRTCPortBlock{
		ID:                 "test-id",
		NodeID:             "node-1",
		NamespaceClusterID: "cluster-1",
		ServiceType:        "turn",
		TURNListenPort:     3478,
		TURNTLSPort:        443,
		TURNRelayPortStart: 49152,
		TURNRelayPortEnd:   49951,
	}

	relayRange := block.TURNRelayPortEnd - block.TURNRelayPortStart + 1
	if relayRange != TURNRelayPortsPerNamespace {
		t.Errorf("TURN relay range = %d, want %d", relayRange, TURNRelayPortsPerNamespace)
	}
}

// testLogger returns a no-op logger for tests
func testLogger() *zap.Logger {
	return zap.NewNop()
}
