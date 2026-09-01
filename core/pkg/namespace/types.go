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
	NodeRoleSFU            NodeRole = "sfu"
	NodeRoleTURN           NodeRole = "turn"
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
	EventRecoveryStarted     EventType = "recovery_started"
	EventNodeReplaced        EventType = "node_replaced"
	EventRecoveryComplete    EventType = "recovery_complete"
	EventRecoveryFailed      EventType = "recovery_failed"
	EventWebRTCEnabled       EventType = "webrtc_enabled"
	EventWebRTCDisabled      EventType = "webrtc_disabled"
	EventSFUStarted          EventType = "sfu_started"
	EventSFUStopped          EventType = "sfu_stopped"
	EventTURNStarted         EventType = "turn_started"
	EventTURNStopped         EventType = "turn_stopped"
)

// Port allocation constants
const (
	// NamespacePortRangeStart is the beginning of the reserved port range for namespace services
	NamespacePortRangeStart = 10000

	// NamespacePortRangeEnd is the end of the reserved port range for namespace services
	NamespacePortRangeEnd = 10099

	// PortsPerNamespace is the tenant-default port block size (rqlite+olric+gateway).
	// Must equal BlueprintTenant().PortNeedCount(). Other blueprints may use fewer.
	// RQLite HTTP (0), RQLite Raft (1), Olric HTTP (2), Olric Memberlist (3), Gateway HTTP (4)
	PortsPerNamespace = 5

	// MaxNamespacesPerNode is how many tenant-default (5-port) instances fit in 10000–10099.
	MaxNamespacesPerNode = (NamespacePortRangeEnd - NamespacePortRangeStart + 1) / PortsPerNamespace // 20

	// Index internals stay on today's host ports until the renumber phase.
	// Do not place these in the tenant pool (10000–10099).
	IndexRQLiteHTTPPort      = 5001
	IndexRQLiteRaftPort      = 7001
	IndexOlricHTTPPort       = 3320
	IndexOlricMemberlistPort = 3322
	IndexGatewayHTTPPort     = 6001
	// IndexPubsubPort is the localhost HTTP API for the app GossipSub mesh.
	// Phase 8 keeps this number when internals move to 10100+.
	IndexPubsubPort = 10105

	// Host-stack edge / singleton ports. Unchanged by the 10100 renumber.
	IndexWireGuardPort      = 51820
	IndexIPFSAPIPort        = 4501
	IndexIPFSClusterAPIPort = 9094
	IndexVaultPort          = 7500
	IndexCaddyHTTPPort      = 80
	IndexCaddyHTTPSPort     = 443
	IndexNtfyPort           = 8090
	IndexAnyoneSOCKSPort    = 9050
)

// WebRTC port allocation constants
// These are separate from the core namespace port range (10000-10099)
// to avoid breaking existing port blocks.
const (
	// SFU media port range: 20000-29999
	// Each namespace gets a 500-port sub-range for RTP media
	SFUMediaPortRangeStart    = 20000
	SFUMediaPortRangeEnd      = 29999
	SFUMediaPortsPerNamespace = 500

	// SFU signaling ports: 30000-30099
	// Each namespace gets 1 signaling port per node
	SFUSignalingPortRangeStart = 30000
	SFUSignalingPortRangeEnd   = 30099

	// TURN relay port range: 49152-65535
	// Each namespace gets an 800-port sub-range for TURN relay
	TURNRelayPortRangeStart    = 49152
	TURNRelayPortRangeEnd      = 65535
	TURNRelayPortsPerNamespace = 800

	// TURN listen ports (standard)
	TURNDefaultPort = 3478
	TURNSPort       = 5349 // TURNS (TURN over TLS on TCP)

	// Default TURN credential TTL in seconds (10 minutes)
	DefaultTURNCredentialTTL = 600

	// Default service counts per namespace
	DefaultSFUNodeCount  = 3 // SFU on all 3 nodes
	DefaultTURNNodeCount = 2 // TURN on 2 of 3 nodes for HA
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
	ID            string        `json:"id" db:"id"`
	NamespaceID   int           `json:"namespace_id" db:"namespace_id"`
	NamespaceName string        `json:"namespace_name" db:"namespace_name"`
	Status        ClusterStatus `json:"status" db:"status"`
	// Per-service replica counts among selected members. They can differ
	// (10 members, RQLite on 3). Today's tenant writes 3/3/3.
	RQLiteNodeCount  int        `json:"rqlite_node_count" db:"rqlite_node_count"`
	OlricNodeCount   int        `json:"olric_node_count" db:"olric_node_count"`
	GatewayNodeCount int        `json:"gateway_node_count" db:"gateway_node_count"`
	ProvisionedBy    string     `json:"provisioned_by" db:"provisioned_by"`
	ProvisionedAt    time.Time  `json:"provisioned_at" db:"provisioned_at"`
	ReadyAt          *time.Time `json:"ready_at,omitempty" db:"ready_at"`
	LastHealthCheck  *time.Time `json:"last_health_check,omitempty" db:"last_health_check"`
	ErrorMessage     string     `json:"error_message,omitempty" db:"error_message"`
	RetryCount       int        `json:"retry_count" db:"retry_count"`

	// Populated by queries, not stored directly
	Nodes []ClusterNode `json:"nodes,omitempty"`
}

// ClusterNode represents a node participating in a namespace cluster
type ClusterNode struct {
	ID                  string     `json:"id" db:"id"`
	NamespaceClusterID  string     `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	NodeID              string     `json:"node_id" db:"node_id"`
	Role                NodeRole   `json:"role" db:"role"`
	RQLiteHTTPPort      int        `json:"rqlite_http_port,omitempty" db:"rqlite_http_port"`
	RQLiteRaftPort      int        `json:"rqlite_raft_port,omitempty" db:"rqlite_raft_port"`
	OlricHTTPPort       int        `json:"olric_http_port,omitempty" db:"olric_http_port"`
	OlricMemberlistPort int        `json:"olric_memberlist_port,omitempty" db:"olric_memberlist_port"`
	GatewayHTTPPort     int        `json:"gateway_http_port,omitempty" db:"gateway_http_port"`
	Status              NodeStatus `json:"status" db:"status"`
	ProcessPID          int        `json:"process_pid,omitempty" db:"process_pid"`
	LastHeartbeat       *time.Time `json:"last_heartbeat,omitempty" db:"last_heartbeat"`
	ErrorMessage        string     `json:"error_message,omitempty" db:"error_message"`
	RQLiteJoinAddress   string     `json:"rqlite_join_address,omitempty" db:"rqlite_join_address"`
	OlricPeers          string     `json:"olric_peers,omitempty" db:"olric_peers"` // JSON array
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

// PortBlock represents an allocated block of ports for a namespace on a node
type PortBlock struct {
	ID                  string    `json:"id" db:"id"`
	NodeID              string    `json:"node_id" db:"node_id"`
	NamespaceClusterID  string    `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	PortStart           int       `json:"port_start" db:"port_start"`
	PortEnd             int       `json:"port_end" db:"port_end"`
	RQLiteHTTPPort      int       `json:"rqlite_http_port" db:"rqlite_http_port"`
	RQLiteRaftPort      int       `json:"rqlite_raft_port" db:"rqlite_raft_port"`
	OlricHTTPPort       int       `json:"olric_http_port" db:"olric_http_port"`
	OlricMemberlistPort int       `json:"olric_memberlist_port" db:"olric_memberlist_port"`
	GatewayHTTPPort     int       `json:"gateway_http_port" db:"gateway_http_port"`
	AllocatedAt         time.Time `json:"allocated_at" db:"allocated_at"`
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
	ErrNoPortsAvailable            = &ClusterError{Message: "no ports available on node"}
	ErrNodeAtCapacity              = &ClusterError{Message: "node has reached maximum namespace instances"}
	ErrInsufficientNodes           = &ClusterError{Message: "insufficient nodes available for cluster"}
	ErrClusterNotFound             = &ClusterError{Message: "namespace cluster not found"}
	ErrClusterAlreadyExists        = &ClusterError{Message: "namespace cluster already exists"}
	ErrProvisioningFailed          = &ClusterError{Message: "cluster provisioning failed"}
	ErrNamespaceNotFound           = &ClusterError{Message: "namespace not found"}
	ErrInvalidClusterStatus        = &ClusterError{Message: "invalid cluster status for operation"}
	ErrRecoveryInProgress          = &ClusterError{Message: "recovery already in progress for this cluster"}
	ErrWebRTCAlreadyEnabled        = &ClusterError{Message: "WebRTC is already enabled for this namespace"}
	ErrWebRTCNotEnabled            = &ClusterError{Message: "WebRTC is not enabled for this namespace"}
	ErrWebRTCStealthAlreadyEnabled = &ClusterError{Message: "WebRTC stealth is already enabled for this namespace"}
	ErrWebRTCStealthNotEnabled     = &ClusterError{Message: "WebRTC stealth is not enabled for this namespace"}
	ErrNoWebRTCPortsAvailable      = &ClusterError{Message: "no WebRTC ports available on node"}
)

// WebRTCConfig represents the per-namespace WebRTC configuration stored in the database
type WebRTCConfig struct {
	ID                 string `json:"id" db:"id"`
	NamespaceClusterID string `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	NamespaceName      string `json:"namespace_name" db:"namespace_name"`
	Enabled            bool   `json:"enabled" db:"enabled"`
	TURNSharedSecret   string `json:"-" db:"turn_shared_secret"` // Never serialize secret to JSON
	TURNCredentialTTL  int    `json:"turn_credential_ttl" db:"turn_credential_ttl"`
	SFUNodeCount       int    `json:"sfu_node_count" db:"sfu_node_count"`
	TURNNodeCount      int    `json:"turn_node_count" db:"turn_node_count"`
	// StealthEnabled gates the censorship-resistant TURNS:443 path (feat-124):
	// stealth cert on the TURN servers, SNI route on :443, and the
	// `turns:<stealth-host>:443` rung in the turn.credentials URI ladder.
	StealthEnabled bool       `json:"stealth_enabled" db:"stealth_enabled"`
	EnabledBy      string     `json:"enabled_by" db:"enabled_by"`
	EnabledAt      time.Time  `json:"enabled_at" db:"enabled_at"`
	DisabledAt     *time.Time `json:"disabled_at,omitempty" db:"disabled_at"`
}

// WebRTCRoom represents an active WebRTC room tracked in the database
type WebRTCRoom struct {
	ID                 string    `json:"id" db:"id"`
	NamespaceClusterID string    `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	NamespaceName      string    `json:"namespace_name" db:"namespace_name"`
	RoomID             string    `json:"room_id" db:"room_id"`
	SFUNodeID          string    `json:"sfu_node_id" db:"sfu_node_id"`
	SFUInternalIP      string    `json:"sfu_internal_ip" db:"sfu_internal_ip"`
	SFUSignalingPort   int       `json:"sfu_signaling_port" db:"sfu_signaling_port"`
	ParticipantCount   int       `json:"participant_count" db:"participant_count"`
	MaxParticipants    int       `json:"max_participants" db:"max_participants"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	LastActivity       time.Time `json:"last_activity" db:"last_activity"`
}

// WebRTCPortBlock represents allocated WebRTC ports for a namespace on a node
type WebRTCPortBlock struct {
	ID                 string `json:"id" db:"id"`
	NodeID             string `json:"node_id" db:"node_id"`
	NamespaceClusterID string `json:"namespace_cluster_id" db:"namespace_cluster_id"`
	ServiceType        string `json:"service_type" db:"service_type"` // "sfu" or "turn"

	// SFU ports
	SFUSignalingPort  int `json:"sfu_signaling_port,omitempty" db:"sfu_signaling_port"`
	SFUMediaPortStart int `json:"sfu_media_port_start,omitempty" db:"sfu_media_port_start"`
	SFUMediaPortEnd   int `json:"sfu_media_port_end,omitempty" db:"sfu_media_port_end"`

	// TURN ports
	TURNListenPort     int `json:"turn_listen_port,omitempty" db:"turn_listen_port"`
	TURNTLSPort        int `json:"turn_tls_port,omitempty" db:"turn_tls_port"`
	TURNRelayPortStart int `json:"turn_relay_port_start,omitempty" db:"turn_relay_port_start"`
	TURNRelayPortEnd   int `json:"turn_relay_port_end,omitempty" db:"turn_relay_port_end"`

	AllocatedAt time.Time `json:"allocated_at" db:"allocated_at"`
}
