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

// WebRTCPortAllocator manages port allocation for SFU and TURN services.
// Uses the webrtc_port_allocations table, separate from namespace_port_allocations,
// to avoid breaking existing port blocks.
type WebRTCPortAllocator struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewWebRTCPortAllocator creates a new WebRTC port allocator
func NewWebRTCPortAllocator(db rqlite.Client, logger *zap.Logger) *WebRTCPortAllocator {
	return &WebRTCPortAllocator{
		db:     db,
		logger: logger.With(zap.String("component", "webrtc-port-allocator")),
	}
}

// AllocateSFUPorts allocates SFU ports for a namespace on a node.
// Each namespace gets: 1 signaling port (30000-30099) + 500 media ports (20000-29999).
// Returns the existing allocation if one already exists (idempotent).
func (wpa *WebRTCPortAllocator) AllocateSFUPorts(ctx context.Context, nodeID, namespaceClusterID string) (*WebRTCPortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Check for existing allocation (idempotent)
	existing, err := wpa.GetSFUPorts(ctx, namespaceClusterID, nodeID)
	if err == nil && existing != nil {
		wpa.logger.Debug("SFU ports already allocated",
			zap.String("node_id", nodeID),
			zap.String("namespace_cluster_id", namespaceClusterID),
			zap.Int("signaling_port", existing.SFUSignalingPort),
		)
		return existing, nil
	}

	// Retry logic for concurrent allocation conflicts
	maxRetries := 10
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Re-check for existing allocation (handles read-after-write lag on retries)
		if attempt > 0 {
			if existing, err := wpa.GetSFUPorts(ctx, namespaceClusterID, nodeID); err == nil && existing != nil {
				return existing, nil
			}
		}

		block, err := wpa.tryAllocateSFUPorts(internalCtx, nodeID, namespaceClusterID)
		if err == nil {
			wpa.logger.Info("SFU ports allocated",
				zap.String("node_id", nodeID),
				zap.String("namespace_cluster_id", namespaceClusterID),
				zap.Int("signaling_port", block.SFUSignalingPort),
				zap.Int("media_start", block.SFUMediaPortStart),
				zap.Int("media_end", block.SFUMediaPortEnd),
				zap.Int("attempt", attempt+1),
			)
			return block, nil
		}

		if isConflictError(err) {
			wpa.logger.Debug("SFU port allocation conflict, retrying",
				zap.String("node_id", nodeID),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
			time.Sleep(retryDelay)
			retryDelay *= 2
			continue
		}

		return nil, err
	}

	return nil, &ClusterError{
		Message: fmt.Sprintf("failed to allocate SFU ports after %d retries", maxRetries),
	}
}

// tryAllocateSFUPorts performs a single attempt to allocate SFU ports.
func (wpa *WebRTCPortAllocator) tryAllocateSFUPorts(ctx context.Context, nodeID, namespaceClusterID string) (*WebRTCPortBlock, error) {
	// Get node IPs sharing the same physical address (dev environment handling)
	nodeIDs, err := wpa.getColocatedNodeIDs(ctx, nodeID)
	if err != nil {
		nodeIDs = []string{nodeID}
	}

	// Find next available SFU signaling port (30000-30099)
	signalingPort, err := wpa.findAvailablePort(ctx, nodeIDs, "sfu", "sfu_signaling_port",
		SFUSignalingPortRangeStart, SFUSignalingPortRangeEnd, 1)
	if err != nil {
		return nil, &ClusterError{
			Message: "no SFU signaling port available on node",
			Cause:   err,
		}
	}

	// Find next available SFU media port block (20000-29999, 500 per namespace)
	mediaStart, err := wpa.findAvailablePortBlock(ctx, nodeIDs, "sfu", "sfu_media_port_start",
		SFUMediaPortRangeStart, SFUMediaPortRangeEnd, SFUMediaPortsPerNamespace)
	if err != nil {
		return nil, &ClusterError{
			Message: "no SFU media port range available on node",
			Cause:   err,
		}
	}

	block := &WebRTCPortBlock{
		ID:                 uuid.New().String(),
		NodeID:             nodeID,
		NamespaceClusterID: namespaceClusterID,
		ServiceType:        "sfu",
		SFUSignalingPort:   signalingPort,
		SFUMediaPortStart:  mediaStart,
		SFUMediaPortEnd:    mediaStart + SFUMediaPortsPerNamespace - 1,
		AllocatedAt:        time.Now(),
	}

	if err := wpa.insertPortBlock(ctx, block); err != nil {
		return nil, err
	}

	return block, nil
}

// AllocateTURNPorts allocates TURN ports for a namespace on a node.
// Each namespace gets: standard listen ports (3478/443) + 800 relay ports (49152-65535).
// Returns the existing allocation if one already exists (idempotent).
func (wpa *WebRTCPortAllocator) AllocateTURNPorts(ctx context.Context, nodeID, namespaceClusterID string) (*WebRTCPortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Check for existing allocation (idempotent)
	existing, err := wpa.GetTURNPorts(ctx, namespaceClusterID, nodeID)
	if err == nil && existing != nil {
		wpa.logger.Debug("TURN ports already allocated",
			zap.String("node_id", nodeID),
			zap.String("namespace_cluster_id", namespaceClusterID),
		)
		return existing, nil
	}

	// Retry logic for concurrent allocation conflicts
	maxRetries := 10
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Re-check for existing allocation (handles read-after-write lag on retries)
		if attempt > 0 {
			if existing, err := wpa.GetTURNPorts(ctx, namespaceClusterID, nodeID); err == nil && existing != nil {
				return existing, nil
			}
		}

		block, err := wpa.tryAllocateTURNPorts(internalCtx, nodeID, namespaceClusterID)
		if err == nil {
			wpa.logger.Info("TURN ports allocated",
				zap.String("node_id", nodeID),
				zap.String("namespace_cluster_id", namespaceClusterID),
				zap.Int("relay_start", block.TURNRelayPortStart),
				zap.Int("relay_end", block.TURNRelayPortEnd),
				zap.Int("attempt", attempt+1),
			)
			return block, nil
		}

		if isConflictError(err) {
			wpa.logger.Debug("TURN port allocation conflict, retrying",
				zap.String("node_id", nodeID),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
			time.Sleep(retryDelay)
			retryDelay *= 2
			continue
		}

		return nil, err
	}

	return nil, &ClusterError{
		Message: fmt.Sprintf("failed to allocate TURN ports after %d retries", maxRetries),
	}
}

// tryAllocateTURNPorts performs a single attempt to allocate TURN ports.
func (wpa *WebRTCPortAllocator) tryAllocateTURNPorts(ctx context.Context, nodeID, namespaceClusterID string) (*WebRTCPortBlock, error) {
	// Get colocated node IDs (dev environment handling)
	nodeIDs, err := wpa.getColocatedNodeIDs(ctx, nodeID)
	if err != nil {
		nodeIDs = []string{nodeID}
	}

	// Find next available TURN relay port block (49152-65535, 800 per namespace)
	relayStart, err := wpa.findAvailablePortBlock(ctx, nodeIDs, "turn", "turn_relay_port_start",
		TURNRelayPortRangeStart, TURNRelayPortRangeEnd, TURNRelayPortsPerNamespace)
	if err != nil {
		return nil, &ClusterError{
			Message: "no TURN relay port range available on node",
			Cause:   err,
		}
	}

	block := &WebRTCPortBlock{
		ID:                 uuid.New().String(),
		NodeID:             nodeID,
		NamespaceClusterID: namespaceClusterID,
		ServiceType:        "turn",
		TURNListenPort:     TURNDefaultPort,
		TURNTLSPort:        TURNSPort,
		TURNRelayPortStart: relayStart,
		TURNRelayPortEnd:   relayStart + TURNRelayPortsPerNamespace - 1,
		AllocatedAt:        time.Now(),
	}

	if err := wpa.insertPortBlock(ctx, block); err != nil {
		return nil, err
	}

	return block, nil
}

// DeallocateAll releases all WebRTC port blocks for a namespace cluster.
func (wpa *WebRTCPortAllocator) DeallocateAll(ctx context.Context, namespaceClusterID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM webrtc_port_allocations WHERE namespace_cluster_id = ?`
	_, err := wpa.db.Exec(internalCtx, query, namespaceClusterID)
	if err != nil {
		return &ClusterError{
			Message: "failed to deallocate WebRTC port blocks",
			Cause:   err,
		}
	}

	wpa.logger.Info("All WebRTC port blocks deallocated",
		zap.String("namespace_cluster_id", namespaceClusterID),
	)

	return nil
}

// DeallocateByNode releases WebRTC port blocks for a specific node and service type.
func (wpa *WebRTCPortAllocator) DeallocateByNode(ctx context.Context, namespaceClusterID, nodeID, serviceType string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM webrtc_port_allocations WHERE namespace_cluster_id = ? AND node_id = ? AND service_type = ?`
	_, err := wpa.db.Exec(internalCtx, query, namespaceClusterID, nodeID, serviceType)
	if err != nil {
		return &ClusterError{
			Message: fmt.Sprintf("failed to deallocate %s port block on node %s", serviceType, nodeID),
			Cause:   err,
		}
	}

	wpa.logger.Info("WebRTC port block deallocated",
		zap.String("namespace_cluster_id", namespaceClusterID),
		zap.String("node_id", nodeID),
		zap.String("service_type", serviceType),
	)

	return nil
}

// GetSFUPorts retrieves the SFU port allocation for a namespace on a node.
func (wpa *WebRTCPortAllocator) GetSFUPorts(ctx context.Context, namespaceClusterID, nodeID string) (*WebRTCPortBlock, error) {
	return wpa.getPortBlock(ctx, namespaceClusterID, nodeID, "sfu")
}

// GetTURNPorts retrieves the TURN port allocation for a namespace on a node.
func (wpa *WebRTCPortAllocator) GetTURNPorts(ctx context.Context, namespaceClusterID, nodeID string) (*WebRTCPortBlock, error) {
	return wpa.getPortBlock(ctx, namespaceClusterID, nodeID, "turn")
}

// GetAllPorts retrieves all WebRTC port blocks for a namespace cluster.
func (wpa *WebRTCPortAllocator) GetAllPorts(ctx context.Context, namespaceClusterID string) ([]WebRTCPortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var blocks []WebRTCPortBlock
	query := `
		SELECT id, node_id, namespace_cluster_id, service_type,
			   sfu_signaling_port, sfu_media_port_start, sfu_media_port_end,
			   turn_listen_port, turn_tls_port, turn_relay_port_start, turn_relay_port_end,
			   allocated_at
		FROM webrtc_port_allocations
		WHERE namespace_cluster_id = ?
		ORDER BY service_type, node_id
	`
	err := wpa.db.Query(internalCtx, &blocks, query, namespaceClusterID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query WebRTC port blocks",
			Cause:   err,
		}
	}

	return blocks, nil
}

// NodeHasTURN checks if a node already has a TURN allocation from any namespace.
// Used during node selection to avoid port conflicts on standard TURN ports (3478/443).
func (wpa *WebRTCPortAllocator) NodeHasTURN(ctx context.Context, nodeID string) (bool, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type countResult struct {
		Count int `db:"count"`
	}

	var results []countResult
	query := `SELECT COUNT(*) as count FROM webrtc_port_allocations WHERE node_id = ? AND service_type = 'turn'`
	err := wpa.db.Query(internalCtx, &results, query, nodeID)
	if err != nil {
		return false, &ClusterError{
			Message: "failed to check TURN allocation on node",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return false, nil
	}

	return results[0].Count > 0, nil
}

// --- internal helpers ---

// getPortBlock retrieves a specific port block by cluster, node, and service type.
func (wpa *WebRTCPortAllocator) getPortBlock(ctx context.Context, namespaceClusterID, nodeID, serviceType string) (*WebRTCPortBlock, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var blocks []WebRTCPortBlock
	query := `
		SELECT id, node_id, namespace_cluster_id, service_type,
			   sfu_signaling_port, sfu_media_port_start, sfu_media_port_end,
			   turn_listen_port, turn_tls_port, turn_relay_port_start, turn_relay_port_end,
			   allocated_at
		FROM webrtc_port_allocations
		WHERE namespace_cluster_id = ? AND node_id = ? AND service_type = ?
		LIMIT 1
	`
	err := wpa.db.Query(internalCtx, &blocks, query, namespaceClusterID, nodeID, serviceType)
	if err != nil {
		return nil, &ClusterError{
			Message: fmt.Sprintf("failed to query %s port block", serviceType),
			Cause:   err,
		}
	}

	if len(blocks) == 0 {
		return nil, nil
	}

	return &blocks[0], nil
}

// insertPortBlock inserts a WebRTC port allocation record.
func (wpa *WebRTCPortAllocator) insertPortBlock(ctx context.Context, block *WebRTCPortBlock) error {
	query := `
		INSERT INTO webrtc_port_allocations (
			id, node_id, namespace_cluster_id, service_type,
			sfu_signaling_port, sfu_media_port_start, sfu_media_port_end,
			turn_listen_port, turn_tls_port, turn_relay_port_start, turn_relay_port_end,
			allocated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := wpa.db.Exec(ctx, query,
		block.ID,
		block.NodeID,
		block.NamespaceClusterID,
		block.ServiceType,
		block.SFUSignalingPort,
		block.SFUMediaPortStart,
		block.SFUMediaPortEnd,
		block.TURNListenPort,
		block.TURNTLSPort,
		block.TURNRelayPortStart,
		block.TURNRelayPortEnd,
		block.AllocatedAt,
	)
	if err != nil {
		return &ClusterError{
			Message: fmt.Sprintf("failed to insert %s port allocation", block.ServiceType),
			Cause:   err,
		}
	}

	return nil
}

// getColocatedNodeIDs returns all node IDs that share the same IP address as the given node.
// In dev environments, multiple logical nodes share one physical IP — port ranges must not overlap.
// In production (one node per IP), returns only the given nodeID.
func (wpa *WebRTCPortAllocator) getColocatedNodeIDs(ctx context.Context, nodeID string) ([]string, error) {
	// Get this node's IP
	type nodeInfo struct {
		IPAddress string `db:"ip_address"`
	}
	var infos []nodeInfo
	if err := wpa.db.Query(ctx, &infos, `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`, nodeID); err != nil || len(infos) == 0 {
		return []string{nodeID}, nil
	}

	ip := infos[0].IPAddress
	if ip == "" {
		return []string{nodeID}, nil
	}

	// Check if multiple nodes share this IP
	type nodeIDRow struct {
		ID string `db:"id"`
	}
	var colocated []nodeIDRow
	if err := wpa.db.Query(ctx, &colocated, `SELECT id FROM dns_nodes WHERE ip_address = ?`, ip); err != nil || len(colocated) <= 1 {
		return []string{nodeID}, nil
	}

	ids := make([]string, len(colocated))
	for i, n := range colocated {
		ids[i] = n.ID
	}

	wpa.logger.Debug("Multiple nodes share IP, allocating globally",
		zap.String("ip_address", ip),
		zap.Int("node_count", len(ids)),
	)

	return ids, nil
}

// findAvailablePort finds the next available single port in a range on the given nodes.
func (wpa *WebRTCPortAllocator) findAvailablePort(ctx context.Context, nodeIDs []string, serviceType, portColumn string, rangeStart, rangeEnd, step int) (int, error) {
	allocated, err := wpa.getAllocatedValues(ctx, nodeIDs, serviceType, portColumn)
	if err != nil {
		return 0, err
	}

	allocatedSet := make(map[int]bool, len(allocated))
	for _, v := range allocated {
		allocatedSet[v] = true
	}

	for port := rangeStart; port <= rangeEnd; port += step {
		if !allocatedSet[port] {
			return port, nil
		}
	}

	return 0, ErrNoWebRTCPortsAvailable
}

// findAvailablePortBlock finds the next available contiguous port block in a range.
func (wpa *WebRTCPortAllocator) findAvailablePortBlock(ctx context.Context, nodeIDs []string, serviceType, portColumn string, rangeStart, rangeEnd, blockSize int) (int, error) {
	allocated, err := wpa.getAllocatedValues(ctx, nodeIDs, serviceType, portColumn)
	if err != nil {
		return 0, err
	}

	allocatedSet := make(map[int]bool, len(allocated))
	for _, v := range allocated {
		allocatedSet[v] = true
	}

	for start := rangeStart; start+blockSize-1 <= rangeEnd; start += blockSize {
		if !allocatedSet[start] {
			return start, nil
		}
	}

	return 0, ErrNoWebRTCPortsAvailable
}

// getAllocatedValues queries the allocated port values for a given column across colocated nodes.
func (wpa *WebRTCPortAllocator) getAllocatedValues(ctx context.Context, nodeIDs []string, serviceType, portColumn string) ([]int, error) {
	type portRow struct {
		Port int `db:"port_val"`
	}

	var rows []portRow

	if len(nodeIDs) == 1 {
		query := fmt.Sprintf(
			`SELECT %s as port_val FROM webrtc_port_allocations WHERE node_id = ? AND service_type = ? AND %s > 0 ORDER BY %s ASC`,
			portColumn, portColumn, portColumn,
		)
		if err := wpa.db.Query(ctx, &rows, query, nodeIDs[0], serviceType); err != nil {
			return nil, &ClusterError{
				Message: "failed to query allocated WebRTC ports",
				Cause:   err,
			}
		}
	} else {
		// Multiple colocated nodes — query by joining with dns_nodes on IP
		// Get the IP of the first node (they all share the same IP)
		type nodeInfo struct {
			IPAddress string `db:"ip_address"`
		}
		var infos []nodeInfo
		if err := wpa.db.Query(ctx, &infos, `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`, nodeIDs[0]); err != nil || len(infos) == 0 {
			return nil, &ClusterError{Message: "failed to get node IP for colocated port query"}
		}

		query := fmt.Sprintf(
			`SELECT wpa.%s as port_val FROM webrtc_port_allocations wpa
			 JOIN dns_nodes dn ON wpa.node_id = dn.id
			 WHERE dn.ip_address = ? AND wpa.service_type = ? AND wpa.%s > 0
			 ORDER BY wpa.%s ASC`,
			portColumn, portColumn, portColumn,
		)
		if err := wpa.db.Query(ctx, &rows, query, infos[0].IPAddress, serviceType); err != nil {
			return nil, &ClusterError{
				Message: "failed to query allocated WebRTC ports (colocated)",
				Cause:   err,
			}
		}
	}

	result := make([]int, len(rows))
	for i, r := range rows {
		result[i] = r.Port
	}
	return result, nil
}


