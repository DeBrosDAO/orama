package namespace

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// mockResult implements sql.Result
type mockResult struct {
	lastInsertID int64
	rowsAffected int64
}

func (m mockResult) LastInsertId() (int64, error) { return m.lastInsertID, nil }
func (m mockResult) RowsAffected() (int64, error) { return m.rowsAffected, nil }

// mockRQLiteClient implements rqlite.Client for testing
type mockRQLiteClient struct {
	queryResults map[string]interface{}
	execResults  map[string]error
	queryCalls   []mockQueryCall
	execCalls    []mockExecCall
}

type mockQueryCall struct {
	Query string
	Args  []interface{}
}

type mockExecCall struct {
	Query string
	Args  []interface{}
}

func newMockRQLiteClient() *mockRQLiteClient {
	return &mockRQLiteClient{
		queryResults: make(map[string]interface{}),
		execResults:  make(map[string]error),
		queryCalls:   make([]mockQueryCall, 0),
		execCalls:    make([]mockExecCall, 0),
	}
}

func (m *mockRQLiteClient) Query(ctx context.Context, dest any, query string, args ...any) error {
	ifaceArgs := make([]interface{}, len(args))
	for i, a := range args {
		ifaceArgs[i] = a
	}
	m.queryCalls = append(m.queryCalls, mockQueryCall{Query: query, Args: ifaceArgs})
	return nil
}

func (m *mockRQLiteClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ifaceArgs := make([]interface{}, len(args))
	for i, a := range args {
		ifaceArgs[i] = a
	}
	m.execCalls = append(m.execCalls, mockExecCall{Query: query, Args: ifaceArgs})
	if err, ok := m.execResults[query]; ok {
		return nil, err
	}
	return mockResult{rowsAffected: 1}, nil
}

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

// Ensure mockRQLiteClient implements rqlite.Client
var _ rqlite.Client = (*mockRQLiteClient)(nil)

func TestPortBlock_PortAssignment(t *testing.T) {
	// Test that port block correctly assigns ports
	block := &PortBlock{
		ID:                  "test-id",
		NodeID:              "node-1",
		NamespaceClusterID:  "cluster-1",
		PortStart:           10000,
		PortEnd:             10004,
		RQLiteHTTPPort:      10000,
		RQLiteRaftPort:      10001,
		OlricHTTPPort:       10002,
		OlricMemberlistPort: 10003,
		GatewayHTTPPort:     10004,
		AllocatedAt:         time.Now(),
	}

	// Verify port assignments
	if block.RQLiteHTTPPort != block.PortStart+0 {
		t.Errorf("RQLiteHTTPPort = %d, want %d", block.RQLiteHTTPPort, block.PortStart+0)
	}
	if block.RQLiteRaftPort != block.PortStart+1 {
		t.Errorf("RQLiteRaftPort = %d, want %d", block.RQLiteRaftPort, block.PortStart+1)
	}
	if block.OlricHTTPPort != block.PortStart+2 {
		t.Errorf("OlricHTTPPort = %d, want %d", block.OlricHTTPPort, block.PortStart+2)
	}
	if block.OlricMemberlistPort != block.PortStart+3 {
		t.Errorf("OlricMemberlistPort = %d, want %d", block.OlricMemberlistPort, block.PortStart+3)
	}
	if block.GatewayHTTPPort != block.PortStart+4 {
		t.Errorf("GatewayHTTPPort = %d, want %d", block.GatewayHTTPPort, block.PortStart+4)
	}
}

func TestPortConstants(t *testing.T) {
	// Verify constants are correctly defined
	if NamespacePortRangeStart != 10000 {
		t.Errorf("NamespacePortRangeStart = %d, want 10000", NamespacePortRangeStart)
	}
	if NamespacePortRangeEnd != 10099 {
		t.Errorf("NamespacePortRangeEnd = %d, want 10099", NamespacePortRangeEnd)
	}
	if PortsPerNamespace != 5 {
		t.Errorf("PortsPerNamespace = %d, want 5", PortsPerNamespace)
	}

	// Verify max namespaces calculation: (10099 - 10000 + 1) / 5 = 100 / 5 = 20
	expectedMax := (NamespacePortRangeEnd - NamespacePortRangeStart + 1) / PortsPerNamespace
	if MaxNamespacesPerNode != expectedMax {
		t.Errorf("MaxNamespacesPerNode = %d, want %d", MaxNamespacesPerNode, expectedMax)
	}
	if MaxNamespacesPerNode != 20 {
		t.Errorf("MaxNamespacesPerNode = %d, want 20", MaxNamespacesPerNode)
	}
}

func TestPortRangeCapacity(t *testing.T) {
	// Test that 20 namespaces fit exactly in the port range
	usedPorts := MaxNamespacesPerNode * PortsPerNamespace
	availablePorts := NamespacePortRangeEnd - NamespacePortRangeStart + 1

	if usedPorts > availablePorts {
		t.Errorf("Port range overflow: %d ports needed for %d namespaces, but only %d available",
			usedPorts, MaxNamespacesPerNode, availablePorts)
	}

	// Verify no wasted ports
	if usedPorts != availablePorts {
		t.Logf("Note: %d ports unused in range", availablePorts-usedPorts)
	}
}

func TestPortBlockAllocation_SequentialBlocks(t *testing.T) {
	// Verify that sequential port blocks don't overlap
	blocks := make([]*PortBlock, MaxNamespacesPerNode)

	for i := 0; i < MaxNamespacesPerNode; i++ {
		portStart := NamespacePortRangeStart + (i * PortsPerNamespace)
		blocks[i] = &PortBlock{
			PortStart:           portStart,
			PortEnd:             portStart + PortsPerNamespace - 1,
			RQLiteHTTPPort:      portStart + 0,
			RQLiteRaftPort:      portStart + 1,
			OlricHTTPPort:       portStart + 2,
			OlricMemberlistPort: portStart + 3,
			GatewayHTTPPort:     portStart + 4,
		}
	}

	// Verify no overlap between consecutive blocks
	for i := 0; i < len(blocks)-1; i++ {
		if blocks[i].PortEnd >= blocks[i+1].PortStart {
			t.Errorf("Block %d (end=%d) overlaps with block %d (start=%d)",
				i, blocks[i].PortEnd, i+1, blocks[i+1].PortStart)
		}
	}

	// Verify last block doesn't exceed range
	lastBlock := blocks[len(blocks)-1]
	if lastBlock.PortEnd > NamespacePortRangeEnd {
		t.Errorf("Last block exceeds port range: end=%d, max=%d",
			lastBlock.PortEnd, NamespacePortRangeEnd)
	}
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
			err:      errors.New("UNIQUE constraint failed"),
			expected: true,
		},
		{
			name:     "constraint violation",
			err:      errors.New("constraint violation"),
			expected: true,
		},
		{
			name:     "conflict error",
			err:      errors.New("conflict detected"),
			expected: true,
		},
		{
			name:     "regular error",
			err:      errors.New("connection timeout"),
			expected: false,
		},
		{
			name:     "empty error",
			err:      errors.New(""),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConflictError(tt.err)
			if result != tt.expected {
				t.Errorf("isConflictError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"", "", true},
		{"hello", "", true},
		{"", "hello", false},
		{"UNIQUE constraint", "UNIQUE", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.substr, func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

func TestNewNamespacePortAllocator(t *testing.T) {
	mockDB := newMockRQLiteClient()
	logger := zap.NewNop()

	allocator := NewNamespacePortAllocator(mockDB, logger)

	if allocator == nil {
		t.Fatal("NewNamespacePortAllocator returned nil")
	}
}

func TestDefaultClusterSizes(t *testing.T) {
	// Verify default cluster size constants
	if DefaultRQLiteNodeCount != 3 {
		t.Errorf("DefaultRQLiteNodeCount = %d, want 3", DefaultRQLiteNodeCount)
	}
	if DefaultOlricNodeCount != 3 {
		t.Errorf("DefaultOlricNodeCount = %d, want 3", DefaultOlricNodeCount)
	}
	if DefaultGatewayNodeCount != 3 {
		t.Errorf("DefaultGatewayNodeCount = %d, want 3", DefaultGatewayNodeCount)
	}

	// Public namespace should have larger clusters
	if PublicRQLiteNodeCount != 5 {
		t.Errorf("PublicRQLiteNodeCount = %d, want 5", PublicRQLiteNodeCount)
	}
	if PublicOlricNodeCount != 5 {
		t.Errorf("PublicOlricNodeCount = %d, want 5", PublicOlricNodeCount)
	}
}
