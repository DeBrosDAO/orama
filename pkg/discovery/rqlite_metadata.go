package discovery

import (
	"time"
)

// RQLiteNodeMetadata contains RQLite-specific information announced via LibP2P
type RQLiteNodeMetadata struct {
	NodeID         string    `json:"node_id"`         // RQLite node ID (from config)
	RaftAddress    string    `json:"raft_address"`    // Raft port address (e.g., "51.83.128.181:7001")
	HTTPAddress    string    `json:"http_address"`    // HTTP API address (e.g., "51.83.128.181:5001")
	NodeType       string    `json:"node_type"`       // Node type identifier
	RaftLogIndex   uint64    `json:"raft_log_index"`  // Current Raft log index (for data comparison)
	LastSeen       time.Time `json:"last_seen"`       // Updated on every announcement
	ClusterVersion string    `json:"cluster_version"` // For compatibility checking
}

// PeerExchangeResponseV2 extends the original response with RQLite metadata
type PeerExchangeResponseV2 struct {
	Peers          []PeerInfo          `json:"peers"`
	RQLiteMetadata *RQLiteNodeMetadata `json:"rqlite_metadata,omitempty"`
}
