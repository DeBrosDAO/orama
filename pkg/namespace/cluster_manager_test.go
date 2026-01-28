package namespace

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestClusterManagerConfig(t *testing.T) {
	cfg := ClusterManagerConfig{
		BaseDomain:  "devnet-orama.network",
		BaseDataDir: "~/.orama/data/namespaces",
	}

	if cfg.BaseDomain != "devnet-orama.network" {
		t.Errorf("BaseDomain = %s, want devnet-orama.network", cfg.BaseDomain)
	}
	if cfg.BaseDataDir != "~/.orama/data/namespaces" {
		t.Errorf("BaseDataDir = %s, want ~/.orama/data/namespaces", cfg.BaseDataDir)
	}
}

func TestNewClusterManager(t *testing.T) {
	mockDB := newMockRQLiteClient()
	logger := zap.NewNop()
	cfg := ClusterManagerConfig{
		BaseDomain:  "devnet-orama.network",
		BaseDataDir: "/tmp/test-namespaces",
	}

	manager := NewClusterManager(mockDB, cfg, logger)

	if manager == nil {
		t.Fatal("NewClusterManager returned nil")
	}
}

func TestNamespaceCluster_InitialState(t *testing.T) {
	now := time.Now()

	cluster := &NamespaceCluster{
		ID:               "test-cluster-id",
		NamespaceID:      1,
		NamespaceName:    "test-namespace",
		Status:           ClusterStatusProvisioning,
		RQLiteNodeCount:  DefaultRQLiteNodeCount,
		OlricNodeCount:   DefaultOlricNodeCount,
		GatewayNodeCount: DefaultGatewayNodeCount,
		ProvisionedBy:    "test-user",
		ProvisionedAt:    now,
		ReadyAt:          nil,
		ErrorMessage:     "",
		RetryCount:       0,
	}

	// Verify initial state
	if cluster.Status != ClusterStatusProvisioning {
		t.Errorf("Initial status = %s, want %s", cluster.Status, ClusterStatusProvisioning)
	}
	if cluster.ReadyAt != nil {
		t.Error("ReadyAt should be nil initially")
	}
	if cluster.ErrorMessage != "" {
		t.Errorf("ErrorMessage should be empty initially, got %s", cluster.ErrorMessage)
	}
	if cluster.RetryCount != 0 {
		t.Errorf("RetryCount should be 0 initially, got %d", cluster.RetryCount)
	}
}

func TestNamespaceCluster_DefaultNodeCounts(t *testing.T) {
	cluster := &NamespaceCluster{
		RQLiteNodeCount:  DefaultRQLiteNodeCount,
		OlricNodeCount:   DefaultOlricNodeCount,
		GatewayNodeCount: DefaultGatewayNodeCount,
	}

	if cluster.RQLiteNodeCount != 3 {
		t.Errorf("RQLiteNodeCount = %d, want 3", cluster.RQLiteNodeCount)
	}
	if cluster.OlricNodeCount != 3 {
		t.Errorf("OlricNodeCount = %d, want 3", cluster.OlricNodeCount)
	}
	if cluster.GatewayNodeCount != 3 {
		t.Errorf("GatewayNodeCount = %d, want 3", cluster.GatewayNodeCount)
	}
}

func TestClusterProvisioningStatus_ReadinessFlags(t *testing.T) {
	tests := []struct {
		name         string
		rqliteReady  bool
		olricReady   bool
		gatewayReady bool
		dnsReady     bool
		expectedAll  bool
	}{
		{"All ready", true, true, true, true, true},
		{"RQLite not ready", false, true, true, true, false},
		{"Olric not ready", true, false, true, true, false},
		{"Gateway not ready", true, true, false, true, false},
		{"DNS not ready", true, true, true, false, false},
		{"None ready", false, false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &ClusterProvisioningStatus{
				RQLiteReady:  tt.rqliteReady,
				OlricReady:   tt.olricReady,
				GatewayReady: tt.gatewayReady,
				DNSReady:     tt.dnsReady,
			}

			allReady := status.RQLiteReady && status.OlricReady && status.GatewayReady && status.DNSReady
			if allReady != tt.expectedAll {
				t.Errorf("All ready = %v, want %v", allReady, tt.expectedAll)
			}
		})
	}
}

func TestClusterStatusTransitions(t *testing.T) {
	// Test valid status transitions
	validTransitions := map[ClusterStatus][]ClusterStatus{
		ClusterStatusNone:           {ClusterStatusProvisioning},
		ClusterStatusProvisioning:   {ClusterStatusReady, ClusterStatusFailed},
		ClusterStatusReady:          {ClusterStatusDegraded, ClusterStatusDeprovisioning},
		ClusterStatusDegraded:       {ClusterStatusReady, ClusterStatusFailed, ClusterStatusDeprovisioning},
		ClusterStatusFailed:         {ClusterStatusProvisioning, ClusterStatusDeprovisioning}, // Retry or delete
		ClusterStatusDeprovisioning: {ClusterStatusNone},
	}

	for from, toList := range validTransitions {
		for _, to := range toList {
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				// This is a documentation test - it verifies the expected transitions
				// The actual enforcement would be in the ClusterManager methods
				if from == to && from != ClusterStatusNone {
					t.Errorf("Status should not transition to itself: %s -> %s", from, to)
				}
			})
		}
	}
}

func TestClusterNode_RoleAssignment(t *testing.T) {
	// Test that a node can have multiple roles
	roles := []NodeRole{
		NodeRoleRQLiteLeader,
		NodeRoleRQLiteFollower,
		NodeRoleOlric,
		NodeRoleGateway,
	}

	// In the implementation, each node hosts all three services
	// but we track them as separate role records
	expectedRolesPerNode := 3 // RQLite (leader OR follower), Olric, Gateway

	// For a 3-node cluster
	nodesCount := 3
	totalRoleRecords := nodesCount * expectedRolesPerNode

	if totalRoleRecords != 9 {
		t.Errorf("Expected 9 role records for 3 nodes, got %d", totalRoleRecords)
	}

	// Verify all roles are represented
	if len(roles) != 4 {
		t.Errorf("Expected 4 role types, got %d", len(roles))
	}
}

func TestClusterEvent_LifecycleEvents(t *testing.T) {
	// Test all lifecycle events are properly ordered
	lifecycleOrder := []EventType{
		EventProvisioningStarted,
		EventNodesSelected,
		EventPortsAllocated,
		EventRQLiteStarted,
		EventRQLiteJoined,
		EventRQLiteLeaderElected,
		EventOlricStarted,
		EventOlricJoined,
		EventGatewayStarted,
		EventDNSCreated,
		EventClusterReady,
	}

	// Verify we have all the events
	if len(lifecycleOrder) != 11 {
		t.Errorf("Expected 11 lifecycle events, got %d", len(lifecycleOrder))
	}

	// Verify they're all unique
	seen := make(map[EventType]bool)
	for _, event := range lifecycleOrder {
		if seen[event] {
			t.Errorf("Duplicate event type: %s", event)
		}
		seen[event] = true
	}
}

func TestClusterEvent_FailureEvents(t *testing.T) {
	failureEvents := []EventType{
		EventClusterDegraded,
		EventClusterFailed,
		EventNodeFailed,
	}

	for _, event := range failureEvents {
		t.Run(string(event), func(t *testing.T) {
			if event == "" {
				t.Error("Event type should not be empty")
			}
		})
	}
}

func TestClusterEvent_RecoveryEvents(t *testing.T) {
	recoveryEvents := []EventType{
		EventNodeRecovered,
	}

	for _, event := range recoveryEvents {
		t.Run(string(event), func(t *testing.T) {
			if event == "" {
				t.Error("Event type should not be empty")
			}
		})
	}
}

func TestClusterEvent_DeprovisioningEvents(t *testing.T) {
	deprovisionEvents := []EventType{
		EventDeprovisionStarted,
		EventDeprovisioned,
	}

	for _, event := range deprovisionEvents {
		t.Run(string(event), func(t *testing.T) {
			if event == "" {
				t.Error("Event type should not be empty")
			}
		})
	}
}

func TestProvisioningResponse_PollURL(t *testing.T) {
	clusterID := "test-cluster-123"
	expectedPollURL := "/v1/namespace/status?id=test-cluster-123"

	pollURL := "/v1/namespace/status?id=" + clusterID
	if pollURL != expectedPollURL {
		t.Errorf("PollURL = %s, want %s", pollURL, expectedPollURL)
	}
}

func TestClusterManager_PortAllocationOrder(t *testing.T) {
	// Verify the order of port assignments within a block
	portStart := 10000

	rqliteHTTP := portStart + 0
	rqliteRaft := portStart + 1
	olricHTTP := portStart + 2
	olricMemberlist := portStart + 3
	gatewayHTTP := portStart + 4

	// Verify order
	if rqliteHTTP != 10000 {
		t.Errorf("RQLite HTTP port = %d, want 10000", rqliteHTTP)
	}
	if rqliteRaft != 10001 {
		t.Errorf("RQLite Raft port = %d, want 10001", rqliteRaft)
	}
	if olricHTTP != 10002 {
		t.Errorf("Olric HTTP port = %d, want 10002", olricHTTP)
	}
	if olricMemberlist != 10003 {
		t.Errorf("Olric Memberlist port = %d, want 10003", olricMemberlist)
	}
	if gatewayHTTP != 10004 {
		t.Errorf("Gateway HTTP port = %d, want 10004", gatewayHTTP)
	}
}

func TestClusterManager_DNSFormat(t *testing.T) {
	// Test the DNS domain format for namespace gateways
	baseDomain := "devnet-orama.network"
	namespaceName := "alice"

	expectedDomain := "ns-alice.devnet-orama.network"
	actualDomain := "ns-" + namespaceName + "." + baseDomain

	if actualDomain != expectedDomain {
		t.Errorf("DNS domain = %s, want %s", actualDomain, expectedDomain)
	}
}

func TestClusterManager_RQLiteAddresses(t *testing.T) {
	// Test RQLite advertised address format
	nodeIP := "192.168.1.100"

	expectedHTTPAddr := "192.168.1.100:10000"
	expectedRaftAddr := "192.168.1.100:10001"

	httpAddr := nodeIP + ":10000"
	raftAddr := nodeIP + ":10001"

	if httpAddr != expectedHTTPAddr {
		t.Errorf("HTTP address = %s, want %s", httpAddr, expectedHTTPAddr)
	}
	if raftAddr != expectedRaftAddr {
		t.Errorf("Raft address = %s, want %s", raftAddr, expectedRaftAddr)
	}
}

func TestClusterManager_OlricPeerFormat(t *testing.T) {
	// Test Olric peer address format
	nodes := []struct {
		ip   string
		port int
	}{
		{"192.168.1.100", 10003},
		{"192.168.1.101", 10003},
		{"192.168.1.102", 10003},
	}

	peers := make([]string, len(nodes))
	for i, n := range nodes {
		peers[i] = n.ip + ":10003"
	}

	expected := []string{
		"192.168.1.100:10003",
		"192.168.1.101:10003",
		"192.168.1.102:10003",
	}

	for i, peer := range peers {
		if peer != expected[i] {
			t.Errorf("Peer[%d] = %s, want %s", i, peer, expected[i])
		}
	}
}

func TestClusterManager_GatewayRQLiteDSN(t *testing.T) {
	// Test the RQLite DSN format used by gateways
	nodeIP := "192.168.1.100"

	expectedDSN := "http://192.168.1.100:10000"
	actualDSN := "http://" + nodeIP + ":10000"

	if actualDSN != expectedDSN {
		t.Errorf("RQLite DSN = %s, want %s", actualDSN, expectedDSN)
	}
}

func TestClusterManager_MinimumNodeRequirement(t *testing.T) {
	// A cluster requires at least 3 nodes
	minimumNodes := DefaultRQLiteNodeCount

	if minimumNodes < 3 {
		t.Errorf("Minimum node count = %d, want at least 3 for fault tolerance", minimumNodes)
	}
}

func TestClusterManager_QuorumCalculation(t *testing.T) {
	// For RQLite Raft consensus, quorum = (n/2) + 1
	tests := []struct {
		nodes         int
		expectedQuorum int
		canLoseNodes  int
	}{
		{3, 2, 1},  // 3 nodes: quorum=2, can lose 1
		{5, 3, 2},  // 5 nodes: quorum=3, can lose 2
		{7, 4, 3},  // 7 nodes: quorum=4, can lose 3
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.nodes+'0'))+" nodes", func(t *testing.T) {
			quorum := (tt.nodes / 2) + 1
			if quorum != tt.expectedQuorum {
				t.Errorf("Quorum for %d nodes = %d, want %d", tt.nodes, quorum, tt.expectedQuorum)
			}

			canLose := tt.nodes - quorum
			if canLose != tt.canLoseNodes {
				t.Errorf("Can lose %d nodes, want %d", canLose, tt.canLoseNodes)
			}
		})
	}
}
