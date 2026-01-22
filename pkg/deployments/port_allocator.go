package deployments

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// PortAllocator manages port allocation across nodes
type PortAllocator struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewPortAllocator creates a new port allocator
func NewPortAllocator(db rqlite.Client, logger *zap.Logger) *PortAllocator {
	return &PortAllocator{
		db:     db,
		logger: logger,
	}
}

// AllocatePort finds and allocates the next available port for a deployment on a specific node
// Port range: 10100-19999 (10000-10099 reserved for system use)
func (pa *PortAllocator) AllocatePort(ctx context.Context, nodeID, deploymentID string) (int, error) {
	// Use internal auth for port allocation operations
	internalCtx := client.WithInternalAuth(ctx)

	// Retry logic for handling concurrent allocation conflicts
	maxRetries := 10
	retryDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		port, err := pa.tryAllocatePort(internalCtx, nodeID, deploymentID)
		if err == nil {
			pa.logger.Info("Port allocated successfully",
				zap.String("node_id", nodeID),
				zap.Int("port", port),
				zap.String("deployment_id", deploymentID),
				zap.Int("attempt", attempt+1),
			)
			return port, nil
		}

		// If it's a conflict error, retry with exponential backoff
		if isConflictError(err) {
			pa.logger.Debug("Port allocation conflict, retrying",
				zap.String("node_id", nodeID),
				zap.String("deployment_id", deploymentID),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)

			time.Sleep(retryDelay)
			retryDelay *= 2
			continue
		}

		// Other errors are non-retryable
		return 0, err
	}

	return 0, &DeploymentError{
		Message: fmt.Sprintf("failed to allocate port after %d retries", maxRetries),
	}
}

// tryAllocatePort attempts to allocate a port (single attempt)
func (pa *PortAllocator) tryAllocatePort(ctx context.Context, nodeID, deploymentID string) (int, error) {
	// Query all allocated ports on this node
	type portRow struct {
		Port int `db:"port"`
	}

	var allocatedPortRows []portRow
	query := `SELECT port FROM port_allocations WHERE node_id = ? ORDER BY port ASC`
	err := pa.db.Query(ctx, &allocatedPortRows, query, nodeID)
	if err != nil {
		return 0, &DeploymentError{
			Message: "failed to query allocated ports",
			Cause:   err,
		}
	}

	// Parse allocated ports into map
	allocatedPorts := make(map[int]bool)
	for _, row := range allocatedPortRows {
		allocatedPorts[row.Port] = true
	}

	// Find first available port (starting from UserMinPort = 10100)
	port := UserMinPort
	for port <= MaxPort {
		if !allocatedPorts[port] {
			break
		}
		port++
	}

	if port > MaxPort {
		return 0, ErrNoPortsAvailable
	}

	// Attempt to insert allocation record (may conflict if another process allocated same port)
	insertQuery := `
		INSERT INTO port_allocations (node_id, port, deployment_id, allocated_at)
		VALUES (?, ?, ?, ?)
	`
	_, err = pa.db.Exec(ctx, insertQuery, nodeID, port, deploymentID, time.Now())
	if err != nil {
		return 0, &DeploymentError{
			Message: "failed to insert port allocation",
			Cause:   err,
		}
	}

	return port, nil
}

// DeallocatePort removes a port allocation for a deployment
func (pa *PortAllocator) DeallocatePort(ctx context.Context, deploymentID string) error {
	internalCtx := client.WithInternalAuth(ctx)

	query := `DELETE FROM port_allocations WHERE deployment_id = ?`
	_, err := pa.db.Exec(internalCtx, query, deploymentID)
	if err != nil {
		return &DeploymentError{
			Message: "failed to deallocate port",
			Cause:   err,
		}
	}

	pa.logger.Info("Port deallocated",
		zap.String("deployment_id", deploymentID),
	)

	return nil
}

// GetAllocatedPort retrieves the currently allocated port for a deployment
func (pa *PortAllocator) GetAllocatedPort(ctx context.Context, deploymentID string) (int, string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type allocation struct {
		NodeID string `db:"node_id"`
		Port   int    `db:"port"`
	}

	var allocs []allocation
	query := `SELECT node_id, port FROM port_allocations WHERE deployment_id = ? LIMIT 1`
	err := pa.db.Query(internalCtx, &allocs, query, deploymentID)
	if err != nil {
		return 0, "", &DeploymentError{
			Message: "failed to query allocated port",
			Cause:   err,
		}
	}

	if len(allocs) == 0 {
		return 0, "", &DeploymentError{
			Message: "no port allocated for deployment",
		}
	}

	return allocs[0].Port, allocs[0].NodeID, nil
}

// GetNodePortCount returns the number of allocated ports on a node
func (pa *PortAllocator) GetNodePortCount(ctx context.Context, nodeID string) (int, error) {
	internalCtx := client.WithInternalAuth(ctx)

	type countResult struct {
		Count int `db:"COUNT(*)"`
	}

	var results []countResult
	query := `SELECT COUNT(*) FROM port_allocations WHERE node_id = ?`
	err := pa.db.Query(internalCtx, &results, query, nodeID)
	if err != nil {
		return 0, &DeploymentError{
			Message: "failed to count allocated ports",
			Cause:   err,
		}
	}

	if len(results) == 0 {
		return 0, nil
	}

	return results[0].Count, nil
}

// GetAvailablePortCount returns the number of available ports on a node
func (pa *PortAllocator) GetAvailablePortCount(ctx context.Context, nodeID string) (int, error) {
	allocatedCount, err := pa.GetNodePortCount(ctx, nodeID)
	if err != nil {
		return 0, err
	}

	totalPorts := MaxPort - UserMinPort + 1
	available := totalPorts - allocatedCount

	if available < 0 {
		available = 0
	}

	return available, nil
}

// isConflictError checks if an error is due to a constraint violation (port already allocated)
func isConflictError(err error) bool {
	if err == nil {
		return false
	}
	// RQLite returns constraint violation errors as strings containing "UNIQUE constraint failed"
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
