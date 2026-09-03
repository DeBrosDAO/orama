package discovery

import (
	"time"
)

// ServiceStatus represents the health of an individual service on a node.
type ServiceStatus struct {
	Name    string `json:"name"`              // e.g. "rqlite", "gateway", "olric"
	Running bool   `json:"running"`           // whether the process is up
	Healthy bool   `json:"healthy"`           // whether it passed its health check
	Message string `json:"message,omitempty"` // optional detail ("leader", "follower", etc.)
}

// NamespaceStatus represents a namespace's status on a node.
type NamespaceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "healthy", "degraded", "recovering"
}

// RQLiteNodeMetadata contains node information announced via LibP2P peer exchange.
// This struct is the single source of truth for node metadata propagated through
// the cluster. Go's json.Unmarshal silently ignores unknown fields, so old nodes
// reading metadata from new nodes simply skip the new fields — no protocol
// version change is needed.
type RQLiteNodeMetadata struct {
	// --- Existing fields (unchanged) ---

	NodeID         string    `json:"node_id"`         // RQLite node ID (raft address)
	RaftAddress    string    `json:"raft_address"`    // Raft port address (e.g., "10.0.0.1:10101")
	HTTPAddress    string    `json:"http_address"`    // HTTP API address (e.g., "10.0.0.1:10100")
	NodeType       string    `json:"node_type"`       // Node type identifier
	RaftLogIndex   uint64    `json:"raft_log_index"`  // Current Raft log index (for data comparison)
	LastSeen       time.Time `json:"last_seen"`       // Updated on every announcement
	ClusterVersion string    `json:"cluster_version"` // For compatibility checking

	// --- New: Identity ---

	// PeerID is the LibP2P peer ID of the node. Used for metadata authentication:
	// on receipt, the receiver verifies PeerID == stream sender to prevent spoofing.
	PeerID string `json:"peer_id,omitempty"`

	// WireGuardIP is the node's WireGuard VPN address (e.g., "10.0.0.1").
	WireGuardIP string `json:"wireguard_ip,omitempty"`

	// --- New: Lifecycle ---

	// LifecycleState is the node's current lifecycle state:
	// "joining", "active", "draining", or "maintenance".
	// Zero value (empty string) from old nodes is treated as "active".
	LifecycleState string `json:"lifecycle_state,omitempty"`

	// MaintenanceTTL is the time at which maintenance mode expires.
	// Only meaningful when LifecycleState == "maintenance".
	MaintenanceTTL time.Time `json:"maintenance_ttl,omitempty"`

	// --- New: Services ---

	// Services reports the status of each service running on the node.
	Services map[string]*ServiceStatus `json:"services,omitempty"`

	// Namespaces reports the status of each namespace on the node.
	Namespaces map[string]*NamespaceStatus `json:"namespaces,omitempty"`

	// --- New: Version ---

	// BinaryVersion is the node's binary version string (e.g., "1.2.3").
	BinaryVersion string `json:"binary_version,omitempty"`
}

// EffectiveLifecycleState returns the lifecycle state, defaulting to "active"
// for old nodes that don't populate the field.
func (m *RQLiteNodeMetadata) EffectiveLifecycleState() string {
	if m.LifecycleState == "" {
		return "active"
	}
	return m.LifecycleState
}

// IsInMaintenance returns true if the node has announced maintenance mode.
func (m *RQLiteNodeMetadata) IsInMaintenance() bool {
	return m.EffectiveLifecycleState() == "maintenance"
}

// IsAvailable returns true if the node is in a state that can serve requests.
func (m *RQLiteNodeMetadata) IsAvailable() bool {
	return m.EffectiveLifecycleState() == "active"
}

// IsMaintenanceExpired returns true if the node is in maintenance and the
// TTL has passed. Used by the leader's health monitor to enforce expiry.
func (m *RQLiteNodeMetadata) IsMaintenanceExpired() bool {
	if !m.IsInMaintenance() {
		return false
	}
	return !m.MaintenanceTTL.IsZero() && time.Now().After(m.MaintenanceTTL)
}

// PeerExchangeResponseV2 extends the original response with RQLite metadata.
// Kept for backward compatibility — the V1 PeerExchangeResponse in discovery.go
// already includes the same RQLiteMetadata field, so this is effectively unused.
type PeerExchangeResponseV2 struct {
	Peers          []PeerInfo          `json:"peers"`
	RQLiteMetadata *RQLiteNodeMetadata `json:"rqlite_metadata,omitempty"`
}
