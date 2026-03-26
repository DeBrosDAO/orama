package contracts

import (
	"context"
	"io"
)

// StorageProvider defines the interface for decentralized storage operations.
// Implementations typically use IPFS Cluster for distributed content storage.
type StorageProvider interface {
	// Add uploads content to the storage network and returns metadata.
	// The content is read from the provided reader and associated with the given name.
	// Returns information about the stored content including its CID (Content IDentifier).
	Add(ctx context.Context, reader io.Reader, name string) (*AddResponse, error)

	// Pin ensures content is persistently stored across the network.
	// The CID identifies the content, name provides a human-readable label,
	// and replicationFactor specifies how many nodes should store the content.
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*PinResponse, error)

	// PinStatus retrieves the current replication status of pinned content.
	// Returns detailed information about which peers are storing the content
	// and the current state of the pin operation.
	PinStatus(ctx context.Context, cid string) (*PinStatus, error)

	// Get retrieves content from the storage network by its CID.
	// The ipfsAPIURL parameter specifies which IPFS API endpoint to query.
	// Returns a ReadCloser that must be closed by the caller.
	Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error)

	// Unpin removes a pin, allowing the content to be garbage collected.
	// This does not immediately delete the content but makes it eligible for removal.
	Unpin(ctx context.Context, cid string) error

	// Health checks if the storage service is operational.
	// Returns an error if the service is unavailable or unhealthy.
	Health(ctx context.Context) error

	// GetPeerCount returns the number of storage peers in the cluster.
	// Useful for monitoring cluster health and connectivity.
	GetPeerCount(ctx context.Context) (int, error)

	// Close gracefully shuts down the storage client and releases resources.
	Close(ctx context.Context) error
}

// AddResponse represents the result of adding content to storage.
type AddResponse struct {
	Name string `json:"name"`
	Cid  string `json:"cid"`
	Size int64  `json:"size"`
}

// PinResponse represents the result of a pin operation.
type PinResponse struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
}

// PinStatus represents the replication status of pinned content.
type PinStatus struct {
	Cid               string   `json:"cid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"` // "pinned", "pinning", "queued", "unpinned", "error"
	ReplicationMin    int      `json:"replication_min"`
	ReplicationMax    int      `json:"replication_max"`
	ReplicationFactor int      `json:"replication_factor"`
	Peers             []string `json:"peers"` // List of peer IDs storing the content
	Error             string   `json:"error,omitempty"`
}
