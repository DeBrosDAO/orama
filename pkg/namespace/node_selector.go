package namespace

import (
	"context"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ClusterNodeSelector selects optimal nodes for namespace clusters.
// It extends the existing capacity scoring system from deployments/home_node.go
// to select multiple nodes based on available capacity.
type ClusterNodeSelector struct {
	db            rqlite.Client
	portAllocator *NamespacePortAllocator
	logger        *zap.Logger
}

// NodeCapacity represents the capacity metrics for a single node
type NodeCapacity struct {
	NodeID                   string  `json:"node_id"`
	IPAddress                string  `json:"ip_address"`
	DeploymentCount          int     `json:"deployment_count"`
	AllocatedPorts           int     `json:"allocated_ports"`
	AvailablePorts           int     `json:"available_ports"`
	UsedMemoryMB             int     `json:"used_memory_mb"`
	AvailableMemoryMB        int     `json:"available_memory_mb"`
	UsedCPUPercent           int     `json:"used_cpu_percent"`
	NamespaceInstanceCount   int     `json:"namespace_instance_count"`   // Number of namespace clusters on this node
	AvailableNamespaceSlots  int     `json:"available_namespace_slots"`  // How many more namespace instances can fit
	Score                    float64 `json:"score"`
}

// NewClusterNodeSelector creates a new node selector
func NewClusterNodeSelector(db rqlite.Client, portAllocator *NamespacePortAllocator, logger *zap.Logger) *ClusterNodeSelector {
	return &ClusterNodeSelector{
		db:            db,
		portAllocator: portAllocator,
		logger:        logger.With(zap.String("component", "cluster-node-selector")),
	}
}

// SelectNodesForCluster selects the optimal N nodes for a new namespace cluster.
// Returns the node IDs sorted by score (best first).
func (cns *ClusterNodeSelector) SelectNodesForCluster(ctx context.Context, nodeCount int) ([]NodeCapacity, error) {
	internalCtx := client.WithInternalAuth(ctx)

	// Get all active nodes
	activeNodes, err := cns.getActiveNodes(internalCtx)
	if err != nil {
		return nil, err
	}

	cns.logger.Debug("Found active nodes", zap.Int("count", len(activeNodes)))

	// Filter nodes that have capacity for namespace instances
	eligibleNodes := make([]NodeCapacity, 0)
	for _, node := range activeNodes {
		capacity, err := cns.getNodeCapacity(internalCtx, node.NodeID, node.IPAddress)
		if err != nil {
			cns.logger.Warn("Failed to get node capacity, skipping",
				zap.String("node_id", node.NodeID),
				zap.Error(err),
			)
			continue
		}

		// Only include nodes with available namespace slots
		if capacity.AvailableNamespaceSlots > 0 {
			eligibleNodes = append(eligibleNodes, *capacity)
		} else {
			cns.logger.Debug("Node at capacity, skipping",
				zap.String("node_id", node.NodeID),
				zap.Int("namespace_instances", capacity.NamespaceInstanceCount),
			)
		}
	}

	cns.logger.Debug("Eligible nodes after filtering", zap.Int("count", len(eligibleNodes)))

	// Check if we have enough nodes
	if len(eligibleNodes) < nodeCount {
		return nil, &ClusterError{
			Message: ErrInsufficientNodes.Message,
			Cause:   nil,
		}
	}

	// Sort by score (highest first)
	sort.Slice(eligibleNodes, func(i, j int) bool {
		return eligibleNodes[i].Score > eligibleNodes[j].Score
	})

	// Return top N nodes
	selectedNodes := eligibleNodes[:nodeCount]

	cns.logger.Info("Selected nodes for cluster",
		zap.Int("requested", nodeCount),
		zap.Int("selected", len(selectedNodes)),
	)

	for i, node := range selectedNodes {
		cns.logger.Debug("Selected node",
			zap.Int("rank", i+1),
			zap.String("node_id", node.NodeID),
			zap.Float64("score", node.Score),
			zap.Int("namespace_instances", node.NamespaceInstanceCount),
			zap.Int("available_slots", node.AvailableNamespaceSlots),
		)
	}

	return selectedNodes, nil
}

// nodeInfo is used for querying active nodes
type nodeInfo struct {
	NodeID    string `db:"id"`
	IPAddress string `db:"ip_address"`
}

// getActiveNodes retrieves all active nodes from dns_nodes table
func (cns *ClusterNodeSelector) getActiveNodes(ctx context.Context) ([]nodeInfo, error) {
	// Nodes must have checked in within last 2 minutes
	cutoff := time.Now().Add(-2 * time.Minute)

	var results []nodeInfo
	query := `
		SELECT id, ip_address FROM dns_nodes
		WHERE status = 'active' AND last_seen > ?
		ORDER BY id
	`
	err := cns.db.Query(ctx, &results, query, cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query active nodes",
			Cause:   err,
		}
	}

	cns.logger.Debug("Found active nodes",
		zap.Int("count", len(results)),
	)

	return results, nil
}

// getNodeCapacity calculates capacity metrics for a single node
func (cns *ClusterNodeSelector) getNodeCapacity(ctx context.Context, nodeID, ipAddress string) (*NodeCapacity, error) {
	// Get deployment count
	deploymentCount, err := cns.getDeploymentCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Get allocated deployment ports
	allocatedPorts, err := cns.getDeploymentPortCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Get resource usage from home_node_assignments
	totalMemoryMB, totalCPUPercent, err := cns.getNodeResourceUsage(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Get namespace instance count
	namespaceInstanceCount, err := cns.portAllocator.GetNodeAllocationCount(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	// Calculate available capacity
	const (
		maxDeployments           = 100
		maxPorts                 = 9900 // User deployment port range
		maxMemoryMB              = 8192 // 8GB
		maxCPUPercent            = 400  // 4 cores
	)

	availablePorts := maxPorts - allocatedPorts
	if availablePorts < 0 {
		availablePorts = 0
	}

	availableMemoryMB := maxMemoryMB - totalMemoryMB
	if availableMemoryMB < 0 {
		availableMemoryMB = 0
	}

	availableNamespaceSlots := MaxNamespacesPerNode - namespaceInstanceCount
	if availableNamespaceSlots < 0 {
		availableNamespaceSlots = 0
	}

	// Calculate capacity score (0.0 to 1.0, higher is better)
	// Extended from home_node.go to include namespace instance count
	score := cns.calculateCapacityScore(
		deploymentCount, maxDeployments,
		allocatedPorts, maxPorts,
		totalMemoryMB, maxMemoryMB,
		totalCPUPercent, maxCPUPercent,
		namespaceInstanceCount, MaxNamespacesPerNode,
	)

	capacity := &NodeCapacity{
		NodeID:                  nodeID,
		IPAddress:               ipAddress,
		DeploymentCount:         deploymentCount,
		AllocatedPorts:          allocatedPorts,
		AvailablePorts:          availablePorts,
		UsedMemoryMB:            totalMemoryMB,
		AvailableMemoryMB:       availableMemoryMB,
		UsedCPUPercent:          totalCPUPercent,
		NamespaceInstanceCount:  namespaceInstanceCount,
		AvailableNamespaceSlots: availableNamespaceSlots,
		Score:                   score,
	}

	return capacity, nil
}

// getDeploymentCount counts active deployments on a node
func (cns *ClusterNodeSelector) getDeploymentCount(ctx context.Context, nodeID string) (int, error) {
	type countResult struct {
		Count int `db:"count"`
	}

	var results []countResult
	query := `SELECT COUNT(*) as count FROM deployments WHERE home_node_id = ? AND status IN ('active', 'deploying')`
	err := cns.db.Query(ctx, &results, query, nodeID)
	if err != nil {
		return 0, &ClusterError{
			Message: "failed to count deployments",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, nil
	}

	return results[0].Count, nil
}

// getDeploymentPortCount counts allocated deployment ports on a node
func (cns *ClusterNodeSelector) getDeploymentPortCount(ctx context.Context, nodeID string) (int, error) {
	type countResult struct {
		Count int `db:"count"`
	}

	var results []countResult
	query := `SELECT COUNT(*) as count FROM port_allocations WHERE node_id = ?`
	err := cns.db.Query(ctx, &results, query, nodeID)
	if err != nil {
		return 0, &ClusterError{
			Message: "failed to count allocated ports",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, nil
	}

	return results[0].Count, nil
}

// getNodeResourceUsage sums up resource usage for all namespaces on a node
func (cns *ClusterNodeSelector) getNodeResourceUsage(ctx context.Context, nodeID string) (int, int, error) {
	type resourceResult struct {
		TotalMemoryMB   int `db:"total_memory"`
		TotalCPUPercent int `db:"total_cpu"`
	}

	var results []resourceResult
	query := `
		SELECT
			COALESCE(SUM(total_memory_mb), 0) as total_memory,
			COALESCE(SUM(total_cpu_percent), 0) as total_cpu
		FROM home_node_assignments
		WHERE home_node_id = ?
	`
	err := cns.db.Query(ctx, &results, query, nodeID)
	if err != nil {
		return 0, 0, &ClusterError{
			Message: "failed to query resource usage",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, 0, nil
	}

	return results[0].TotalMemoryMB, results[0].TotalCPUPercent, nil
}

// calculateCapacityScore calculates a weighted capacity score (0.0 to 1.0)
// Higher scores indicate more available capacity
func (cns *ClusterNodeSelector) calculateCapacityScore(
	deploymentCount, maxDeployments int,
	allocatedPorts, maxPorts int,
	usedMemoryMB, maxMemoryMB int,
	usedCPUPercent, maxCPUPercent int,
	namespaceInstances, maxNamespaceInstances int,
) float64 {
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

	namespaceScore := 1.0 - (float64(namespaceInstances) / float64(maxNamespaceInstances))
	if namespaceScore < 0 {
		namespaceScore = 0
	}

	// Weighted average
	// Namespace instance count gets significant weight since that's what we're optimizing for
	// Weights: deployments 30%, ports 15%, memory 15%, cpu 15%, namespace instances 25%
	totalScore := (deploymentScore * 0.30) +
		(portScore * 0.15) +
		(memoryScore * 0.15) +
		(cpuScore * 0.15) +
		(namespaceScore * 0.25)

	cns.logger.Debug("Calculated capacity score",
		zap.Int("deployments", deploymentCount),
		zap.Int("allocated_ports", allocatedPorts),
		zap.Int("used_memory_mb", usedMemoryMB),
		zap.Int("used_cpu_percent", usedCPUPercent),
		zap.Int("namespace_instances", namespaceInstances),
		zap.Float64("deployment_score", deploymentScore),
		zap.Float64("port_score", portScore),
		zap.Float64("memory_score", memoryScore),
		zap.Float64("cpu_score", cpuScore),
		zap.Float64("namespace_score", namespaceScore),
		zap.Float64("total_score", totalScore),
	)

	return totalScore
}

// GetNodeByID retrieves a node's information by ID
func (cns *ClusterNodeSelector) GetNodeByID(ctx context.Context, nodeID string) (*nodeInfo, error) {
	internalCtx := client.WithInternalAuth(ctx)

	var results []nodeInfo
	query := `SELECT id, ip_address FROM dns_nodes WHERE id = ? LIMIT 1`
	err := cns.db.Query(internalCtx, &results, query, nodeID)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query node",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return nil, nil
	}

	return &results[0], nil
}
