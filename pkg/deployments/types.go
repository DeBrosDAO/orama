// Package deployments provides infrastructure for managing custom deployments
// (static sites, Next.js apps, Go/Node.js backends, and SQLite databases)
package deployments

import (
	"time"
)

// DeploymentType represents the type of deployment
type DeploymentType string

const (
	DeploymentTypeStatic        DeploymentType = "static"          // Static sites (React, Vite)
	DeploymentTypeNextJS        DeploymentType = "nextjs"          // Next.js SSR
	DeploymentTypeNextJSStatic  DeploymentType = "nextjs-static"  // Next.js static export
	DeploymentTypeGoBackend     DeploymentType = "go-backend"     // Go native binary
	DeploymentTypeGoWASM        DeploymentType = "go-wasm"        // Go compiled to WASM
	DeploymentTypeNodeJSBackend DeploymentType = "nodejs-backend" // Node.js/TypeScript backend
)

// DeploymentStatus represents the current state of a deployment
type DeploymentStatus string

const (
	DeploymentStatusDeploying DeploymentStatus = "deploying"
	DeploymentStatusActive    DeploymentStatus = "active"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	DeploymentStatusStopped   DeploymentStatus = "stopped"
	DeploymentStatusUpdating  DeploymentStatus = "updating"
)

// RestartPolicy defines how a deployment should restart on failure
type RestartPolicy string

const (
	RestartPolicyAlways    RestartPolicy = "always"
	RestartPolicyOnFailure RestartPolicy = "on-failure"
	RestartPolicyNever     RestartPolicy = "never"
)

// RoutingType defines how DNS routing works for a deployment
type RoutingType string

const (
	RoutingTypeBalanced     RoutingType = "balanced"      // Load-balanced across nodes
	RoutingTypeNodeSpecific RoutingType = "node_specific" // Specific to one node
)

// Deployment represents a deployed application or service
type Deployment struct {
	ID        string           `json:"id"`
	Namespace string           `json:"namespace"`
	Name      string           `json:"name"`
	Type      DeploymentType   `json:"type"`
	Version   int              `json:"version"`
	Status    DeploymentStatus `json:"status"`

	// Content storage
	ContentCID string `json:"content_cid,omitempty"`
	BuildCID   string `json:"build_cid,omitempty"`

	// Runtime configuration
	HomeNodeID string `json:"home_node_id,omitempty"`
	Port       int    `json:"port,omitempty"`
	Subdomain  string `json:"subdomain,omitempty"`
	Environment map[string]string `json:"environment,omitempty"` // Unmarshaled from JSON

	// Resource limits
	MemoryLimitMB    int `json:"memory_limit_mb"`
	CPULimitPercent  int `json:"cpu_limit_percent"`
	DiskLimitMB      int `json:"disk_limit_mb"`

	// Health & monitoring
	HealthCheckPath     string        `json:"health_check_path,omitempty"`
	HealthCheckInterval int           `json:"health_check_interval"`
	RestartPolicy       RestartPolicy `json:"restart_policy"`
	MaxRestartCount     int           `json:"max_restart_count"`

	// Metadata
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	DeployedBy string    `json:"deployed_by"`
}

// ReplicaStatus represents the status of a deployment replica on a node
type ReplicaStatus string

const (
	ReplicaStatusPending  ReplicaStatus = "pending"
	ReplicaStatusActive   ReplicaStatus = "active"
	ReplicaStatusFailed   ReplicaStatus = "failed"
	ReplicaStatusRemoving ReplicaStatus = "removing"
)

// DefaultReplicaCount is the default number of replicas per deployment
const DefaultReplicaCount = 2

// Replica represents a deployment replica on a specific node
type Replica struct {
	DeploymentID string        `json:"deployment_id"`
	NodeID       string        `json:"node_id"`
	Port         int           `json:"port"`
	Status       ReplicaStatus `json:"status"`
	IsPrimary    bool          `json:"is_primary"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// PortAllocation represents an allocated port on a specific node
type PortAllocation struct {
	NodeID       string    `json:"node_id"`
	Port         int       `json:"port"`
	DeploymentID string    `json:"deployment_id"`
	AllocatedAt  time.Time `json:"allocated_at"`
}

// HomeNodeAssignment maps a namespace to its home node
type HomeNodeAssignment struct {
	Namespace       string    `json:"namespace"`
	HomeNodeID      string    `json:"home_node_id"`
	AssignedAt      time.Time `json:"assigned_at"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	DeploymentCount int       `json:"deployment_count"`
	TotalMemoryMB   int       `json:"total_memory_mb"`
	TotalCPUPercent int       `json:"total_cpu_percent"`
}

// DeploymentDomain represents a custom domain mapping
type DeploymentDomain struct {
	ID                string      `json:"id"`
	DeploymentID      string      `json:"deployment_id"`
	Namespace         string      `json:"namespace"`
	Domain            string      `json:"domain"`
	RoutingType       RoutingType `json:"routing_type"`
	NodeID            string      `json:"node_id,omitempty"`
	IsCustom          bool        `json:"is_custom"`
	TLSCertCID        string      `json:"tls_cert_cid,omitempty"`
	VerifiedAt        *time.Time  `json:"verified_at,omitempty"`
	VerificationToken string      `json:"verification_token,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// DeploymentHistory tracks deployment versions for rollback
type DeploymentHistory struct {
	ID                 string    `json:"id"`
	DeploymentID       string    `json:"deployment_id"`
	Version            int       `json:"version"`
	ContentCID         string    `json:"content_cid,omitempty"`
	BuildCID           string    `json:"build_cid,omitempty"`
	DeployedAt         time.Time `json:"deployed_at"`
	DeployedBy         string    `json:"deployed_by"`
	Status             string    `json:"status"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	RollbackFromVersion *int     `json:"rollback_from_version,omitempty"`
}

// DeploymentEvent represents an audit trail event
type DeploymentEvent struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	EventType    string    `json:"event_type"`
	Message      string    `json:"message,omitempty"`
	Metadata     string    `json:"metadata,omitempty"` // JSON
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
}

// DeploymentHealthCheck represents a health check result
type DeploymentHealthCheck struct {
	ID             string    `json:"id"`
	DeploymentID   string    `json:"deployment_id"`
	NodeID         string    `json:"node_id"`
	Status         string    `json:"status"` // healthy, unhealthy, unknown
	ResponseTimeMS int       `json:"response_time_ms,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
}

// DeploymentRequest represents a request to create a new deployment
type DeploymentRequest struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	Type      DeploymentType `json:"type"`
	Subdomain string         `json:"subdomain,omitempty"`

	// Content
	ContentTarball []byte            `json:"-"` // Binary data, not JSON
	Environment    map[string]string `json:"environment,omitempty"`

	// Resource limits
	MemoryLimitMB   int `json:"memory_limit_mb,omitempty"`
	CPULimitPercent int `json:"cpu_limit_percent,omitempty"`

	// Health monitoring
	HealthCheckPath string `json:"health_check_path,omitempty"`

	// Routing
	LoadBalanced bool   `json:"load_balanced,omitempty"` // Create load-balanced DNS records
	CustomDomain string `json:"custom_domain,omitempty"` // Optional custom domain
}

// DeploymentResponse represents the result of a deployment operation
type DeploymentResponse struct {
	DeploymentID string   `json:"deployment_id"`
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	Status       string   `json:"status"`
	URLs         []string `json:"urls"` // All URLs where deployment is accessible
	Version      int      `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
}

// NodeCapacity represents available resources on a node
type NodeCapacity struct {
	NodeID            string `json:"node_id"`
	DeploymentCount   int    `json:"deployment_count"`
	AllocatedPorts    int    `json:"allocated_ports"`
	AvailablePorts    int    `json:"available_ports"`
	UsedMemoryMB      int    `json:"used_memory_mb"`
	AvailableMemoryMB int    `json:"available_memory_mb"`
	UsedCPUPercent    int    `json:"used_cpu_percent"`
	AvailableDiskMB   int64  `json:"available_disk_mb"`
	Score             float64 `json:"score"` // Calculated capacity score
}

// Port range constants
const (
	MinPort         = 10000 // Minimum allocatable port
	MaxPort         = 19999 // Maximum allocatable port
	ReservedMinPort = 10000 // Start of reserved range
	ReservedMaxPort = 10099 // End of reserved range
	UserMinPort     = 10100 // Start of user-allocatable range
)

// Default resource limits
const (
	DefaultMemoryLimitMB    = 256
	DefaultCPULimitPercent  = 50
	DefaultDiskLimitMB      = 1024
	DefaultHealthCheckInterval = 30 // seconds
	DefaultMaxRestartCount  = 10
)

// Errors
var (
	ErrNoPortsAvailable     = &DeploymentError{Message: "no ports available on node"}
	ErrNoNodesAvailable     = &DeploymentError{Message: "no nodes available for deployment"}
	ErrDeploymentNotFound   = &DeploymentError{Message: "deployment not found"}
	ErrNamespaceNotAssigned = &DeploymentError{Message: "namespace has no home node assigned"}
	ErrInvalidDeploymentType = &DeploymentError{Message: "invalid deployment type"}
	ErrSubdomainTaken       = &DeploymentError{Message: "subdomain already in use"}
	ErrDomainReserved       = &DeploymentError{Message: "domain is reserved"}
)

// DeploymentError represents a deployment-related error
type DeploymentError struct {
	Message string
	Cause   error
}

func (e *DeploymentError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *DeploymentError) Unwrap() error {
	return e.Cause
}
