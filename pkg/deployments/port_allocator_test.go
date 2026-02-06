package deployments

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// mockRQLiteClient implements a simple in-memory mock for testing
type mockRQLiteClient struct {
	allocations map[string]map[int]string // nodeID -> port -> deploymentID
}

func newMockRQLiteClient() *mockRQLiteClient {
	return &mockRQLiteClient{
		allocations: make(map[string]map[int]string),
	}
}

func (m *mockRQLiteClient) Query(ctx context.Context, dest any, query string, args ...any) error {
	// Determine what type of query based on dest type
	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Ptr {
		return nil
	}

	sliceVal := destVal.Elem()
	if sliceVal.Kind() != reflect.Slice {
		return nil
	}

	elemType := sliceVal.Type().Elem()

	// Handle port allocation queries
	if len(args) > 0 {
		if nodeID, ok := args[0].(string); ok {
			if elemType.Name() == "portRow" {
				// Query for allocated ports
				if nodeAllocs, exists := m.allocations[nodeID]; exists {
					for port := range nodeAllocs {
						portRow := reflect.New(elemType).Elem()
						portRow.FieldByName("Port").SetInt(int64(port))
						sliceVal.Set(reflect.Append(sliceVal, portRow))
					}
				}
				return nil
			}

			if elemType.Name() == "allocation" {
				// Query for specific deployment allocation
				for nid, ports := range m.allocations {
					for port := range ports {
						if nid == nodeID {
							alloc := reflect.New(elemType).Elem()
							alloc.FieldByName("NodeID").SetString(nid)
							alloc.FieldByName("Port").SetInt(int64(port))
							sliceVal.Set(reflect.Append(sliceVal, alloc))
							return nil
						}
					}
				}
				return nil
			}

			if elemType.Name() == "countResult" {
				// Count query
				count := 0
				if nodeAllocs, exists := m.allocations[nodeID]; exists {
					count = len(nodeAllocs)
				}
				countRes := reflect.New(elemType).Elem()
				countRes.FieldByName("Count").SetInt(int64(count))
				sliceVal.Set(reflect.Append(sliceVal, countRes))
				return nil
			}
		}
	}

	return nil
}

func (m *mockRQLiteClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	// Handle INSERT (port allocation)
	if len(args) >= 3 {
		nodeID, _ := args[0].(string)
		port, _ := args[1].(int)
		deploymentID, _ := args[2].(string)

		if m.allocations[nodeID] == nil {
			m.allocations[nodeID] = make(map[int]string)
		}

		// Check for conflict
		if _, exists := m.allocations[nodeID][port]; exists {
			return nil, &DeploymentError{Message: "UNIQUE constraint failed"}
		}

		m.allocations[nodeID][port] = deploymentID
		return nil, nil
	}

	// Handle DELETE (deallocation)
	if len(args) >= 1 {
		deploymentID, _ := args[0].(string)
		for nodeID, ports := range m.allocations {
			for port, allocatedDepID := range ports {
				if allocatedDepID == deploymentID {
					delete(m.allocations[nodeID], port)
					return nil, nil
				}
			}
		}
	}

	return nil, nil
}

// Stub implementations for rqlite.Client interface
func (m *mockRQLiteClient) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return nil
}

func (m *mockRQLiteClient) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return nil
}

func (m *mockRQLiteClient) Save(ctx context.Context, entity any) error {
	return nil
}

func (m *mockRQLiteClient) Remove(ctx context.Context, entity any) error {
	return nil
}

func (m *mockRQLiteClient) Repository(table string) any {
	return nil
}

func (m *mockRQLiteClient) CreateQueryBuilder(table string) *rqlite.QueryBuilder {
	return nil
}

func (m *mockRQLiteClient) Tx(ctx context.Context, fn func(tx rqlite.Tx) error) error {
	return nil
}

func TestPortAllocator_AllocatePort(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	pa := NewPortAllocator(mockDB, logger)

	ctx := context.Background()
	nodeID := "node-test123"

	t.Run("allocate first port", func(t *testing.T) {
		port, err := pa.AllocatePort(ctx, nodeID, "deploy-1")
		if err != nil {
			t.Fatalf("failed to allocate port: %v", err)
		}

		if port != UserMinPort {
			t.Errorf("expected first port to be %d, got %d", UserMinPort, port)
		}
	})

	t.Run("allocate sequential ports", func(t *testing.T) {
		port2, err := pa.AllocatePort(ctx, nodeID, "deploy-2")
		if err != nil {
			t.Fatalf("failed to allocate second port: %v", err)
		}

		if port2 != UserMinPort+1 {
			t.Errorf("expected second port to be %d, got %d", UserMinPort+1, port2)
		}

		port3, err := pa.AllocatePort(ctx, nodeID, "deploy-3")
		if err != nil {
			t.Fatalf("failed to allocate third port: %v", err)
		}

		if port3 != UserMinPort+2 {
			t.Errorf("expected third port to be %d, got %d", UserMinPort+2, port3)
		}
	})

	t.Run("allocate on different node", func(t *testing.T) {
		port, err := pa.AllocatePort(ctx, "node-other", "deploy-4")
		if err != nil {
			t.Fatalf("failed to allocate port on different node: %v", err)
		}

		if port != UserMinPort {
			t.Errorf("expected first port on new node to be %d, got %d", UserMinPort, port)
		}
	})
}

func TestPortAllocator_DeallocatePort(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	pa := NewPortAllocator(mockDB, logger)

	ctx := context.Background()
	nodeID := "node-test123"

	// Allocate some ports
	_, err := pa.AllocatePort(ctx, nodeID, "deploy-1")
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	port2, err := pa.AllocatePort(ctx, nodeID, "deploy-2")
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	t.Run("deallocate port", func(t *testing.T) {
		err := pa.DeallocatePort(ctx, "deploy-1")
		if err != nil {
			t.Fatalf("failed to deallocate port: %v", err)
		}
	})

	t.Run("allocate reuses gap", func(t *testing.T) {
		port, err := pa.AllocatePort(ctx, nodeID, "deploy-3")
		if err != nil {
			t.Fatalf("failed to allocate port: %v", err)
		}

		// Should reuse the gap created by deallocating deploy-1
		if port != UserMinPort {
			t.Errorf("expected port to fill gap at %d, got %d", UserMinPort, port)
		}

		// Next allocation should be after the last allocated port
		port4, err := pa.AllocatePort(ctx, nodeID, "deploy-4")
		if err != nil {
			t.Fatalf("failed to allocate port: %v", err)
		}

		if port4 != port2+1 {
			t.Errorf("expected next sequential port %d, got %d", port2+1, port4)
		}
	})
}

func TestPortAllocator_GetNodePortCount(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	pa := NewPortAllocator(mockDB, logger)

	ctx := context.Background()
	nodeID := "node-test123"

	t.Run("empty node has zero ports", func(t *testing.T) {
		count, err := pa.GetNodePortCount(ctx, nodeID)
		if err != nil {
			t.Fatalf("failed to get port count: %v", err)
		}

		if count != 0 {
			t.Errorf("expected 0 ports, got %d", count)
		}
	})

	t.Run("count after allocations", func(t *testing.T) {
		// Allocate 3 ports
		for i := 0; i < 3; i++ {
			_, err := pa.AllocatePort(ctx, nodeID, "deploy-"+string(rune(i)))
			if err != nil {
				t.Fatalf("failed to allocate port: %v", err)
			}
		}

		count, err := pa.GetNodePortCount(ctx, nodeID)
		if err != nil {
			t.Fatalf("failed to get port count: %v", err)
		}

		if count != 3 {
			t.Errorf("expected 3 ports, got %d", count)
		}
	})
}

func TestPortAllocator_GetAvailablePortCount(t *testing.T) {
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	pa := NewPortAllocator(mockDB, logger)

	ctx := context.Background()
	nodeID := "node-test123"

	totalPorts := MaxPort - UserMinPort + 1

	t.Run("all ports available initially", func(t *testing.T) {
		available, err := pa.GetAvailablePortCount(ctx, nodeID)
		if err != nil {
			t.Fatalf("failed to get available port count: %v", err)
		}

		if available != totalPorts {
			t.Errorf("expected %d available ports, got %d", totalPorts, available)
		}
	})

	t.Run("available decreases after allocation", func(t *testing.T) {
		_, err := pa.AllocatePort(ctx, nodeID, "deploy-1")
		if err != nil {
			t.Fatalf("failed to allocate port: %v", err)
		}

		available, err := pa.GetAvailablePortCount(ctx, nodeID)
		if err != nil {
			t.Fatalf("failed to get available port count: %v", err)
		}

		expected := totalPorts - 1
		if available != expected {
			t.Errorf("expected %d available ports, got %d", expected, available)
		}
	})
}

func TestIsConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "UNIQUE constraint error",
			err:      &DeploymentError{Message: "UNIQUE constraint failed"},
			expected: true,
		},
		{
			name:     "constraint error",
			err:      &DeploymentError{Message: "constraint violation"},
			expected: true,
		},
		{
			name:     "conflict error",
			err:      &DeploymentError{Message: "conflict detected"},
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      &DeploymentError{Message: "network timeout"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConflictError(tt.err)
			if result != tt.expected {
				t.Errorf("isConflictError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "exact match",
			s:        "UNIQUE",
			substr:   "UNIQUE",
			expected: true,
		},
		{
			name:     "substring present",
			s:        "UNIQUE constraint failed",
			substr:   "constraint",
			expected: true,
		},
		{
			name:     "substring not present",
			s:        "network error",
			substr:   "constraint",
			expected: false,
		},
		{
			name:     "empty substring",
			s:        "test",
			substr:   "",
			expected: true,
		},
		{
			name:     "empty string",
			s:        "",
			substr:   "test",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("contains(%q, %q) = %v, expected %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}
