package deployments

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ReplicaManager manages deployment replicas across nodes
type ReplicaManager struct {
	db            rqlite.Client
	homeNodeMgr   *HomeNodeManager
	portAllocator *PortAllocator
	logger        *zap.Logger
}

// NewReplicaManager creates a new replica manager
func NewReplicaManager(db rqlite.Client, homeNodeMgr *HomeNodeManager, portAllocator *PortAllocator, logger *zap.Logger) *ReplicaManager {
	return &ReplicaManager{
		db:            db,
		homeNodeMgr:   homeNodeMgr,
		portAllocator: portAllocator,
		logger:        logger,
	}
}

// SelectReplicaNodes picks additional nodes for replicas, excluding the primary node.
// Returns up to count node IDs.
func (rm *ReplicaManager) SelectReplicaNodes(ctx context.Context, primaryNodeID string, count int) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	activeNodes, err := rm.homeNodeMgr.getActiveNodes(internalCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active nodes: %w", err)
	}

	// Filter out the primary node
	var candidates []string
	for _, nodeID := range activeNodes {
		if nodeID != primaryNodeID {
			candidates = append(candidates, nodeID)
		}
	}

	if len(candidates) == 0 {
		return nil, nil // No additional nodes available
	}

	// Calculate capacity scores and pick the best ones
	capacities, err := rm.homeNodeMgr.calculateNodeCapacities(internalCtx, candidates)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate capacities: %w", err)
	}

	// Sort by score descending (simple selection)
	selected := make([]string, 0, count)
	for i := 0; i < count && i < len(capacities); i++ {
		best := rm.homeNodeMgr.selectBestNode(capacities)
		if best == nil {
			break
		}
		selected = append(selected, best.NodeID)
		// Remove selected from capacities
		remaining := make([]*NodeCapacity, 0, len(capacities)-1)
		for _, c := range capacities {
			if c.NodeID != best.NodeID {
				remaining = append(remaining, c)
			}
		}
		capacities = remaining
	}

	rm.logger.Info("Selected replica nodes",
		zap.String("primary", primaryNodeID),
		zap.Strings("replicas", selected),
		zap.Int("requested", count),
	)

	return selected, nil
}

// CreateReplica inserts a replica record for a deployment on a specific node.
func (rm *ReplicaManager) CreateReplica(ctx context.Context, deploymentID, nodeID string, port int, isPrimary bool) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `
		INSERT INTO deployment_replicas (deployment_id, node_id, port, status, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(deployment_id, node_id) DO UPDATE SET
			port = excluded.port,
			status = excluded.status,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`

	now := time.Now()
	_, err := rm.db.Exec(internalCtx, query, deploymentID, nodeID, port, ReplicaStatusActive, isPrimary, now, now)
	if err != nil {
		return &DeploymentError{
			Message: fmt.Sprintf("failed to create replica for deployment %s on node %s", deploymentID, nodeID),
			Cause:   err,
		}
	}

	rm.logger.Info("Created deployment replica",
		zap.String("deployment_id", deploymentID),
		zap.String("node_id", nodeID),
		zap.Int("port", port),
		zap.Bool("is_primary", isPrimary),
	)

	return nil
}

// GetReplicas returns all replicas for a deployment.
func (rm *ReplicaManager) GetReplicas(ctx context.Context, deploymentID string) ([]Replica, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type replicaRow struct {
		DeploymentID string `db:"deployment_id"`
		NodeID       string `db:"node_id"`
		Port         int    `db:"port"`
		Status       string `db:"status"`
		IsPrimary    bool   `db:"is_primary"`
	}

	var rows []replicaRow
	query := `SELECT deployment_id, node_id, port, status, is_primary FROM deployment_replicas WHERE deployment_id = ?`
	err := rm.db.Query(internalCtx, &rows, query, deploymentID)
	if err != nil {
		return nil, &DeploymentError{
			Message: "failed to query replicas",
			Cause:   err,
		}
	}

	replicas := make([]Replica, len(rows))
	for i, row := range rows {
		replicas[i] = Replica{
			DeploymentID: row.DeploymentID,
			NodeID:       row.NodeID,
			Port:         row.Port,
			Status:       ReplicaStatus(row.Status),
			IsPrimary:    row.IsPrimary,
		}
	}

	return replicas, nil
}

// GetActiveReplicaNodes returns node IDs of all active replicas for a deployment.
func (rm *ReplicaManager) GetActiveReplicaNodes(ctx context.Context, deploymentID string) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type nodeRow struct {
		NodeID string `db:"node_id"`
	}

	var rows []nodeRow
	query := `SELECT node_id FROM deployment_replicas WHERE deployment_id = ? AND status = ?`
	err := rm.db.Query(internalCtx, &rows, query, deploymentID, ReplicaStatusActive)
	if err != nil {
		return nil, &DeploymentError{
			Message: "failed to query active replicas",
			Cause:   err,
		}
	}

	nodes := make([]string, len(rows))
	for i, row := range rows {
		nodes[i] = row.NodeID
	}

	return nodes, nil
}

// IsReplicaNode checks if the given node is an active replica for the deployment.
func (rm *ReplicaManager) IsReplicaNode(ctx context.Context, deploymentID, nodeID string) (bool, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type countRow struct {
		Count int `db:"c"`
	}

	var rows []countRow
	query := `SELECT COUNT(*) as c FROM deployment_replicas WHERE deployment_id = ? AND node_id = ? AND status = ?`
	err := rm.db.Query(internalCtx, &rows, query, deploymentID, nodeID, ReplicaStatusActive)
	if err != nil {
		return false, err
	}

	return len(rows) > 0 && rows[0].Count > 0, nil
}

// GetReplicaPort returns the port allocated for a deployment on a specific node.
func (rm *ReplicaManager) GetReplicaPort(ctx context.Context, deploymentID, nodeID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type portRow struct {
		Port int `db:"port"`
	}

	var rows []portRow
	query := `SELECT port FROM deployment_replicas WHERE deployment_id = ? AND node_id = ? AND status = ? LIMIT 1`
	err := rm.db.Query(internalCtx, &rows, query, deploymentID, nodeID, ReplicaStatusActive)
	if err != nil {
		return 0, err
	}

	if len(rows) == 0 {
		return 0, fmt.Errorf("no active replica found for deployment %s on node %s", deploymentID, nodeID)
	}

	return rows[0].Port, nil
}

// UpdateReplicaStatus updates the status of a specific replica.
func (rm *ReplicaManager) UpdateReplicaStatus(ctx context.Context, deploymentID, nodeID string, status ReplicaStatus) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `UPDATE deployment_replicas SET status = ?, updated_at = ? WHERE deployment_id = ? AND node_id = ?`
	_, err := rm.db.Exec(internalCtx, query, status, time.Now(), deploymentID, nodeID)
	if err != nil {
		return &DeploymentError{
			Message: fmt.Sprintf("failed to update replica status for %s on %s", deploymentID, nodeID),
			Cause:   err,
		}
	}

	return nil
}

// RemoveReplicas deletes all replica records for a deployment.
func (rm *ReplicaManager) RemoveReplicas(ctx context.Context, deploymentID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM deployment_replicas WHERE deployment_id = ?`
	_, err := rm.db.Exec(internalCtx, query, deploymentID)
	if err != nil {
		return &DeploymentError{
			Message: "failed to remove replicas",
			Cause:   err,
		}
	}

	return nil
}

// GetNodeIP retrieves the IP address for a node from dns_nodes.
func (rm *ReplicaManager) GetNodeIP(ctx context.Context, nodeID string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type nodeRow struct {
		IPAddress string `db:"ip_address"`
	}

	var rows []nodeRow
	query := `SELECT ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	err := rm.db.Query(internalCtx, &rows, query, nodeID)
	if err != nil {
		return "", err
	}

	if len(rows) == 0 {
		return "", fmt.Errorf("node not found: %s", nodeID)
	}

	return rows[0].IPAddress, nil
}
