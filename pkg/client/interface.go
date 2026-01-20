package client

import (
	"context"
	"io"
	"time"
)

// NetworkClient provides the main interface for applications to interact with the network
type NetworkClient interface {
	// Database operations (namespaced per app)
	Database() DatabaseClient

	// Pub/Sub messaging
	PubSub() PubSubClient

	// Network information
	Network() NetworkInfo

	// Storage operations (IPFS)
	Storage() StorageClient

	// Lifecycle
	Connect() error
	Disconnect() error
	Health() (*HealthStatus, error)

	// Config access (snapshot copy)
	Config() *ClientConfig
}

// DatabaseClient provides database operations for applications
type DatabaseClient interface {
	Query(ctx context.Context, sql string, args ...interface{}) (*QueryResult, error)
	Transaction(ctx context.Context, queries []string) error
	CreateTable(ctx context.Context, schema string) error
	DropTable(ctx context.Context, tableName string) error
	GetSchema(ctx context.Context) (*SchemaInfo, error)
}

// PubSubClient provides publish/subscribe messaging
type PubSubClient interface {
	Subscribe(ctx context.Context, topic string, handler MessageHandler) error
	Publish(ctx context.Context, topic string, data []byte) error
	Unsubscribe(ctx context.Context, topic string) error
	ListTopics(ctx context.Context) ([]string, error)
}

// NetworkInfo provides network status and peer information
type NetworkInfo interface {
	GetPeers(ctx context.Context) ([]PeerInfo, error)
	GetStatus(ctx context.Context) (*NetworkStatus, error)
	ConnectToPeer(ctx context.Context, peerAddr string) error
	DisconnectFromPeer(ctx context.Context, peerID string) error
}

// StorageClient provides IPFS storage operations
type StorageClient interface {
	// Upload uploads content to IPFS and pins it
	Upload(ctx context.Context, reader io.Reader, name string) (*StorageUploadResult, error)

	// Pin pins an existing CID
	Pin(ctx context.Context, cid string, name string) (*StoragePinResult, error)

	// Status gets the pin status for a CID
	Status(ctx context.Context, cid string) (*StorageStatus, error)

	// Get retrieves content from IPFS by CID
	Get(ctx context.Context, cid string) (io.ReadCloser, error)

	// Unpin removes a pin from a CID
	Unpin(ctx context.Context, cid string) error
}

// MessageHandler is called when a pub/sub message is received
type MessageHandler func(topic string, data []byte) error

// Data structures

// QueryResult represents the result of a database query
type QueryResult struct {
	Columns []string        `json:"columns"`
	Rows    [][]interface{} `json:"rows"`
	Count   int64           `json:"count"`
}

// SchemaInfo contains database schema information
type SchemaInfo struct {
	Tables []TableInfo `json:"tables"`
}

// TableInfo contains information about a database table
type TableInfo struct {
	Name    string       `json:"name"`
	Columns []ColumnInfo `json:"columns"`
}

// ColumnInfo contains information about a table column
type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
}

// PeerInfo contains information about a network peer
type PeerInfo struct {
	ID        string    `json:"id"`
	Addresses []string  `json:"addresses"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
}

// NetworkStatus contains overall network status
type NetworkStatus struct {
	NodeID       string               `json:"node_id"`
	PeerID       string               `json:"peer_id"`
	Connected    bool                 `json:"connected"`
	PeerCount    int                  `json:"peer_count"`
	DatabaseSize int64                `json:"database_size"`
	Uptime       time.Duration        `json:"uptime"`
	IPFS         *IPFSPeerInfo        `json:"ipfs,omitempty"`
	IPFSCluster  *IPFSClusterPeerInfo `json:"ipfs_cluster,omitempty"`
}

// IPFSPeerInfo contains IPFS peer information for discovery
type IPFSPeerInfo struct {
	PeerID         string   `json:"peer_id"`
	SwarmAddresses []string `json:"swarm_addresses"`
}

// IPFSClusterPeerInfo contains IPFS Cluster peer information for cluster discovery
type IPFSClusterPeerInfo struct {
	PeerID    string   `json:"peer_id"`    // Cluster peer ID (different from IPFS peer ID)
	Addresses []string `json:"addresses"`  // Cluster multiaddresses (e.g., /ip4/x.x.x.x/tcp/9098)
}

// HealthStatus contains health check information
type HealthStatus struct {
	Status       string            `json:"status"` // "healthy", "degraded", "unhealthy"
	Checks       map[string]string `json:"checks"`
	LastUpdated  time.Time         `json:"last_updated"`
	ResponseTime time.Duration     `json:"response_time"`
}

// StorageUploadResult represents the result of uploading content to IPFS
type StorageUploadResult struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// StoragePinResult represents the result of pinning a CID
type StoragePinResult struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
}

// StorageStatus represents the status of a pinned CID
type StorageStatus struct {
	Cid               string   `json:"cid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"` // "pinned", "pinning", "queued", "unpinned", "error"
	ReplicationMin    int      `json:"replication_min"`
	ReplicationMax    int      `json:"replication_max"`
	ReplicationFactor int      `json:"replication_factor"`
	Peers             []string `json:"peers"`
	Error             string   `json:"error,omitempty"`
}
