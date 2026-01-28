package namespace

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NamespacePortAllocator manages the reserved port range (10000-10099) for namespace services.
// Each namespace instance on a node gets a block of 5 consecutive ports.
type NamespacePortAllocator struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewNamespacePortAllocator creates a new port allocator
func NewNamespacePortAllocator(db rqlite.Client, logger *zap.Logger) *NamespacePortAllocator {
	return &NamespacePortAllocator{
		db:     db,
		logger: logger.With(zap.String("component", "namespace-port-allocator")),
	}
}

// AllocatePortBlock finds and allocates the next available 5-port block on a node.
// Returns an error if the node is at capacity (20 namespace instances).
func (npa *NamespacePortAllocator) AllocatePortBlock(ctx context.Context, nodeID, namespaceClusterID string) (*PortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Check if allocation already exists for this namespace on this node
	existingBlock, err := npa.GetPortBlock(ctx, namespaceClusterID, nodeID)
	if err == nil && existingBlock != nil {
		npa.logger.Debug("Port block already allocated",
			zap.String("node_id", nodeID),
			zap.String("namespace_cluster_id", namespaceClusterID),
			zap.Int("port_start", existingBlock.PortStart),
		)
		return existingBlock, nil
	}

	// Retry logic for handling concurrent allocation conflicts
	maxRetries := 10
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		block, err := npa.tryAllocatePortBlock(internalCtx, nodeID, namespaceClusterID)
		if err == nil {
			npa.logger.Info("Port block allocated successfully",
				zap.String("node_id", nodeID),
				zap.String("namespace_cluster_id", namespaceClusterID),
				zap.Int("port_start", block.PortStart),
				zap.Int("attempt", attempt+1),
			)
			return block, nil
		}

		// If it's a conflict error, retry with exponential backoff
		if isConflictError(err) {
			npa.logger.Debug("Port allocation conflict, retrying",
				zap.String("node_id", nodeID),
				zap.String("namespace_cluster_id", namespaceClusterID),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
			time.Sleep(retryDelay)
			retryDelay *= 2
			continue
		}

		// Other errors are non-retryable
		return nil, err
	}

	return nil, &ClusterError{
		Message: fmt.Sprintf("failed to allocate port block after %d retries", maxRetries),
	}
}

// tryAllocatePortBlock attempts to allocate a port block (single attempt)
func (npa *NamespacePortAllocator) tryAllocatePortBlock(ctx context.Context, nodeID, namespaceClusterID string) (*PortBlock, error) {
	// In dev environments where all nodes share the same IP, we need to track
	// allocations by IP address to avoid port conflicts. First get this node's IP.
	var nodeInfos []struct {
		IPAddress string `db:"ip_address"`
	}
	nodeQuery := `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	if err := npa.db.Query(ctx, &nodeInfos, nodeQuery, nodeID); err != nil || len(nodeInfos) == 0 {
		// Fallback: if we can't get the IP, allocate per node_id only
		npa.logger.Debug("Could not get node IP, falling back to node_id-only allocation",
			zap.String("node_id", nodeID),
		)
	}

	// Query all allocated port blocks. If nodes share the same IP, we need to
	// check allocations by IP address to prevent port conflicts.
	type portRow struct {
		PortStart int `db:"port_start"`
	}

	var allocatedBlocks []portRow
	var query string
	var err error

	if len(nodeInfos) > 0 && nodeInfos[0].IPAddress != "" {
		// Check if other nodes share this IP - if so, allocate globally by IP
		var sameIPCount []struct {
			Count int `db:"count"`
		}
		countQuery := `SELECT COUNT(DISTINCT id) as count FROM dns_nodes WHERE ip_address = ?`
		if err := npa.db.Query(ctx, &sameIPCount, countQuery, nodeInfos[0].IPAddress); err == nil && len(sameIPCount) > 0 && sameIPCount[0].Count > 1 {
			// Multiple nodes share this IP (dev environment) - allocate globally
			query = `
				SELECT npa.port_start
				FROM namespace_port_allocations npa
				JOIN dns_nodes dn ON npa.node_id = dn.id
				WHERE dn.ip_address = ?
				ORDER BY npa.port_start ASC
			`
			err = npa.db.Query(ctx, &allocatedBlocks, query, nodeInfos[0].IPAddress)
			npa.logger.Debug("Multiple nodes share IP, allocating globally",
				zap.String("ip_address", nodeInfos[0].IPAddress),
				zap.Int("same_ip_nodes", sameIPCount[0].Count),
			)
		} else {
			// Single node per IP (production) - allocate per node
			query = `SELECT port_start FROM namespace_port_allocations WHERE node_id = ? ORDER BY port_start ASC`
			err = npa.db.Query(ctx, &allocatedBlocks, query, nodeID)
		}
	} else {
		// No IP info - allocate per node_id
		query = `SELECT port_start FROM namespace_port_allocations WHERE node_id = ? ORDER BY port_start ASC`
		err = npa.db.Query(ctx, &allocatedBlocks, query, nodeID)
	}

	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query allocated ports",
			Cause:   err,
		}
	}

	// Build map of allocated block starts
	allocatedStarts := make(map[int]bool)
	for _, row := range allocatedBlocks {
		allocatedStarts[row.PortStart] = true
	}

	// Check node capacity
	if len(allocatedBlocks) >= MaxNamespacesPerNode {
		return nil, ErrNodeAtCapacity
	}

	// Find first available port block
	portStart := -1
	for start := NamespacePortRangeStart; start <= NamespacePortRangeEnd-PortsPerNamespace+1; start += PortsPerNamespace {
		if !allocatedStarts[start] {
			portStart = start
			break
		}
	}

	if portStart < 0 {
		return nil, ErrNoPortsAvailable
	}

	// Create port block
	block := &PortBlock{
		ID:                  uuid.New().String(),
		NodeID:              nodeID,
		NamespaceClusterID:  namespaceClusterID,
		PortStart:           portStart,
		PortEnd:             portStart + PortsPerNamespace - 1,
		RQLiteHTTPPort:      portStart + 0,
		RQLiteRaftPort:      portStart + 1,
		OlricHTTPPort:       portStart + 2,
		OlricMemberlistPort: portStart + 3,
		GatewayHTTPPort:     portStart + 4,
		AllocatedAt:         time.Now(),
	}

	// Attempt to insert allocation record
	insertQuery := `
		INSERT INTO namespace_port_allocations (
			id, node_id, namespace_cluster_id, port_start, port_end,
			rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port,
			allocated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = npa.db.Exec(ctx, insertQuery,
		block.ID,
		block.NodeID,
		block.NamespaceClusterID,
		block.PortStart,
		block.PortEnd,
		block.RQLiteHTTPPort,
		block.RQLiteRaftPort,
		block.OlricHTTPPort,
		block.OlricMemberlistPort,
		block.GatewayHTTPPort,
		block.AllocatedAt,
	)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to insert port allocation",
			Cause:   err,
		}
	}

	return block, nil
}

// DeallocatePortBlock releases a port block when a namespace is deprovisioned
func (npa *NamespacePortAllocator) DeallocatePortBlock(ctx context.Context, namespaceClusterID, nodeID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?`
	_, err := npa.db.Exec(internalCtx, query, namespaceClusterID, nodeID)
	if err != nil {
		return &ClusterError{
			Message: "failed to deallocate port block",
			Cause:   err,
		}
	}

	npa.logger.Info("Port block deallocated",
		zap.String("namespace_cluster_id", namespaceClusterID),
		zap.String("node_id", nodeID),
	)

	return nil
}

// DeallocateAllPortBlocks releases all port blocks for a namespace cluster
func (npa *NamespacePortAllocator) DeallocateAllPortBlocks(ctx context.Context, namespaceClusterID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM namespace_port_allocations WHERE namespace_cluster_id = ?`
	_, err := npa.db.Exec(internalCtx, query, namespaceClusterID)
	if err != nil {
		return &ClusterError{
			Message: "failed to deallocate all port blocks",
			Cause:   err,
		}
	}

	npa.logger.Info("All port blocks deallocated",
		zap.String("namespace_cluster_id", namespaceClusterID),
	)

	return nil
}

// GetPortBlock retrieves the port block for a namespace on a specific node
func (npa *NamespacePortAllocator) GetPortBlock(ctx context.Context, namespaceClusterID, nodeID string) (*PortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var blocks []PortBlock
	query := `
		SELECT id, node_id, namespace_cluster_id, port_start, port_end,
			   rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port,
			   allocated_at
		FROM namespace_port_allocations
		WHERE namespace_cluster_id = ? AND node_id = ?
		LIMIT 1
	`
	err := npa.db.Query(internalCtx, &blocks, query, namespaceClusterID, nodeID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query port block",
			Cause:   err,
		}
	}

	if len(blocks) == 0 {
		return nil, nil
	}

	return &blocks[0], nil
}

// GetAllPortBlocks retrieves all port blocks for a namespace cluster
func (npa *NamespacePortAllocator) GetAllPortBlocks(ctx context.Context, namespaceClusterID string) ([]PortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var blocks []PortBlock
	query := `
		SELECT id, node_id, namespace_cluster_id, port_start, port_end,
			   rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port,
			   allocated_at
		FROM namespace_port_allocations
		WHERE namespace_cluster_id = ?
		ORDER BY port_start ASC
	`
	err := npa.db.Query(internalCtx, &blocks, query, namespaceClusterID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query port blocks",
			Cause:   err,
		}
	}

	return blocks, nil
}

// GetNodeCapacity returns how many more namespace instances a node can host
func (npa *NamespacePortAllocator) GetNodeCapacity(ctx context.Context, nodeID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type countResult struct {
		Count int `db:"count"`
	}

	var results []countResult
	query := `SELECT COUNT(*) as count FROM namespace_port_allocations WHERE node_id = ?`
	err := npa.db.Query(internalCtx, &results, query, nodeID)
	if err != nil {
		return 0, &ClusterError{
			Message: "failed to count allocated port blocks",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return MaxNamespacesPerNode, nil
	}

	allocated := results[0].Count
	available := MaxNamespacesPerNode - allocated

	if available < 0 {
		available = 0
	}

	return available, nil
}

// GetNodeAllocationCount returns the number of namespace instances on a node
func (npa *NamespacePortAllocator) GetNodeAllocationCount(ctx context.Context, nodeID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type countResult struct {
		Count int `db:"count"`
	}

	var results []countResult
	query := `SELECT COUNT(*) as count FROM namespace_port_allocations WHERE node_id = ?`
	err := npa.db.Query(internalCtx, &results, query, nodeID)
	if err != nil {
		return 0, &ClusterError{
			Message: "failed to count allocated port blocks",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, nil
	}

	return results[0].Count, nil
}

// isConflictError checks if an error is due to a constraint violation
func isConflictError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "UNIQUE") || contains(errStr, "constraint") || contains(errStr, "conflict")
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
