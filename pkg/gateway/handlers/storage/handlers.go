package storage

import (
	"context"
	"io"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// IPFSClient defines the interface for interacting with IPFS.
// This interface matches the ipfs.IPFSClient implementation.
type IPFSClient interface {
	Add(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error)
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error)
	PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error)
	Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error)
	Unpin(ctx context.Context, cid string) error
}

// Config holds configuration values needed by storage handlers.
type Config struct {
	// IPFSReplicationFactor is the desired number of replicas for pinned content
	IPFSReplicationFactor int
	// IPFSAPIURL is the IPFS API endpoint URL
	IPFSAPIURL string
}

// Handlers provides HTTP handlers for IPFS storage operations.
// It manages file uploads, downloads, pinning, and status checking.
type Handlers struct {
	ipfsClient IPFSClient
	logger     *logging.ColoredLogger
	config     Config
	db         rqlite.Client // For tracking IPFS content ownership
}

// New creates a new storage handlers instance with the provided dependencies.
func New(ipfsClient IPFSClient, logger *logging.ColoredLogger, config Config, db rqlite.Client) *Handlers {
	return &Handlers{
		ipfsClient: ipfsClient,
		logger:     logger,
		config:     config,
		db:         db,
	}
}

// getNamespaceFromContext retrieves the namespace from the request context.
func (h *Handlers) getNamespaceFromContext(ctx context.Context) string {
	if v := ctx.Value(ctxkeys.NamespaceOverride); v != nil {
		if ns, ok := v.(string); ok {
			return ns
		}
	}
	return ""
}

// recordCIDOwnership records that a namespace owns a specific CID in the database.
// This enables namespace isolation for IPFS content.
func (h *Handlers) recordCIDOwnership(ctx context.Context, cid, namespace, name, uploadedBy string, sizeBytes int64) error {
	query := `INSERT INTO ipfs_content_ownership (id, cid, namespace, name, size_bytes, is_pinned, uploaded_at, uploaded_by)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), ?)
		ON CONFLICT(cid, namespace) DO NOTHING`

	id := cid + ":" + namespace // Simple unique ID
	_, err := h.db.Exec(ctx, query, id, cid, namespace, name, sizeBytes, false, uploadedBy)
	return err
}

// checkCIDOwnership verifies that a namespace owns (has uploaded) a specific CID.
// Returns true if the namespace owns the CID, false otherwise.
func (h *Handlers) checkCIDOwnership(ctx context.Context, cid, namespace string) (bool, error) {
	query := `SELECT COUNT(*) as count FROM ipfs_content_ownership WHERE cid = ? AND namespace = ?`

	var result []map[string]interface{}
	if err := h.db.Query(ctx, &result, query, cid, namespace); err != nil {
		return false, err
	}

	if len(result) == 0 {
		return false, nil
	}

	// Extract count value
	count, ok := result[0]["count"].(float64)
	if !ok {
		// Try int64
		countInt, ok := result[0]["count"].(int64)
		if ok {
			count = float64(countInt)
		}
	}

	return count > 0, nil
}

// updatePinStatus updates the pin status for a CID in the ownership table.
func (h *Handlers) updatePinStatus(ctx context.Context, cid, namespace string, isPinned bool) error {
	query := `UPDATE ipfs_content_ownership SET is_pinned = ? WHERE cid = ? AND namespace = ?`
	_, err := h.db.Exec(ctx, query, isPinned, cid, namespace)
	return err
}
