package deployments

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// mockHomeNodeDB extends mockRQLiteClient for home node testing
type mockHomeNodeDB struct {
	*mockRQLiteClient
	assignments   map[string]string           // namespace -> homeNodeID
	nodes         map[string]nodeData         // nodeID -> nodeData
	deployments   map[string][]deploymentData // nodeID -> deployments
	resourceUsage map[string]resourceData     // nodeID -> resource usage
}

type nodeData struct {
	id       string
	status   string
	lastSeen time.Time
}

type deploymentData struct {
	id     string
	status string
}

type resourceData struct {
	memoryMB   int
	cpuPercent int
}

func newMockHomeNodeDB() *mockHomeNodeDB {
	return &mockHomeNodeDB{
		mockRQLiteClient: newMockRQLiteClient(),
		assignments:      make(map[string]string),
		nodes:            make(map[string]nodeData),
		deployments:      make(map[string][]deploymentData),
		resourceUsage:    make(map[string]resourceData),
	}
}

func (m *mockHomeNodeDB) Query(ctx context.Context, dest any, query string, args ...any) error {
	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Ptr {
		return nil
	}

	sliceVal := destVal.Elem()
	if sliceVal.Kind() != reflect.Slice {
		return nil
	}

	elemType := sliceVal.Type().Elem()

	// Handle different query types based on struct type
	switch elemType.Name() {
	case "nodeResult":
		// Active nodes query
		for _, node := range m.nodes {
			if node.status == "active" {
				nodeRes := reflect.New(elemType).Elem()
				nodeRes.FieldByName("ID").SetString(node.id)
				sliceVal.Set(reflect.Append(sliceVal, nodeRes))
			}
		}
		return nil

	case "homeNodeResult":
		// Home node lookup
		if len(args) > 0 {
			if namespace, ok := args[0].(string); ok {
				if homeNodeID, exists := m.assignments[namespace]; exists {
					hnRes := reflect.New(elemType).Elem()
					hnRes.FieldByName("HomeNodeID").SetString(homeNodeID)
					sliceVal.Set(reflect.Append(sliceVal, hnRes))
				}
			}
		}
		return nil

	case "countResult":
		// Deployment count or port count
		if len(args) > 0 {
			if nodeID, ok := args[0].(string); ok {
				count := len(m.deployments[nodeID])
				countRes := reflect.New(elemType).Elem()
				countRes.FieldByName("Count").SetInt(int64(count))
				sliceVal.Set(reflect.Append(sliceVal, countRes))
			}
		}
		return nil

	case "resourceResult":
		// Resource usage query
		if len(args) > 0 {
			if nodeID, ok := args[0].(string); ok {
				usage := m.resourceUsage[nodeID]
				resRes := reflect.New(elemType).Elem()
				resRes.FieldByName("TotalMemoryMB").SetInt(int64(usage.memoryMB))
				resRes.FieldByName("TotalCPUPercent").SetInt(int64(usage.cpuPercent))
				sliceVal.Set(reflect.Append(sliceVal, resRes))
			}
		}
		return nil

	case "namespaceResult":
		// Stale namespaces query
		// For testing, we'll return empty
		return nil
	}

	return m.mockRQLiteClient.Query(ctx, dest, query, args...)
}

func (m *mockHomeNodeDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	// Handle home node assignment (INSERT)
	if len(args) >= 2 {
		if namespace, ok := args[0].(string); ok {
			if homeNodeID, ok := args[1].(string); ok {
				m.assignments[namespace] = homeNodeID
				return nil, nil
			}
		}
	}

	// Handle migration (UPDATE) - args are: newNodeID, timestamp, timestamp, namespace
	if len(args) >= 4 {
		if newNodeID, ok := args[0].(string); ok {
			// Last arg should be namespace
			if namespace, ok := args[3].(string); ok {
				m.assignments[namespace] = newNodeID
				return nil, nil
			}
		}
	}

	return m.mockRQLiteClient.Exec(ctx, query, args...)
}

func (m *mockHomeNodeDB) addNode(id, status string) {
	m.nodes[id] = nodeData{
		id:       id,
		status:   status,
		lastSeen: time.Now(),
	}
}

// Implement interface methods (inherited from mockRQLiteClient but need to be available)
func (m *mockHomeNodeDB) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return m.mockRQLiteClient.FindBy(ctx, dest, table, criteria, opts...)
}

func (m *mockHomeNodeDB) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return m.mockRQLiteClient.FindOneBy(ctx, dest, table, criteria, opts...)
}

func (m *mockHomeNodeDB) Save(ctx context.Context, entity any) error {
	return m.mockRQLiteClient.Save(ctx, entity)
}

func (m *mockHomeNodeDB) Remove(ctx context.Context, entity any) error {
	return m.mockRQLiteClient.Remove(ctx, entity)
}

func (m *mockHomeNodeDB) Repository(table string) any {
	return m.mockRQLiteClient.Repository(table)
}

func (m *mockHomeNodeDB) CreateQueryBuilder(table string) *rqlite.QueryBuilder {
	return m.mockRQLiteClient.CreateQueryBuilder(table)
}

func (m *mockHomeNodeDB) Tx(ctx context.Context, fn func(tx rqlite.Tx) error) error {
	return m.mockRQLiteClient.Tx(ctx, fn)
}

func (m *mockHomeNodeDB) addDeployment(nodeID, deploymentID, status string) {
	m.deployments[nodeID] = append(m.deployments[nodeID], deploymentData{
		id:     deploymentID,
		status: status,
	})
}

func (m *mockHomeNodeDB) setResourceUsage(nodeID string, memoryMB, cpuPercent int) {
	m.resourceUsage[nodeID] = resourceData{
		memoryMB:   memoryMB,
		cpuPercent: cpuPercent,
	}
}

func TestHomeNodeManager_AssignHomeNode(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	ctx := context.Background()

	// Add test nodes
	mockDB.addNode("node-1", "active")
	mockDB.addNode("node-2", "active")
	mockDB.addNode("node-3", "active")

	t.Run("assign to new namespace", func(t *testing.T) {
		nodeID, err := hnm.AssignHomeNode(ctx, "test-namespace")
		if err != nil {
			t.Fatalf("failed to assign home node: %v", err)
		}

		if nodeID == "" {
			t.Error("expected non-empty node ID")
		}

		// Verify assignment was stored
		storedNodeID, err := hnm.GetHomeNode(ctx, "test-namespace")
		if err != nil {
			t.Fatalf("failed to get home node: %v", err)
		}

		if storedNodeID != nodeID {
			t.Errorf("stored node ID %s doesn't match assigned %s", storedNodeID, nodeID)
		}
	})

	t.Run("reuse existing assignment", func(t *testing.T) {
		// Assign once
		firstNodeID, err := hnm.AssignHomeNode(ctx, "namespace-2")
		if err != nil {
			t.Fatalf("failed first assignment: %v", err)
		}

		// Assign again - should return same node
		secondNodeID, err := hnm.AssignHomeNode(ctx, "namespace-2")
		if err != nil {
			t.Fatalf("failed second assignment: %v", err)
		}

		if firstNodeID != secondNodeID {
			t.Errorf("expected same node ID, got %s then %s", firstNodeID, secondNodeID)
		}
	})

	t.Run("error when no nodes available", func(t *testing.T) {
		emptyDB := newMockHomeNodeDB()
		emptyHNM := NewHomeNodeManager(emptyDB, portAllocator, logger)

		_, err := emptyHNM.AssignHomeNode(ctx, "test-namespace")
		if err != ErrNoNodesAvailable {
			t.Errorf("expected ErrNoNodesAvailable, got %v", err)
		}
	})
}

func TestHomeNodeManager_CalculateCapacityScore(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	tests := []struct {
		name            string
		deploymentCount int
		allocatedPorts  int
		availablePorts  int
		usedMemoryMB    int
		usedCPUPercent  int
		expectedMin     float64
		expectedMax     float64
	}{
		{
			name:            "empty node - perfect score",
			deploymentCount: 0,
			allocatedPorts:  0,
			availablePorts:  9900,
			usedMemoryMB:    0,
			usedCPUPercent:  0,
			expectedMin:     0.95,
			expectedMax:     1.0,
		},
		{
			name:            "half capacity",
			deploymentCount: 50,
			allocatedPorts:  4950,
			availablePorts:  4950,
			usedMemoryMB:    4096,
			usedCPUPercent:  200,
			expectedMin:     0.45,
			expectedMax:     0.55,
		},
		{
			name:            "full capacity - low score",
			deploymentCount: 100,
			allocatedPorts:  9900,
			availablePorts:  0,
			usedMemoryMB:    8192,
			usedCPUPercent:  400,
			expectedMin:     0.0,
			expectedMax:     0.05,
		},
		{
			name:            "light load",
			deploymentCount: 10,
			allocatedPorts:  1000,
			availablePorts:  8900,
			usedMemoryMB:    512,
			usedCPUPercent:  50,
			expectedMin:     0.80,
			expectedMax:     0.95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := hnm.calculateCapacityScore(
				tt.deploymentCount,
				tt.allocatedPorts,
				tt.availablePorts,
				tt.usedMemoryMB,
				tt.usedCPUPercent,
			)

			if score < tt.expectedMin || score > tt.expectedMax {
				t.Errorf("score %.2f outside expected range [%.2f, %.2f]", score, tt.expectedMin, tt.expectedMax)
			}

			// Score should always be in 0-1 range
			if score < 0 || score > 1 {
				t.Errorf("score %.2f outside valid range [0, 1]", score)
			}
		})
	}
}

func TestHomeNodeManager_SelectBestNode(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	t.Run("select from multiple nodes", func(t *testing.T) {
		capacities := []*NodeCapacity{
			{
				NodeID:          "node-1",
				DeploymentCount: 50,
				Score:           0.5,
			},
			{
				NodeID:          "node-2",
				DeploymentCount: 10,
				Score:           0.9,
			},
			{
				NodeID:          "node-3",
				DeploymentCount: 80,
				Score:           0.2,
			},
		}

		best := hnm.selectBestNode(capacities)
		if best == nil {
			t.Fatal("expected non-nil best node")
		}

		if best.NodeID != "node-2" {
			t.Errorf("expected node-2 (highest score), got %s", best.NodeID)
		}

		if best.Score != 0.9 {
			t.Errorf("expected score 0.9, got %.2f", best.Score)
		}
	})

	t.Run("return nil for empty list", func(t *testing.T) {
		best := hnm.selectBestNode([]*NodeCapacity{})
		if best != nil {
			t.Error("expected nil for empty capacity list")
		}
	})

	t.Run("single node", func(t *testing.T) {
		capacities := []*NodeCapacity{
			{
				NodeID:          "node-1",
				DeploymentCount: 5,
				Score:           0.8,
			},
		}

		best := hnm.selectBestNode(capacities)
		if best == nil {
			t.Fatal("expected non-nil best node")
		}

		if best.NodeID != "node-1" {
			t.Errorf("expected node-1, got %s", best.NodeID)
		}
	})
}

func TestHomeNodeManager_GetHomeNode(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	ctx := context.Background()

	t.Run("get non-existent assignment", func(t *testing.T) {
		_, err := hnm.GetHomeNode(ctx, "non-existent")
		if err != ErrNamespaceNotAssigned {
			t.Errorf("expected ErrNamespaceNotAssigned, got %v", err)
		}
	})

	t.Run("get existing assignment", func(t *testing.T) {
		// Manually add assignment
		mockDB.assignments["test-namespace"] = "node-123"

		nodeID, err := hnm.GetHomeNode(ctx, "test-namespace")
		if err != nil {
			t.Fatalf("failed to get home node: %v", err)
		}

		if nodeID != "node-123" {
			t.Errorf("expected node-123, got %s", nodeID)
		}
	})
}

func TestHomeNodeManager_MigrateNamespace(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	ctx := context.Background()

	t.Run("migrate namespace to new node", func(t *testing.T) {
		// Set up initial assignment
		mockDB.assignments["test-namespace"] = "node-old"

		// Migrate
		err := hnm.MigrateNamespace(ctx, "test-namespace", "node-new")
		if err != nil {
			t.Fatalf("failed to migrate namespace: %v", err)
		}

		// Verify migration
		nodeID, err := hnm.GetHomeNode(ctx, "test-namespace")
		if err != nil {
			t.Fatalf("failed to get home node after migration: %v", err)
		}

		if nodeID != "node-new" {
			t.Errorf("expected node-new after migration, got %s", nodeID)
		}
	})
}

func TestHomeNodeManager_UpdateHeartbeat(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	ctx := context.Background()

	t.Run("update heartbeat", func(t *testing.T) {
		err := hnm.UpdateHeartbeat(ctx, "test-namespace")
		if err != nil {
			t.Fatalf("failed to update heartbeat: %v", err)
		}
	})
}

func TestHomeNodeManager_UpdateResourceUsage(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	ctx := context.Background()

	t.Run("update resource usage", func(t *testing.T) {
		err := hnm.UpdateResourceUsage(ctx, "test-namespace", 5, 1024, 150)
		if err != nil {
			t.Fatalf("failed to update resource usage: %v", err)
		}
	})
}

func TestCapacityScoreWeighting(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockHomeNodeDB()
	portAllocator := NewPortAllocator(mockDB, logger)
	hnm := NewHomeNodeManager(mockDB, portAllocator, logger)

	t.Run("deployment count has highest weight", func(t *testing.T) {
		// Node with low deployments but high other usage
		score1 := hnm.calculateCapacityScore(10, 5000, 4900, 4000, 200)

		// Node with high deployments but low other usage
		score2 := hnm.calculateCapacityScore(90, 100, 9800, 100, 10)

		// Score1 should be higher because deployment count has 40% weight
		if score1 <= score2 {
			t.Errorf("expected score1 (%.2f) > score2 (%.2f) due to deployment count weight", score1, score2)
		}
	})

	t.Run("deployment count weight matters", func(t *testing.T) {
		// Node A: 20 deployments, 50% other resources
		nodeA := hnm.calculateCapacityScore(20, 4950, 4950, 4096, 200)

		// Node B: 80 deployments, 50% other resources
		nodeB := hnm.calculateCapacityScore(80, 4950, 4950, 4096, 200)

		// Node A should score higher due to lower deployment count
		// (deployment count has 40% weight, so this should make a difference)
		if nodeA <= nodeB {
			t.Errorf("expected node A (%.2f) > node B (%.2f) - deployment count should matter", nodeA, nodeB)
		}

		// Verify the difference is significant (should be about 0.24 = 60% of 40% weight)
		diff := nodeA - nodeB
		if diff < 0.2 {
			t.Errorf("expected significant difference due to deployment count weight, got %.2f", diff)
		}
	})
}
