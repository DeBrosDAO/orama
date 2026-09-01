package namespace

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NamespacePortAllocator manages the reserved port range (10000-10099) for namespace services.
// Block size is the blueprint's PortNeedCount() (tenant default 5).
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

// AllocatePortBlock finds and allocates a contiguous block sized to bp.PortNeedCount().
func (npa *NamespacePortAllocator) AllocatePortBlock(ctx context.Context, nodeID, namespaceClusterID string, bp Blueprint) (*PortBlock, error) {
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
		block, err := npa.tryAllocatePortBlock(internalCtx, nodeID, namespaceClusterID, bp)
		if err == nil {
			npa.logger.Info("Port block allocated successfully",
				zap.String("node_id", nodeID),
				zap.String("namespace_cluster_id", namespaceClusterID),
				zap.Int("port_start", block.PortStart),
				zap.Int("port_end", block.PortEnd),
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

type allocatedRange struct {
	Start int
	End   int // inclusive
}

// findFreeBlock returns the first rangeStart..rangeEnd inclusive gap of size ports.
func findFreeBlock(allocated []allocatedRange, rangeStart, rangeEnd, size int) (int, bool) {
	if size <= 0 {
		return 0, false
	}
	sorted := append([]allocatedRange(nil), allocated...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	cursor := rangeStart
	for _, r := range sorted {
		if r.End < cursor {
			continue
		}
		if cursor+size-1 < r.Start && cursor+size-1 <= rangeEnd {
			return cursor, true
		}
		if r.End+1 > cursor {
			cursor = r.End + 1
		}
	}
	if cursor+size-1 <= rangeEnd {
		return cursor, true
	}
	return 0, false
}

func portBlockFromBlueprint(nodeID, clusterID string, portStart int, bp Blueprint) *PortBlock {
	size := bp.PortNeedCount()
	if size < 1 {
		size = 1
	}
	block := &PortBlock{
		ID:                 uuid.New().String(),
		NodeID:             nodeID,
		NamespaceClusterID: clusterID,
		PortStart:          portStart,
		PortEnd:            portStart + size - 1,
		AllocatedAt:        time.Now(),
	}
	for _, spec := range bp.Services {
		var ports []int
		for _, p := range spec.PortNeeds {
			if p.Fixed != 0 {
				continue
			}
			ports = append(ports, portStart+p.FromBlock)
		}
		switch spec.Name {
		case ServiceRQLite:
			if len(ports) > 0 {
				block.RQLiteHTTPPort = ports[0]
			}
			if len(ports) > 1 {
				block.RQLiteRaftPort = ports[1]
			}
		case ServiceOlric:
			if len(ports) > 0 {
				block.OlricHTTPPort = ports[0]
			}
			if len(ports) > 1 {
				block.OlricMemberlistPort = ports[1]
			}
		case ServiceGateway:
			if len(ports) > 0 {
				block.GatewayHTTPPort = ports[0]
			}
		}
	}
	return block
}

func (npa *NamespacePortAllocator) allocatedRanges(ctx context.Context, nodeID string) ([]allocatedRange, error) {
	// In dev environments where all nodes share the same IP, track allocations
	// by IP so two node IDs cannot bind the same host port.
	var nodeInfos []struct {
		IPAddress string `db:"ip_address"`
	}
	nodeQuery := `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	if err := npa.db.Query(ctx, &nodeInfos, nodeQuery, nodeID); err != nil || len(nodeInfos) == 0 {
		npa.logger.Debug("Could not get node IP, falling back to node_id-only allocation",
			zap.String("node_id", nodeID),
		)
	}

	type portRow struct {
		PortStart int `db:"port_start"`
		PortEnd   int `db:"port_end"`
	}
	var rows []portRow
	var err error

	if len(nodeInfos) > 0 && nodeInfos[0].IPAddress != "" {
		var sameIPCount []struct {
			Count int `db:"count"`
		}
		countQuery := `SELECT COUNT(DISTINCT id) as count FROM dns_nodes WHERE ip_address = ?`
		if err := npa.db.Query(ctx, &sameIPCount, countQuery, nodeInfos[0].IPAddress); err == nil && len(sameIPCount) > 0 && sameIPCount[0].Count > 1 {
			query := `
				SELECT npa.port_start, npa.port_end
				FROM namespace_port_allocations npa
				JOIN dns_nodes dn ON npa.node_id = dn.id
				WHERE dn.ip_address = ?
				ORDER BY npa.port_start ASC
			`
			err = npa.db.Query(ctx, &rows, query, nodeInfos[0].IPAddress)
			npa.logger.Debug("Multiple nodes share IP, allocating globally",
				zap.String("ip_address", nodeInfos[0].IPAddress),
				zap.Int("same_ip_nodes", sameIPCount[0].Count),
			)
		} else {
			query := `SELECT port_start, port_end FROM namespace_port_allocations WHERE node_id = ? ORDER BY port_start ASC`
			err = npa.db.Query(ctx, &rows, query, nodeID)
		}
	} else {
		query := `SELECT port_start, port_end FROM namespace_port_allocations WHERE node_id = ? ORDER BY port_start ASC`
		err = npa.db.Query(ctx, &rows, query, nodeID)
	}
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query allocated ports",
			Cause:   err,
		}
	}

	out := make([]allocatedRange, 0, len(rows))
	for _, row := range rows {
		end := row.PortEnd
		if end < row.PortStart {
			end = row.PortStart + PortsPerNamespace - 1
		}
		out = append(out, allocatedRange{Start: row.PortStart, End: end})
	}
	return out, nil
}

// tryAllocatePortBlock attempts to allocate a port block (single attempt)
func (npa *NamespacePortAllocator) tryAllocatePortBlock(ctx context.Context, nodeID, namespaceClusterID string, bp Blueprint) (*PortBlock, error) {
	size := bp.PortNeedCount()
	if size <= 0 {
		return nil, &ClusterError{Message: "blueprint needs no ports"}
	}

	allocated, err := npa.allocatedRanges(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	portStart, ok := findFreeBlock(allocated, NamespacePortRangeStart, NamespacePortRangeEnd, size)
	if !ok {
		return nil, ErrNoPortsAvailable
	}

	block := portBlockFromBlueprint(nodeID, namespaceClusterID, portStart, bp)

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

// GetNodeCapacity returns how many more tenant-default (PortsPerNamespace)
// blocks still fit on the node, first-fit.
func (npa *NamespacePortAllocator) GetNodeCapacity(ctx context.Context, nodeID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)
	allocated, err := npa.allocatedRanges(internalCtx, nodeID)
	if err != nil {
		return 0, err
	}
	n := 0
	alloc := append([]allocatedRange(nil), allocated...)
	for {
		start, ok := findFreeBlock(alloc, NamespacePortRangeStart, NamespacePortRangeEnd, PortsPerNamespace)
		if !ok {
			break
		}
		n++
		alloc = append(alloc, allocatedRange{Start: start, End: start + PortsPerNamespace - 1})
	}
	return n, nil
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
	return strings.Contains(errStr, "UNIQUE") || strings.Contains(errStr, "constraint") || strings.Contains(errStr, "conflict")
}
