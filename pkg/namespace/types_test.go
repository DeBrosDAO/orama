package namespace

import (
	"errors"
	"testing"
	"time"
)

func TestClusterStatus_Values(t *testing.T) {
	// Verify all cluster status values are correct
	tests := []struct {
		status   ClusterStatus
		expected string
	}{
		{ClusterStatusNone, "none"},
		{ClusterStatusProvisioning, "provisioning"},
		{ClusterStatusReady, "ready"},
		{ClusterStatusDegraded, "degraded"},
		{ClusterStatusFailed, "failed"},
		{ClusterStatusDeprovisioning, "deprovisioning"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("ClusterStatus = %s, want %s", tt.status, tt.expected)
			}
		})
	}
}

func TestNodeRole_Values(t *testing.T) {
	// Verify all node role values are correct
	tests := []struct {
		role     NodeRole
		expected string
	}{
		{NodeRoleRQLiteLeader, "rqlite_leader"},
		{NodeRoleRQLiteFollower, "rqlite_follower"},
		{NodeRoleOlric, "olric"},
		{NodeRoleGateway, "gateway"},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if string(tt.role) != tt.expected {
				t.Errorf("NodeRole = %s, want %s", tt.role, tt.expected)
			}
		})
	}
}

func TestNodeStatus_Values(t *testing.T) {
	// Verify all node status values are correct
	tests := []struct {
		status   NodeStatus
		expected string
	}{
		{NodeStatusPending, "pending"},
		{NodeStatusStarting, "starting"},
		{NodeStatusRunning, "running"},
		{NodeStatusStopped, "stopped"},
		{NodeStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("NodeStatus = %s, want %s", tt.status, tt.expected)
			}
		})
	}
}

func TestEventType_Values(t *testing.T) {
	// Verify all event type values are correct
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventProvisioningStarted, "provisioning_started"},
		{EventNodesSelected, "nodes_selected"},
		{EventPortsAllocated, "ports_allocated"},
		{EventRQLiteStarted, "rqlite_started"},
		{EventRQLiteJoined, "rqlite_joined"},
		{EventRQLiteLeaderElected, "rqlite_leader_elected"},
		{EventOlricStarted, "olric_started"},
		{EventOlricJoined, "olric_joined"},
		{EventGatewayStarted, "gateway_started"},
		{EventDNSCreated, "dns_created"},
		{EventClusterReady, "cluster_ready"},
		{EventClusterDegraded, "cluster_degraded"},
		{EventClusterFailed, "cluster_failed"},
		{EventNodeFailed, "node_failed"},
		{EventNodeRecovered, "node_recovered"},
		{EventDeprovisionStarted, "deprovisioning_started"},
		{EventDeprovisioned, "deprovisioned"},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			if string(tt.eventType) != tt.expected {
				t.Errorf("EventType = %s, want %s", tt.eventType, tt.expected)
			}
		})
	}
}

func TestClusterError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ClusterError
		expected string
	}{
		{
			name:     "message only",
			err:      &ClusterError{Message: "something failed"},
			expected: "something failed",
		},
		{
			name:     "message with cause",
			err:      &ClusterError{Message: "operation failed", Cause: errors.New("connection timeout")},
			expected: "operation failed: connection timeout",
		},
		{
			name:     "empty message with cause",
			err:      &ClusterError{Message: "", Cause: errors.New("cause")},
			expected: ": cause",
		},
		{
			name:     "empty message no cause",
			err:      &ClusterError{Message: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if result != tt.expected {
				t.Errorf("Error() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestClusterError_Unwrap(t *testing.T) {
	cause := errors.New("original error")
	err := &ClusterError{
		Message: "wrapped",
		Cause:   cause,
	}

	unwrapped := err.Unwrap()
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}

	// Test with no cause
	errNoCause := &ClusterError{Message: "no cause"}
	if errNoCause.Unwrap() != nil {
		t.Errorf("Unwrap() with no cause should return nil")
	}
}

func TestPredefinedErrors(t *testing.T) {
	// Test that predefined errors have the correct messages
	tests := []struct {
		name     string
		err      *ClusterError
		expected string
	}{
		{"ErrNoPortsAvailable", ErrNoPortsAvailable, "no ports available on node"},
		{"ErrNodeAtCapacity", ErrNodeAtCapacity, "node has reached maximum namespace instances"},
		{"ErrInsufficientNodes", ErrInsufficientNodes, "insufficient nodes available for cluster"},
		{"ErrClusterNotFound", ErrClusterNotFound, "namespace cluster not found"},
		{"ErrClusterAlreadyExists", ErrClusterAlreadyExists, "namespace cluster already exists"},
		{"ErrProvisioningFailed", ErrProvisioningFailed, "cluster provisioning failed"},
		{"ErrNamespaceNotFound", ErrNamespaceNotFound, "namespace not found"},
		{"ErrInvalidClusterStatus", ErrInvalidClusterStatus, "invalid cluster status for operation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Message != tt.expected {
				t.Errorf("%s.Message = %q, want %q", tt.name, tt.err.Message, tt.expected)
			}
		})
	}
}

func TestNamespaceCluster_Struct(t *testing.T) {
	now := time.Now()
	readyAt := now.Add(5 * time.Minute)

	cluster := &NamespaceCluster{
		ID:               "cluster-123",
		NamespaceID:      42,
		NamespaceName:    "test-namespace",
		Status:           ClusterStatusReady,
		RQLiteNodeCount:  3,
		OlricNodeCount:   3,
		GatewayNodeCount: 3,
		ProvisionedBy:    "admin",
		ProvisionedAt:    now,
		ReadyAt:          &readyAt,
		LastHealthCheck:  nil,
		ErrorMessage:     "",
		RetryCount:       0,
		Nodes:            nil,
	}

	if cluster.ID != "cluster-123" {
		t.Errorf("ID = %s, want cluster-123", cluster.ID)
	}
	if cluster.NamespaceID != 42 {
		t.Errorf("NamespaceID = %d, want 42", cluster.NamespaceID)
	}
	if cluster.Status != ClusterStatusReady {
		t.Errorf("Status = %s, want %s", cluster.Status, ClusterStatusReady)
	}
	if cluster.RQLiteNodeCount != 3 {
		t.Errorf("RQLiteNodeCount = %d, want 3", cluster.RQLiteNodeCount)
	}
}

func TestClusterNode_Struct(t *testing.T) {
	now := time.Now()
	heartbeat := now.Add(-30 * time.Second)

	node := &ClusterNode{
		ID:                  "node-record-123",
		NamespaceClusterID:  "cluster-456",
		NodeID:              "12D3KooWabc123",
		Role:                NodeRoleRQLiteLeader,
		RQLiteHTTPPort:      10000,
		RQLiteRaftPort:      10001,
		OlricHTTPPort:       10002,
		OlricMemberlistPort: 10003,
		GatewayHTTPPort:     10004,
		Status:              NodeStatusRunning,
		ProcessPID:          12345,
		LastHeartbeat:       &heartbeat,
		ErrorMessage:        "",
		RQLiteJoinAddress:   "192.168.1.100:10001",
		OlricPeers:          `["192.168.1.100:10003","192.168.1.101:10003"]`,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if node.Role != NodeRoleRQLiteLeader {
		t.Errorf("Role = %s, want %s", node.Role, NodeRoleRQLiteLeader)
	}
	if node.Status != NodeStatusRunning {
		t.Errorf("Status = %s, want %s", node.Status, NodeStatusRunning)
	}
	if node.RQLiteHTTPPort != 10000 {
		t.Errorf("RQLiteHTTPPort = %d, want 10000", node.RQLiteHTTPPort)
	}
	if node.ProcessPID != 12345 {
		t.Errorf("ProcessPID = %d, want 12345", node.ProcessPID)
	}
}

func TestClusterProvisioningStatus_Struct(t *testing.T) {
	now := time.Now()
	readyAt := now.Add(2 * time.Minute)

	status := &ClusterProvisioningStatus{
		ClusterID:    "cluster-789",
		Namespace:    "my-namespace",
		Status:       ClusterStatusProvisioning,
		Nodes:        []string{"node-1", "node-2", "node-3"},
		RQLiteReady:  true,
		OlricReady:   true,
		GatewayReady: false,
		DNSReady:     false,
		Error:        "",
		CreatedAt:    now,
		ReadyAt:      &readyAt,
	}

	if status.ClusterID != "cluster-789" {
		t.Errorf("ClusterID = %s, want cluster-789", status.ClusterID)
	}
	if len(status.Nodes) != 3 {
		t.Errorf("len(Nodes) = %d, want 3", len(status.Nodes))
	}
	if !status.RQLiteReady {
		t.Error("RQLiteReady should be true")
	}
	if status.GatewayReady {
		t.Error("GatewayReady should be false")
	}
}

func TestProvisioningResponse_Struct(t *testing.T) {
	resp := &ProvisioningResponse{
		Status:               "provisioning",
		ClusterID:            "cluster-abc",
		PollURL:              "/v1/namespace/status?id=cluster-abc",
		EstimatedTimeSeconds: 120,
	}

	if resp.Status != "provisioning" {
		t.Errorf("Status = %s, want provisioning", resp.Status)
	}
	if resp.ClusterID != "cluster-abc" {
		t.Errorf("ClusterID = %s, want cluster-abc", resp.ClusterID)
	}
	if resp.EstimatedTimeSeconds != 120 {
		t.Errorf("EstimatedTimeSeconds = %d, want 120", resp.EstimatedTimeSeconds)
	}
}

func TestClusterEvent_Struct(t *testing.T) {
	now := time.Now()

	event := &ClusterEvent{
		ID:                 "event-123",
		NamespaceClusterID: "cluster-456",
		EventType:          EventClusterReady,
		NodeID:             "node-1",
		Message:            "Cluster is now ready",
		Metadata:           `{"nodes":["node-1","node-2","node-3"]}`,
		CreatedAt:          now,
	}

	if event.EventType != EventClusterReady {
		t.Errorf("EventType = %s, want %s", event.EventType, EventClusterReady)
	}
	if event.Message != "Cluster is now ready" {
		t.Errorf("Message = %s, want 'Cluster is now ready'", event.Message)
	}
}

func TestPortBlock_Struct(t *testing.T) {
	now := time.Now()

	block := &PortBlock{
		ID:                  "port-block-123",
		NodeID:              "node-456",
		NamespaceClusterID:  "cluster-789",
		PortStart:           10000,
		PortEnd:             10004,
		RQLiteHTTPPort:      10000,
		RQLiteRaftPort:      10001,
		OlricHTTPPort:       10002,
		OlricMemberlistPort: 10003,
		GatewayHTTPPort:     10004,
		AllocatedAt:         now,
	}

	// Verify port calculations
	if block.PortEnd-block.PortStart+1 != PortsPerNamespace {
		t.Errorf("Port range size = %d, want %d", block.PortEnd-block.PortStart+1, PortsPerNamespace)
	}

	// Verify each port is within the block
	ports := []int{
		block.RQLiteHTTPPort,
		block.RQLiteRaftPort,
		block.OlricHTTPPort,
		block.OlricMemberlistPort,
		block.GatewayHTTPPort,
	}

	for i, port := range ports {
		if port < block.PortStart || port > block.PortEnd {
			t.Errorf("Port %d (%d) is outside block range [%d, %d]",
				i, port, block.PortStart, block.PortEnd)
		}
	}
}

func TestErrorsImplementError(t *testing.T) {
	// Verify ClusterError implements error interface
	var _ error = &ClusterError{}

	err := &ClusterError{Message: "test error"}
	var errInterface error = err

	if errInterface.Error() != "test error" {
		t.Errorf("error interface Error() = %s, want 'test error'", errInterface.Error())
	}
}

func TestErrorsUnwrap(t *testing.T) {
	// Test errors.Is/errors.As compatibility
	cause := errors.New("root cause")
	err := &ClusterError{
		Message: "wrapper",
		Cause:   cause,
	}

	if !errors.Is(err, cause) {
		t.Error("errors.Is should find the wrapped cause")
	}

	// Test unwrap chain
	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Error("errors.Unwrap should return the cause")
	}
}
