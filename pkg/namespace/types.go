package namespace

import (
	"time"
)

// ClusterStatus represents the current state of a namespace cluster
type ClusterStatus string

const (
	ClusterStatusNone           ClusterStatus = "none"           // No cluster provisioned
	ClusterStatusProvisioning   ClusterStatus = "provisioning"   // Cluster is being provisioned
	ClusterStatusReady          ClusterStatus = "ready"          // Cluster is operational
	ClusterStatusDegraded       ClusterStatus = "degraded"       // Some nodes are unhealthy
	ClusterStatusFailed         ClusterStatus = "failed"         // Cluster failed to provision/operate
	ClusterStatusDeprovisioning ClusterStatus = "deprovisioning" // Cluster is being deprovisioned
)

// NodeRole represents the role of a node in a namespace cluster
type NodeRole string

const (
	NodeRoleRQLiteLeader   NodeRole = "rqlite_leader"
	NodeRoleRQLiteFollower NodeRole = "rqlite_follower"
	NodeRoleOlric          NodeRole = "olric"
	NodeRoleGateway        NodeRole = "gateway"
)

// NodeStatus represents the status of a service on a node
type NodeStatus string

const (
	NodeStatusPending  NodeStatus = "pending"
	NodeStatusStarting NodeStatus = "starting"
	NodeStatusRunning  NodeStatus = "running"
	NodeStatusStopped  NodeStatus = "stopped"
	NodeStatusFailed   NodeStatus = "failed"
)

// EventType represents types of cluster lifecycle events
type EventType string

const (
	EventProvisioningStarted EventType = "provisioning_started"
	EventNodesSelected       EventType = "nodes_selected"
	EventPortsAllocated      EventType = "ports_allocated"
	EventRQLiteStarted       EventType = "rqlite_started"
	EventRQLiteJoined        EventType = "rqlite_joined"
	EventRQLiteLeaderElected EventType = "rqlite_leader_elected"
	EventOlricStarted        EventType = "olric_started"
	EventOlricJoined         EventType = "olric_joined"
	EventGatewayStarted      EventType = "gateway_started"
	EventDNSCreated          EventType = "dns_created"
	EventClusterReady        EventType = "cluster_ready"
	EventClusterDegraded     EventType = "cluster_degraded"
	EventClusterFailed       EventType = "cluster_failed"
	EventNodeFailed          EventType = "node_failed"
	EventNodeRecovered       EventType = "node_recovered"
	EventDeprovisionStarted  EventType = "deprovisioning_started"
	EventDeprovisioned       EventType = "deprovisioned"
)

// Port allocation constants
const (
	// NamespacePortRangeStart is the beginning of the reserved port range for namespace services
	NamespacePortRangeStart = 10000

	// NamespacePortRangeEnd is the end of the reserved port range for namespace services
	NamespacePortRangeEnd = 10099

	// PortsPerNamespace is the number of ports required per namespace instance on a node
	// RQLite HTTP (0), RQLite Raft (1), Olric HTTP (2), Olric Memberlist (3), Gateway HTTP (4)
	PortsPerNamespace = 5

	// MaxNamespacesPerNode is the maximum number of namespace instances a single node can host
	MaxNamespacesPerNode = (NamespacePortRangeEnd - NamespacePortRangeStart + 1) / PortsPerNamespace // 20
)

// Default cluster sizes
const (
	DefaultRQLiteNodeCount  = 3
	DefaultOlricNodeCount   = 3
	DefaultGatewayNodeCount = 3
	PublicRQLiteNodeCount   = 5
	PublicOlricNodeCount    = 5
)

// NamespaceCluster represents a dedicated cluster for a namespace
type NamespaceCluster struct {
	ID               string        `json:"id" db:"id"`
	NamespaceID      int           `json:"namespace_id" db:"namespace_id"`
	NamespaceName    string        `json:"namespace_name" db:"namespace_name"`
	Status           ClusterStatus `json:"status" db:"status"`
	RQLiteNodeCount  int           `json:"rqlite_node_count" db:"rqlite_node_count"`
	OlricNodeCount   int           `json:"olric_node_count" db:"olric_node_count"`
	GatewayNodeCount int           `json:"gateway_node_count" db:"gateway_node_count"`
	ProvisionedBy    string        `json:"provisioned_by" db:"provisioned_by"`
	ProvisionedAt    time.Time     `json:"provisioned_at" db:"provisioned_at"`
	ReadyAt          *time.Time    `json:"ready_at,omitempty" db:"ready_at"`
	LastHealthCheck  *time.Time    `json:"last_health_check,omitempty" db:"last_health_check"`
	ErrorMessage     string        `json:"error_message,omitempty" db:"error_message"`
	RetryCount       int           `json:"retry_count" db:"retry_count"`

	// Populated by queries, not stored directly
	Nodes []ClusterNode `json:"nodes,omitempty"`
}

// ClusterNode represents a node participating in a namespace cluster
type ClusterNode struct {
	ID                 string     `json:"id" db:"id"`
	NamespaceClusterID string     `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	NodeID             string     `json:"node_id" db:"node_id"`
	Role               NodeRole   `json:"role" db:"role"`
	RQLiteHTTPPort     int        `json:"rqlite_http_port,omitempty" db:"rqlite_http_port"`
	RQLiteRaftPort     int        `json:"rqlite_raft_port,omitempty" db:"rqlite_raft_port"`
	OlricHTTPPort      int        `json:"olric_http_port,omitempty" db:"olric_http_port"`
	OlricMemberlistPort int       `json:"olric_memberlist_port,omitempty" db:"olric_memberlist_port"`
	GatewayHTTPPort    int        `json:"gateway_http_port,omitempty" db:"gateway_http_port"`
	Status             NodeStatus `json:"status" db:"status"`
	ProcessPID         int        `json:"process_pid,omitempty" db:"process_pid"`
	LastHeartbeat      *time.Time `json:"last_heartbeat,omitempty" db:"last_heartbeat"`
	ErrorMessage       string     `json:"error_message,omitempty" db:"error_message"`
	RQLiteJoinAddress  string     `json:"rqlite_join_address,omitempty" db:"rqlite_join_address"`
	OlricPeers         string     `json:"olric_peers,omitempty" db:"olric_peers"` // JSON array
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}

// PortBlock represents an allocated block of ports for a namespace on a node
type PortBlock struct {
	ID                 string    `json:"id" db:"id"`
	NodeID             string    `json:"node_id" db:"node_id"`
	NamespaceClusterID string    `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	PortStart          int       `json:"port_start" db:"port_start"`
	PortEnd            int       `json:"port_end" db:"port_end"`
	RQLiteHTTPPort     int       `json:"rqlite_http_port" db:"rqlite_http_port"`
	RQLiteRaftPort     int       `json:"rqlite_raft_port" db:"rqlite_raft_port"`
	OlricHTTPPort      int       `json:"olric_http_port" db:"olric_http_port"`
	OlricMemberlistPort int      `json:"olric_memberlist_port" db:"olric_memberlist_port"`
	GatewayHTTPPort    int       `json:"gateway_http_port" db:"gateway_http_port"`
	AllocatedAt        time.Time `json:"allocated_at" db:"allocated_at"`
}

// ClusterEvent represents an audit event for cluster lifecycle
type ClusterEvent struct {
	ID                 string    `json:"id" db:"id"`
	NamespaceClusterID string    `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	EventType          EventType `json:"event_type" db:"event_type"`
	NodeID             string    `json:"node_id,omitempty" db:"node_id"`
	Message            string    `json:"message,omitempty" db:"message"`
	Metadata           string    `json:"metadata,omitempty" db:"metadata"` // JSON
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
}

// ClusterProvisioningStatus is the response format for the /v1/namespace/status endpoint
type ClusterProvisioningStatus struct {
	ClusterID    string        `json:"cluster_id"`
	Namespace    string        `json:"namespace"`
	Status       ClusterStatus `json:"status"`
	Nodes        []string      `json:"nodes"`
	RQLiteReady  bool          `json:"rqlite_ready"`
	OlricReady   bool          `json:"olric_ready"`
	GatewayReady bool          `json:"gateway_ready"`
	DNSReady     bool          `json:"dns_ready"`
	Error        string        `json:"error,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	ReadyAt      *time.Time    `json:"ready_at,omitempty"`
}

// ProvisioningResponse is returned when a new namespace triggers cluster provisioning
type ProvisioningResponse struct {
	Status               string `json:"status"`
	ClusterID            string `json:"cluster_id"`
	PollURL              string `json:"poll_url"`
	EstimatedTimeSeconds int    `json:"estimated_time_seconds"`
}

// Errors
type ClusterError struct {
	Message string
	Cause   error
}

func (e *ClusterError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *ClusterError) Unwrap() error {
	return e.Cause
}

var (
	ErrNoPortsAvailable      = &ClusterError{Message: "no ports available on node"}
	ErrNodeAtCapacity        = &ClusterError{Message: "node has reached maximum namespace instances"}
	ErrInsufficientNodes     = &ClusterError{Message: "insufficient nodes available for cluster"}
	ErrClusterNotFound       = &ClusterError{Message: "namespace cluster not found"}
	ErrClusterAlreadyExists  = &ClusterError{Message: "namespace cluster already exists"}
	ErrProvisioningFailed    = &ClusterError{Message: "cluster provisioning failed"}
	ErrNamespaceNotFound     = &ClusterError{Message: "namespace not found"}
	ErrInvalidClusterStatus  = &ClusterError{Message: "invalid cluster status for operation"}
)
