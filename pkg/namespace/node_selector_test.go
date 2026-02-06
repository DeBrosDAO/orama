package namespace

import (
	"testing"

	"go.uber.org/zap"
)

func TestCalculateCapacityScore_EmptyNode(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Empty node should have score of 1.0 (100% available)
	score := selector.calculateCapacityScore(
		0, 100,   // deployments
		0, 9900,  // ports
		0, 8192,  // memory
		0, 400,   // cpu
		0, 20,    // namespace instances
	)

	if score != 1.0 {
		t.Errorf("Empty node score = %f, want 1.0", score)
	}
}

func TestCalculateCapacityScore_FullNode(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Full node should have score of 0.0 (0% available)
	score := selector.calculateCapacityScore(
		100, 100,   // deployments (full)
		9900, 9900, // ports (full)
		8192, 8192, // memory (full)
		400, 400,   // cpu (full)
		20, 20,     // namespace instances (full)
	)

	if score != 0.0 {
		t.Errorf("Full node score = %f, want 0.0", score)
	}
}

func TestCalculateCapacityScore_HalfCapacity(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Half-full node should have score of approximately 0.5
	score := selector.calculateCapacityScore(
		50, 100,    // 50% deployments
		4950, 9900, // 50% ports
		4096, 8192, // 50% memory
		200, 400,   // 50% cpu
		10, 20,     // 50% namespace instances
	)

	// With all components at 50%, the weighted average should be 0.5
	expected := 0.5
	tolerance := 0.01

	if score < expected-tolerance || score > expected+tolerance {
		t.Errorf("Half capacity score = %f, want approximately %f", score, expected)
	}
}

func TestCalculateCapacityScore_Weights(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Test that deployment weight is 30%, namespace instance weight is 25%
	// Only deployments full (other metrics empty)
	deploymentOnlyScore := selector.calculateCapacityScore(
		100, 100, // deployments full (contributes 0 * 0.30 = 0)
		0, 9900,  // ports empty (contributes 1.0 * 0.15 = 0.15)
		0, 8192,  // memory empty (contributes 1.0 * 0.15 = 0.15)
		0, 400,   // cpu empty (contributes 1.0 * 0.15 = 0.15)
		0, 20,    // namespace instances empty (contributes 1.0 * 0.25 = 0.25)
	)
	// Expected: 0 + 0.15 + 0.15 + 0.15 + 0.25 = 0.70
	expectedDeploymentOnly := 0.70
	tolerance := 0.01

	if deploymentOnlyScore < expectedDeploymentOnly-tolerance || deploymentOnlyScore > expectedDeploymentOnly+tolerance {
		t.Errorf("Deployment-only-full score = %f, want %f", deploymentOnlyScore, expectedDeploymentOnly)
	}

	// Only namespace instances full (other metrics empty)
	namespaceOnlyScore := selector.calculateCapacityScore(
		0, 100,   // deployments empty (contributes 1.0 * 0.30 = 0.30)
		0, 9900,  // ports empty (contributes 1.0 * 0.15 = 0.15)
		0, 8192,  // memory empty (contributes 1.0 * 0.15 = 0.15)
		0, 400,   // cpu empty (contributes 1.0 * 0.15 = 0.15)
		20, 20,   // namespace instances full (contributes 0 * 0.25 = 0)
	)
	// Expected: 0.30 + 0.15 + 0.15 + 0.15 + 0 = 0.75
	expectedNamespaceOnly := 0.75

	if namespaceOnlyScore < expectedNamespaceOnly-tolerance || namespaceOnlyScore > expectedNamespaceOnly+tolerance {
		t.Errorf("Namespace-only-full score = %f, want %f", namespaceOnlyScore, expectedNamespaceOnly)
	}
}

func TestCalculateCapacityScore_NegativeValues(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Test that over-capacity values (which would produce negative scores) are clamped to 0
	score := selector.calculateCapacityScore(
		200, 100,     // 200% deployments (should clamp to 0)
		20000, 9900,  // over ports (should clamp to 0)
		16000, 8192,  // over memory (should clamp to 0)
		800, 400,     // over cpu (should clamp to 0)
		40, 20,       // over namespace instances (should clamp to 0)
	)

	if score != 0.0 {
		t.Errorf("Over-capacity score = %f, want 0.0", score)
	}
}

func TestNodeCapacity_AvailableSlots(t *testing.T) {
	tests := []struct {
		name               string
		instanceCount      int
		expectedAvailable  int
	}{
		{"Empty node", 0, 20},
		{"One instance", 1, 19},
		{"Half full", 10, 10},
		{"Almost full", 19, 1},
		{"Full", 20, 0},
		{"Over capacity", 25, 0}, // Should clamp to 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			available := MaxNamespacesPerNode - tt.instanceCount
			if available < 0 {
				available = 0
			}
			if available != tt.expectedAvailable {
				t.Errorf("Available slots for %d instances = %d, want %d",
					tt.instanceCount, available, tt.expectedAvailable)
			}
		})
	}
}

func TestNewClusterNodeSelector(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)

	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	if selector == nil {
		t.Fatal("NewClusterNodeSelector returned nil")
	}
}

func TestNodeCapacityStruct(t *testing.T) {
	// Test NodeCapacity struct initialization
	capacity := NodeCapacity{
		NodeID:                  "node-123",
		IPAddress:               "192.168.1.100",
		DeploymentCount:         10,
		AllocatedPorts:          50,
		AvailablePorts:          9850,
		UsedMemoryMB:            2048,
		AvailableMemoryMB:       6144,
		UsedCPUPercent:          100,
		NamespaceInstanceCount:  5,
		AvailableNamespaceSlots: 15,
		Score:                   0.75,
	}

	if capacity.NodeID != "node-123" {
		t.Errorf("NodeID = %s, want node-123", capacity.NodeID)
	}
	if capacity.AvailableNamespaceSlots != 15 {
		t.Errorf("AvailableNamespaceSlots = %d, want 15", capacity.AvailableNamespaceSlots)
	}
	if capacity.Score != 0.75 {
		t.Errorf("Score = %f, want 0.75", capacity.Score)
	}
}

func TestScoreRanking(t *testing.T) {
	// Test that higher scores indicate more available capacity
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	portAllocator := NewNamespacePortAllocator(mockDB, logger)
	selector := NewClusterNodeSelector(mockDB, portAllocator, logger)

	// Node A: Light load
	scoreA := selector.calculateCapacityScore(
		10, 100,   // 10% deployments
		500, 9900, // ~5% ports
		1000, 8192,// ~12% memory
		50, 400,   // ~12% cpu
		2, 20,     // 10% namespace instances
	)

	// Node B: Heavy load
	scoreB := selector.calculateCapacityScore(
		80, 100,    // 80% deployments
		8000, 9900, // ~80% ports
		7000, 8192, // ~85% memory
		350, 400,   // ~87% cpu
		18, 20,     // 90% namespace instances
	)

	if scoreA <= scoreB {
		t.Errorf("Light load score (%f) should be higher than heavy load score (%f)", scoreA, scoreB)
	}
}
