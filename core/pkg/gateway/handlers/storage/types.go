package storage

// StorageUploadRequest represents a request to upload content to IPFS.
// It supports JSON-based uploads with base64-encoded data.
type StorageUploadRequest struct {
	// Name is the optional filename for the uploaded content
	Name string `json:"name,omitempty"`
	// Data is the base64-encoded content data (alternative to multipart upload)
	Data string `json:"data,omitempty"`
}

// StorageUploadResponse represents the response from uploading content to IPFS.
type StorageUploadResponse struct {
	// Cid is the Content Identifier (hash) of the uploaded content
	Cid string `json:"cid"`
	// Name is the filename associated with the content
	Name string `json:"name"`
	// Size is the size of the uploaded content in bytes
	Size int64 `json:"size"`
}

// StoragePinRequest represents a request to pin a CID in the IPFS cluster.
type StoragePinRequest struct {
	// Cid is the Content Identifier to pin
	Cid string `json:"cid"`
	// Name is an optional human-readable name for the pinned content
	Name string `json:"name,omitempty"`
}

// StoragePinResponse represents the response from pinning a CID.
type StoragePinResponse struct {
	// Cid is the Content Identifier that was pinned
	Cid string `json:"cid"`
	// Name is the human-readable name associated with the pin
	Name string `json:"name"`
}

// StorageStatusResponse represents the status of a pinned CID in the IPFS cluster.
type StorageStatusResponse struct {
	// Cid is the Content Identifier
	Cid string `json:"cid"`
	// Name is the human-readable name associated with the pin
	Name string `json:"name"`
	// Status indicates the pin state (e.g., "pinned", "pinning", "unpinned")
	Status string `json:"status"`
	// ReplicationMin is the minimum number of replicas
	ReplicationMin int `json:"replication_min"`
	// ReplicationMax is the maximum number of replicas
	ReplicationMax int `json:"replication_max"`
	// ReplicationFactor is the desired number of replicas
	ReplicationFactor int `json:"replication_factor"`
	// Peers is the list of peer IDs holding replicas
	Peers []string `json:"peers"`
	// Error contains any error message related to the pin status
	Error string `json:"error,omitempty"`
}
