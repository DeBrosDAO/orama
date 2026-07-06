package storage

import (
	"context"
	"io"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
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
	// Skip if no database client is available (e.g., in tests)
	if h.db == nil {
		return nil
	}

	query := `INSERT INTO ipfs_content_ownership (id, cid, namespace, name, size_bytes, is_pinned, uploaded_at, uploaded_by)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), ?)
		ON CONFLICT(cid, namespace) DO NOTHING`

	id := cid + ":" + namespace // Simple unique ID
	_, err := h.db.Exec(ctx, query, id, cid, namespace, name, sizeBytes, false, uploadedBy)
	return err
}

// quotaRowToInt64 coerces an rqlite scalar (float64 / int64 / int) to int64.
func quotaRowToInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

// getNamespaceStorageBudget returns the namespace's configured storage budget
// in raw (RF-inclusive) bytes from namespace_quotas, or 0 when there is no row
// or a non-positive value — meaning storage is unlimited and quota enforcement
// is skipped. Enforcement is OPT-IN (bugboard #141): a namespace is only capped
// once an operator sets a positive max_storage_bytes.
func (h *Handlers) getNamespaceStorageBudget(ctx context.Context, namespace string) (int64, error) {
	var rows []map[string]interface{}
	if err := h.db.Query(ctx, &rows,
		`SELECT max_storage_bytes FROM namespace_quotas WHERE namespace = ?`, namespace); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return quotaRowToInt64(rows[0]["max_storage_bytes"]), nil
}

// getNamespaceStorageUsage returns the namespace's current LOGICAL stored bytes
// (sum of size_bytes across all owned CIDs). Multiply by the replication factor
// for the raw cluster cost.
func (h *Handlers) getNamespaceStorageUsage(ctx context.Context, namespace string) (int64, error) {
	var rows []map[string]interface{}
	if err := h.db.Query(ctx, &rows,
		`SELECT COALESCE(SUM(size_bytes), 0) AS used FROM ipfs_content_ownership WHERE namespace = ?`, namespace); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return quotaRowToInt64(rows[0]["used"]), nil
}

// storageQuotaExceeded reports whether adding additionalBytes of LOGICAL content
// would push the namespace's RF-inclusive storage over its configured budget
// (bugboard #141). Returns exceeded=false when the namespace has no budget
// (unlimited) or when h.db is nil (test mode). budget/projected are returned for
// the error message. A DB error is surfaced so the caller can decide fail-open.
func (h *Handlers) storageQuotaExceeded(ctx context.Context, namespace string, additionalBytes int64) (exceeded bool, budget, projected int64, err error) {
	if h.db == nil {
		return false, 0, 0, nil
	}
	budget, err = h.getNamespaceStorageBudget(ctx, namespace)
	if err != nil {
		return false, 0, 0, err
	}
	if budget <= 0 {
		return false, 0, 0, nil // not configured → unlimited
	}
	usage, err := h.getNamespaceStorageUsage(ctx, namespace)
	if err != nil {
		return false, budget, 0, err
	}
	rf := int64(h.config.IPFSReplicationFactor)
	if rf < 1 {
		rf = 3
	}
	projected = (usage + additionalBytes) * rf
	return projected > budget, budget, projected, nil
}

// checkCIDOwnership verifies that a namespace owns (has uploaded) a specific CID.
// Returns true if the namespace owns the CID, false otherwise.
func (h *Handlers) checkCIDOwnership(ctx context.Context, cid, namespace string) (bool, error) {
	// Skip if no database client is available (e.g., in tests)
	if h.db == nil {
		return true, nil // Allow access in test mode
	}

	// Add 5-second timeout to prevent hanging on slow RQLite queries
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	h.logger.ComponentDebug(logging.ComponentGeneral, "Querying RQLite for CID ownership",
		zap.String("cid", cid),
		zap.String("namespace", namespace))

	query := `SELECT COUNT(*) as count FROM ipfs_content_ownership WHERE cid = ? AND namespace = ?`

	var result []map[string]interface{}
	if err := h.db.Query(ctx, &result, query, cid, namespace); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "RQLite ownership query failed",
			zap.Error(err),
			zap.String("cid", cid))
		return false, err
	}

	h.logger.ComponentDebug(logging.ComponentGeneral, "RQLite ownership query completed",
		zap.String("cid", cid),
		zap.Int("result_count", len(result)))

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
	// Skip if no database client is available (e.g., in tests)
	if h.db == nil {
		return nil
	}

	query := `UPDATE ipfs_content_ownership SET is_pinned = ? WHERE cid = ? AND namespace = ?`
	_, err := h.db.Exec(ctx, query, isPinned, cid, namespace)
	return err
}
