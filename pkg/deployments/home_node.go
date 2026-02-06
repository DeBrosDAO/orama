package deployments

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// HomeNodeManager manages namespace-to-node assignments
type HomeNodeManager struct {
	db            rqlite.Client
	portAllocator *PortAllocator
	logger        *zap.Logger
}

// NewHomeNodeManager creates a new home node manager
func NewHomeNodeManager(db rqlite.Client, portAllocator *PortAllocator, logger *zap.Logger) *HomeNodeManager {
	return &HomeNodeManager{
		db:            db,
		portAllocator: portAllocator,
		logger:        logger,
	}
}

// AssignHomeNode assigns a home node to a namespace (or returns existing assignment)
func (hnm *HomeNodeManager) AssignHomeNode(ctx context.Context, namespace string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Check if namespace already has a home node
	existing, err := hnm.GetHomeNode(ctx, namespace)
	if err == nil && existing != "" {
		hnm.logger.Debug("Namespace already has home node",
			zap.String("namespace", namespace),
			zap.String("home_node_id", existing),
		)
		return existing, nil
	}

	// Get all active nodes
	activeNodes, err := hnm.getActiveNodes(internalCtx)
	if err != nil {
		return "", err
	}

	if len(activeNodes) == 0 {
		return "", ErrNoNodesAvailable
	}

	// Calculate capacity scores for each node
	nodeCapacities, err := hnm.calculateNodeCapacities(internalCtx, activeNodes)
	if err != nil {
		return "", err
	}

	// Select node with highest score
	bestNode := hnm.selectBestNode(nodeCapacities)
	if bestNode == nil {
		return "", ErrNoNodesAvailable
	}

	// Create home node assignment
	insertQuery := `
		INSERT INTO home_node_assignments (namespace, home_node_id, assigned_at, last_heartbeat, deployment_count, total_memory_mb, total_cpu_percent)
		VALUES (?, ?, ?, ?, 0, 0, 0)
		ON CONFLICT(namespace) DO UPDATE SET
			home_node_id = excluded.home_node_id,
			assigned_at = excluded.assigned_at,
			last_heartbeat = excluded.last_heartbeat
	`

	now := time.Now()
	_, err = hnm.db.Exec(internalCtx, insertQuery, namespace, bestNode.NodeID, now, now)
	if err != nil {
		return "", &DeploymentError{
			Message: "failed to create home node assignment",
			Cause:   err,
		}
	}

	hnm.logger.Info("Home node assigned",
		zap.String("namespace", namespace),
		zap.String("home_node_id", bestNode.NodeID),
		zap.Float64("capacity_score", bestNode.Score),
		zap.Int("deployment_count", bestNode.DeploymentCount),
	)

	return bestNode.NodeID, nil
}

// GetHomeNode retrieves the home node for a namespace
func (hnm *HomeNodeManager) GetHomeNode(ctx context.Context, namespace string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type homeNodeResult struct {
		HomeNodeID string `db:"home_node_id"`
	}

	var results []homeNodeResult
	query := `SELECT home_node_id FROM home_node_assignments WHERE namespace = ? LIMIT 1`
	err := hnm.db.Query(internalCtx, &results, query, namespace)
	if err != nil {
		return "", &DeploymentError{
			Message: "failed to query home node",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return "", ErrNamespaceNotAssigned
	}

	return results[0].HomeNodeID, nil
}

// UpdateHeartbeat updates the last heartbeat timestamp for a namespace
func (hnm *HomeNodeManager) UpdateHeartbeat(ctx context.Context, namespace string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `UPDATE home_node_assignments SET last_heartbeat = ? WHERE namespace = ?`
	_, err := hnm.db.Exec(internalCtx, query, time.Now(), namespace)
	if err != nil {
		return &DeploymentError{
			Message: "failed to update heartbeat",
			Cause:   err,
		}
	}

	return nil
}

// GetStaleNamespaces returns namespaces that haven't sent a heartbeat recently
func (hnm *HomeNodeManager) GetStaleNamespaces(ctx context.Context, staleThreshold time.Duration) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	cutoff := time.Now().Add(-staleThreshold)

	type namespaceResult struct {
		Namespace string `db:"namespace"`
	}

	var results []namespaceResult
	query := `SELECT namespace FROM home_node_assignments WHERE last_heartbeat < ?`
	err := hnm.db.Query(internalCtx, &results, query, cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, &DeploymentError{
			Message: "failed to query stale namespaces",
			Cause:   err,
		}
	}

	namespaces := make([]string, 0, len(results))
	for _, result := range results {
		namespaces = append(namespaces, result.Namespace)
	}

	return namespaces, nil
}

// UpdateResourceUsage updates the cached resource usage for a namespace
func (hnm *HomeNodeManager) UpdateResourceUsage(ctx context.Context, namespace string, deploymentCount, memoryMB, cpuPercent int) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `
		UPDATE home_node_assignments
		SET deployment_count = ?, total_memory_mb = ?, total_cpu_percent = ?
		WHERE namespace = ?
	`
	_, err := hnm.db.Exec(internalCtx, query, deploymentCount, memoryMB, cpuPercent, namespace)
	if err != nil {
		return &DeploymentError{
			Message: "failed to update resource usage",
			Cause:   err,
		}
	}

	return nil
}

// getActiveNodes retrieves all active nodes from dns_nodes table
func (hnm *HomeNodeManager) getActiveNodes(ctx context.Context) ([]string, error) {
	// Query dns_nodes for active nodes with recent heartbeats
	cutoff := time.Now().Add(-2 * time.Minute) // Nodes must have checked in within last 2 minutes

	type nodeResult struct {
		ID string `db:"id"`
	}

	var results []nodeResult
	query := `
		SELECT id FROM dns_nodes
		WHERE status = 'active' AND last_seen > ?
		ORDER BY id
	`
	err := hnm.db.Query(ctx, &results, query, cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, &DeploymentError{
			Message: "failed to query active nodes",
			Cause:   err,
		}
	}

	nodes := make([]string, 0, len(results))
	for _, result := range results {
		nodes = append(nodes, result.ID)
	}

	hnm.logger.Debug("Found active nodes",
		zap.Int("count", len(nodes)),
		zap.Strings("nodes", nodes),
	)

	return nodes, nil
}

// calculateNodeCapacities calculates capacity scores for all nodes
func (hnm *HomeNodeManager) calculateNodeCapacities(ctx context.Context, nodeIDs []string) ([]*NodeCapacity, error) {
	capacities := make([]*NodeCapacity, 0, len(nodeIDs))

	for _, nodeID := range nodeIDs {
		capacity, err := hnm.getNodeCapacity(ctx, nodeID)
		if err != nil {
			hnm.logger.Warn("Failed to get node capacity, skipping",
				zap.String("node_id", nodeID),
				zap.Error(err),
			)
			continue
		}

		capacities = append(capacities, capacity)
	}

	return capacities, nil
}

// getNodeCapacity calculates capacity metrics for a single node
func (hnm *HomeNodeManager) getNodeCapacity(ctx context.Context, nodeID string) (*NodeCapacity, error) {
	// Count deployments on this node
	deploymentCount, err := hnm.getDeploymentCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Count allocated ports
	allocatedPorts, err := hnm.portAllocator.GetNodePortCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	availablePorts, err := hnm.portAllocator.GetAvailablePortCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Get total resource usage from home_node_assignments
	totalMemoryMB, totalCPUPercent, err := hnm.getNodeResourceUsage(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Calculate capacity score (0.0 to 1.0, higher is better)
	score := hnm.calculateCapacityScore(deploymentCount, allocatedPorts, availablePorts, totalMemoryMB, totalCPUPercent)

	capacity := &NodeCapacity{
		NodeID:            nodeID,
		DeploymentCount:   deploymentCount,
		AllocatedPorts:    allocatedPorts,
		AvailablePorts:    availablePorts,
		UsedMemoryMB:      totalMemoryMB,
		AvailableMemoryMB: 8192 - totalMemoryMB, // Assume 8GB per node (make configurable later)
		UsedCPUPercent:    totalCPUPercent,
		Score:             score,
	}

	return capacity, nil
}

// getDeploymentCount counts deployments on a node
func (hnm *HomeNodeManager) getDeploymentCount(ctx context.Context, nodeID string) (int, error) {
	type countResult struct {
		Count int `db:"COUNT(*)"`
	}

	var results []countResult
	query := `SELECT COUNT(*) FROM deployments WHERE home_node_id = ? AND status IN ('active', 'deploying')`
	err := hnm.db.Query(ctx, &results, query, nodeID)
	if err != nil {
		return 0, &DeploymentError{
			Message: "failed to count deployments",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, nil
	}

	return results[0].Count, nil
}

// getNodeResourceUsage sums up resource usage for all namespaces on a node
func (hnm *HomeNodeManager) getNodeResourceUsage(ctx context.Context, nodeID string) (int, int, error) {
	type resourceResult struct {
		TotalMemoryMB   int `db:"COALESCE(SUM(total_memory_mb), 0)"`
		TotalCPUPercent int `db:"COALESCE(SUM(total_cpu_percent), 0)"`
	}

	var results []resourceResult
	query := `
		SELECT COALESCE(SUM(total_memory_mb), 0), COALESCE(SUM(total_cpu_percent), 0)
		FROM home_node_assignments
		WHERE home_node_id = ?
	`
	err := hnm.db.Query(ctx, &results, query, nodeID)
	if err != nil {
		return 0, 0, &DeploymentError{
			Message: "failed to query resource usage",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, 0, nil
	}

	return results[0].TotalMemoryMB, results[0].TotalCPUPercent, nil
}

// calculateCapacityScore calculates a 0.0-1.0 score (higher is better)
func (hnm *HomeNodeManager) calculateCapacityScore(deploymentCount, allocatedPorts, availablePorts, usedMemoryMB, usedCPUPercent int) float64 {
	const (
		maxDeployments = 100    // Max deployments per node
		maxMemoryMB    = 8192   // 8GB
		maxCPUPercent  = 400    // 400% = 4 cores
		maxPorts       = 9900   // ~10k ports available
	)

	// Calculate individual component scores (0.0 to 1.0)
	deploymentScore := 1.0 - (float64(deploymentCount) / float64(maxDeployments))
	if deploymentScore < 0 {
		deploymentScore = 0
	}

	portScore := 1.0 - (float64(allocatedPorts) / float64(maxPorts))
	if portScore < 0 {
		portScore = 0
	}

	memoryScore := 1.0 - (float64(usedMemoryMB) / float64(maxMemoryMB))
	if memoryScore < 0 {
		memoryScore = 0
	}

	cpuScore := 1.0 - (float64(usedCPUPercent) / float64(maxCPUPercent))
	if cpuScore < 0 {
		cpuScore = 0
	}

	// Weighted average (adjust weights as needed)
	totalScore := (deploymentScore * 0.4) + (portScore * 0.2) + (memoryScore * 0.2) + (cpuScore * 0.2)

	hnm.logger.Debug("Calculated capacity score",
		zap.Int("deployments", deploymentCount),
		zap.Int("allocated_ports", allocatedPorts),
		zap.Int("used_memory_mb", usedMemoryMB),
		zap.Int("used_cpu_percent", usedCPUPercent),
		zap.Float64("deployment_score", deploymentScore),
		zap.Float64("port_score", portScore),
		zap.Float64("memory_score", memoryScore),
		zap.Float64("cpu_score", cpuScore),
		zap.Float64("total_score", totalScore),
	)

	return totalScore
}

// selectBestNode selects the node with the highest capacity score
func (hnm *HomeNodeManager) selectBestNode(capacities []*NodeCapacity) *NodeCapacity {
	if len(capacities) == 0 {
		return nil
	}

	best := capacities[0]
	for _, capacity := range capacities[1:] {
		if capacity.Score > best.Score {
			best = capacity
		}
	}

	hnm.logger.Info("Selected best node",
		zap.String("node_id", best.NodeID),
		zap.Float64("score", best.Score),
		zap.Int("deployment_count", best.DeploymentCount),
		zap.Int("allocated_ports", best.AllocatedPorts),
	)

	return best
}

// MigrateNamespace moves a namespace from one node to another (used for node failures)
func (hnm *HomeNodeManager) MigrateNamespace(ctx context.Context, namespace, newNodeID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `
		UPDATE home_node_assignments
		SET home_node_id = ?, assigned_at = ?, last_heartbeat = ?
		WHERE namespace = ?
	`

	now := time.Now()
	_, err := hnm.db.Exec(internalCtx, query, newNodeID, now, now, namespace)
	if err != nil {
		return &DeploymentError{
			Message: fmt.Sprintf("failed to migrate namespace %s to node %s", namespace, newNodeID),
			Cause:   err,
		}
	}

	hnm.logger.Info("Namespace migrated",
		zap.String("namespace", namespace),
		zap.String("new_home_node_id", newNodeID),
	)

	return nil
}
