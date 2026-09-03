package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/database"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// Tuning constants.
const (
	consecutiveFailuresThreshold = 3
	defaultDesiredReplicas       = deployments.DefaultReplicaCount
)

// ProcessManager is the subset of process.Manager needed by the health checker.
type ProcessManager interface {
	Restart(ctx context.Context, deployment *deployments.Deployment) error
	Stop(ctx context.Context, deployment *deployments.Deployment) error
}

// ReplicaReconciler provides replica management for the reconciliation loop.
type ReplicaReconciler interface {
	SelectReplicaNodes(ctx context.Context, primaryNodeID string, count int) ([]string, error)
	UpdateReplicaStatus(ctx context.Context, deploymentID, nodeID string, status deployments.ReplicaStatus) error
}

// ReplicaProvisioner provisions new replicas on remote nodes.
type ReplicaProvisioner interface {
	SetupDynamicReplica(ctx context.Context, deployment *deployments.Deployment, nodeID string)
}

// deploymentRow represents a deployment record for health checking.
type deploymentRow struct {
	ID              string `db:"id"`
	Namespace       string `db:"namespace"`
	Name            string `db:"name"`
	Type            string `db:"type"`
	Port            int    `db:"port"`
	HealthCheckPath string `db:"health_check_path"`
	HomeNodeID      string `db:"home_node_id"`
	RestartPolicy   string `db:"restart_policy"`
	MaxRestartCount int    `db:"max_restart_count"`
	ReplicaStatus   string `db:"replica_status"`
}

// replicaState tracks in-memory health state for a deployment on this node.
type replicaState struct {
	consecutiveMisses int
	restartCount      int
}

// HealthChecker monitors deployment health on the local node.
type HealthChecker struct {
	db             database.Database
	logger         *zap.Logger
	workers        int
	nodeID         string
	processManager ProcessManager

	// In-memory per-replica state (keyed by deployment ID)
	stateMu sync.Mutex
	states  map[string]*replicaState

	// Reconciliation (optional, set via SetReconciler)
	rqliteDSN   string
	reconciler  ReplicaReconciler
	provisioner ReplicaProvisioner
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(db database.Database, logger *zap.Logger, nodeID string, pm ProcessManager) *HealthChecker {
	return &HealthChecker{
		db:             db,
		logger:         logger,
		workers:        10,
		nodeID:         nodeID,
		processManager: pm,
		states:         make(map[string]*replicaState),
	}
}

// SetReconciler configures the reconciliation loop dependencies (optional).
// Must be called before Start() if re-replication is desired.
func (hc *HealthChecker) SetReconciler(rqliteDSN string, rc ReplicaReconciler, rp ReplicaProvisioner) {
	hc.rqliteDSN = rqliteDSN
	hc.reconciler = rc
	hc.provisioner = rp
}

// Start begins health monitoring with two periodic tasks:
//  1. Every 30s: probe local replicas
//  2. Every 5m: (leader-only) reconcile under-replicated deployments
func (hc *HealthChecker) Start(ctx context.Context) error {
	hc.logger.Info("Starting health checker",
		zap.Int("workers", hc.workers),
		zap.String("node_id", hc.nodeID),
	)

	probeTicker := time.NewTicker(30 * time.Second)
	reconcileTicker := time.NewTicker(5 * time.Minute)
	defer probeTicker.Stop()
	defer reconcileTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			hc.logger.Info("Health checker stopped")
			return ctx.Err()
		case <-probeTicker.C:
			if err := hc.checkAllDeployments(ctx); err != nil {
				hc.logger.Error("Health check cycle failed", zap.Error(err))
			}
		case <-reconcileTicker.C:
			hc.reconcileDeployments(ctx)
		}
	}
}

// checkAllDeployments checks all deployments with active or failed replicas on this node.
func (hc *HealthChecker) checkAllDeployments(ctx context.Context) error {
	var rows []deploymentRow
	query := `
		SELECT d.id, d.namespace, d.name, d.type, dr.port,
		       d.health_check_path, d.home_node_id,
		       d.restart_policy, d.max_restart_count,
		       dr.status as replica_status
		FROM deployments d
		JOIN deployment_replicas dr ON d.id = dr.deployment_id
		WHERE d.status IN ('active', 'degraded')
		  AND dr.node_id = ?
		  AND dr.status IN ('active', 'failed')
		  AND d.type IN ('nextjs', 'nodejs-backend', 'go-backend')
	`

	err := hc.db.Query(ctx, &rows, query, hc.nodeID)
	if err != nil {
		return fmt.Errorf("failed to query deployments: %w", err)
	}

	hc.logger.Info("Checking deployments", zap.Int("count", len(rows)))

	// Process in parallel
	sem := make(chan struct{}, hc.workers)
	var wg sync.WaitGroup

	for _, row := range rows {
		wg.Add(1)
		go func(r deploymentRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			healthy := hc.checkDeployment(ctx, r)
			hc.recordHealthCheck(ctx, r.ID, healthy)

			if healthy {
				hc.handleHealthy(ctx, r)
			} else {
				hc.handleUnhealthy(ctx, r)
			}
		}(row)
	}

	wg.Wait()
	return nil
}

// checkDeployment checks a single deployment's health via HTTP.
func (hc *HealthChecker) checkDeployment(ctx context.Context, dep deploymentRow) bool {
	if dep.Port == 0 {
		// Static deployments are always healthy
		return true
	}

	// Check local port
	url := fmt.Sprintf("http://localhost:%d%s", dep.Port, dep.HealthCheckPath)

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, "GET", url, nil)
	if err != nil {
		hc.logger.Error("Failed to create health check request",
			zap.String("deployment", dep.Name),
			zap.Error(err),
		)
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		hc.logger.Warn("Health check failed",
			zap.String("deployment", dep.Name),
			zap.String("namespace", dep.Namespace),
			zap.String("url", url),
			zap.Error(err),
		)
		return false
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300

	if !healthy {
		hc.logger.Warn("Health check returned unhealthy status",
			zap.String("deployment", dep.Name),
			zap.Int("status", resp.StatusCode),
		)
	}

	return healthy
}

// handleHealthy processes a healthy check result.
func (hc *HealthChecker) handleHealthy(ctx context.Context, dep deploymentRow) {
	hc.stateMu.Lock()
	delete(hc.states, dep.ID)
	hc.stateMu.Unlock()

	// If the replica was in 'failed' state but is now healthy, decide:
	// recover it or stop it (if a replacement already exists).
	if dep.ReplicaStatus == "failed" {
		// Count how many active replicas this deployment already has.
		type countRow struct {
			Count int `db:"c"`
		}
		var rows []countRow
		countQuery := `SELECT COUNT(*) as c FROM deployment_replicas WHERE deployment_id = ? AND status = 'active'`
		if err := hc.db.Query(ctx, &rows, countQuery, dep.ID); err != nil {
			hc.logger.Error("Failed to count active replicas for recovery check", zap.Error(err))
			return
		}
		activeCount := 0
		if len(rows) > 0 {
			activeCount = rows[0].Count
		}

		if activeCount >= defaultDesiredReplicas {
			// This replica was replaced while the node was down.
			// Stop the zombie process and remove the stale replica row.
			hc.logger.Warn("Zombie replica detected — node was replaced, stopping process",
				zap.String("deployment", dep.Name),
				zap.String("node_id", hc.nodeID),
				zap.Int("active_replicas", activeCount),
			)

			if hc.processManager != nil {
				d := &deployments.Deployment{
					ID:        dep.ID,
					Namespace: dep.Namespace,
					Name:      dep.Name,
					Type:      deployments.DeploymentType(dep.Type),
					Port:      dep.Port,
				}
				if err := hc.processManager.Stop(ctx, d); err != nil {
					hc.logger.Error("Failed to stop zombie deployment process", zap.Error(err))
				}
			}

			deleteQuery := `DELETE FROM deployment_replicas WHERE deployment_id = ? AND node_id = ? AND status = 'failed'`
			hc.db.Exec(ctx, deleteQuery, dep.ID, hc.nodeID)

			eventQuery := `INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, 'zombie_replica_stopped', ?, ?)`
			msg := fmt.Sprintf("Zombie replica on node %s stopped and removed (already at %d active replicas)", hc.nodeID, activeCount)
			hc.db.Exec(ctx, eventQuery, dep.ID, msg, time.Now())
			return
		}

		// Under-replicated — genuine recovery. Bring this replica back.
		hc.logger.Info("Failed replica recovered, marking active",
			zap.String("deployment", dep.Name),
			zap.String("node_id", hc.nodeID),
		)

		replicaQuery := `UPDATE deployment_replicas SET status = 'active', updated_at = ? WHERE deployment_id = ? AND node_id = ?`
		if _, err := hc.db.Exec(ctx, replicaQuery, time.Now(), dep.ID, hc.nodeID); err != nil {
			hc.logger.Error("Failed to recover replica status", zap.Error(err))
			return
		}

		// Recalculate deployment status — may go from 'degraded' back to 'active'
		hc.recalculateDeploymentStatus(ctx, dep.ID)

		eventQuery := `INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, 'replica_recovered', ?, ?)`
		msg := fmt.Sprintf("Replica on node %s recovered and marked active", hc.nodeID)
		hc.db.Exec(ctx, eventQuery, dep.ID, msg, time.Now())
	}
}

// handleUnhealthy processes an unhealthy check result.
func (hc *HealthChecker) handleUnhealthy(ctx context.Context, dep deploymentRow) {
	// Don't take action on already-failed replicas
	if dep.ReplicaStatus == "failed" {
		return
	}

	hc.stateMu.Lock()
	st, exists := hc.states[dep.ID]
	if !exists {
		st = &replicaState{}
		hc.states[dep.ID] = st
	}
	st.consecutiveMisses++
	misses := st.consecutiveMisses
	restarts := st.restartCount
	hc.stateMu.Unlock()

	if misses < consecutiveFailuresThreshold {
		return
	}

	// Reached threshold — decide: restart or mark failed
	maxRestarts := dep.MaxRestartCount
	if maxRestarts == 0 {
		maxRestarts = deployments.DefaultMaxRestartCount
	}

	canRestart := dep.RestartPolicy != string(deployments.RestartPolicyNever) &&
		restarts < maxRestarts &&
		hc.processManager != nil

	if canRestart {
		hc.logger.Info("Attempting restart for unhealthy deployment",
			zap.String("deployment", dep.Name),
			zap.Int("restart_attempt", restarts+1),
			zap.Int("max_restarts", maxRestarts),
		)

		// Build minimal Deployment struct for process manager
		d := &deployments.Deployment{
			ID:        dep.ID,
			Namespace: dep.Namespace,
			Name:      dep.Name,
			Type:      deployments.DeploymentType(dep.Type),
			Port:      dep.Port,
		}

		if err := hc.processManager.Restart(ctx, d); err != nil {
			hc.logger.Error("Failed to restart deployment",
				zap.String("deployment", dep.Name),
				zap.Error(err),
			)
		}

		hc.stateMu.Lock()
		st.restartCount++
		st.consecutiveMisses = 0
		hc.stateMu.Unlock()

		eventQuery := `INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, 'health_restart', ?, ?)`
		msg := fmt.Sprintf("Process restarted on node %s (attempt %d/%d)", hc.nodeID, restarts+1, maxRestarts)
		hc.db.Exec(ctx, eventQuery, dep.ID, msg, time.Now())
		return
	}

	// Restart limit exhausted (or policy is "never") — mark THIS replica as failed
	hc.logger.Error("Marking replica as failed after exhausting restarts",
		zap.String("deployment", dep.Name),
		zap.String("node_id", hc.nodeID),
		zap.Int("restarts_attempted", restarts),
	)

	replicaQuery := `UPDATE deployment_replicas SET status = 'failed', updated_at = ? WHERE deployment_id = ? AND node_id = ?`
	if _, err := hc.db.Exec(ctx, replicaQuery, time.Now(), dep.ID, hc.nodeID); err != nil {
		hc.logger.Error("Failed to mark replica as failed", zap.Error(err))
	}

	// Recalculate deployment status based on remaining active replicas
	hc.recalculateDeploymentStatus(ctx, dep.ID)

	eventQuery := `INSERT INTO deployment_events (deployment_id, event_type, message, created_at) VALUES (?, 'replica_failed', ?, ?)`
	msg := fmt.Sprintf("Replica on node %s marked failed after %d restart attempts", hc.nodeID, restarts)
	hc.db.Exec(ctx, eventQuery, dep.ID, msg, time.Now())
}

// recalculateDeploymentStatus sets the deployment to 'active', 'degraded', or 'failed'
// based on the number of remaining active replicas.
func (hc *HealthChecker) recalculateDeploymentStatus(ctx context.Context, deploymentID string) {
	type countRow struct {
		Count int `db:"c"`
	}

	var rows []countRow
	countQuery := `SELECT COUNT(*) as c FROM deployment_replicas WHERE deployment_id = ? AND status = 'active'`
	if err := hc.db.Query(ctx, &rows, countQuery, deploymentID); err != nil {
		hc.logger.Error("Failed to count active replicas", zap.Error(err))
		return
	}

	activeCount := 0
	if len(rows) > 0 {
		activeCount = rows[0].Count
	}

	var newStatus string
	switch {
	case activeCount == 0:
		newStatus = "failed"
	case activeCount < defaultDesiredReplicas:
		newStatus = "degraded"
	default:
		newStatus = "active"
	}

	updateQuery := `UPDATE deployments SET status = ?, updated_at = ? WHERE id = ?`
	if _, err := hc.db.Exec(ctx, updateQuery, newStatus, time.Now(), deploymentID); err != nil {
		hc.logger.Error("Failed to update deployment status",
			zap.String("deployment", deploymentID),
			zap.String("new_status", newStatus),
			zap.Error(err),
		)
	}
}

// recordHealthCheck records the health check result in the database.
func (hc *HealthChecker) recordHealthCheck(ctx context.Context, deploymentID string, healthy bool) {
	status := "healthy"
	if !healthy {
		status = "unhealthy"
	}

	query := `
		INSERT INTO deployment_health_checks (deployment_id, node_id, status, checked_at, response_time_ms)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := hc.db.Exec(ctx, query, deploymentID, hc.nodeID, status, time.Now(), 0)
	if err != nil {
		hc.logger.Error("Failed to record health check",
			zap.String("deployment", deploymentID),
			zap.Error(err),
		)
	}
}

// reconcileDeployments checks for under-replicated deployments and triggers re-replication.
// Only runs on the RQLite leader to avoid duplicate repairs.
func (hc *HealthChecker) reconcileDeployments(ctx context.Context) {
	if hc.reconciler == nil || hc.provisioner == nil {
		return
	}

	if !hc.isRQLiteLeader(ctx) {
		return
	}

	hc.logger.Info("Running deployment reconciliation check")

	type reconcileRow struct {
		ID              string `db:"id"`
		Namespace       string `db:"namespace"`
		Name            string `db:"name"`
		Type            string `db:"type"`
		HomeNodeID      string `db:"home_node_id"`
		ContentCID      string `db:"content_cid"`
		BuildCID        string `db:"build_cid"`
		Environment     string `db:"environment"`
		Port            int    `db:"port"`
		HealthCheckPath string `db:"health_check_path"`
		MemoryLimitMB   int    `db:"memory_limit_mb"`
		CPULimitPercent int    `db:"cpu_limit_percent"`
		RestartPolicy   string `db:"restart_policy"`
		MaxRestartCount int    `db:"max_restart_count"`
		ActiveReplicas  int    `db:"active_replicas"`
	}

	var rows []reconcileRow
	query := `
		SELECT d.id, d.namespace, d.name, d.type, d.home_node_id,
		       d.content_cid, d.build_cid, d.environment, d.port,
		       d.health_check_path, d.memory_limit_mb, d.cpu_limit_percent,
		       d.restart_policy, d.max_restart_count,
		       (SELECT COUNT(*) FROM deployment_replicas dr
		        WHERE dr.deployment_id = d.id AND dr.status = 'active') AS active_replicas
		FROM deployments d
		WHERE d.status IN ('active', 'degraded')
		  AND d.type IN ('nextjs', 'nodejs-backend', 'go-backend')
	`

	if err := hc.db.Query(ctx, &rows, query); err != nil {
		hc.logger.Error("Failed to query deployments for reconciliation", zap.Error(err))
		return
	}

	for _, row := range rows {
		if row.ActiveReplicas >= defaultDesiredReplicas {
			continue
		}

		needed := defaultDesiredReplicas - row.ActiveReplicas
		hc.logger.Warn("Deployment under-replicated, triggering re-replication",
			zap.String("deployment", row.Name),
			zap.String("namespace", row.Namespace),
			zap.Int("active_replicas", row.ActiveReplicas),
			zap.Int("desired", defaultDesiredReplicas),
			zap.Int("needed", needed),
		)

		newNodes, err := hc.reconciler.SelectReplicaNodes(ctx, row.HomeNodeID, needed)
		if err != nil || len(newNodes) == 0 {
			hc.logger.Warn("No nodes available for re-replication",
				zap.String("deployment", row.Name),
				zap.Error(err),
			)
			continue
		}

		dep := &deployments.Deployment{
			ID:              row.ID,
			Namespace:       row.Namespace,
			Name:            row.Name,
			Type:            deployments.DeploymentType(row.Type),
			HomeNodeID:      row.HomeNodeID,
			ContentCID:      row.ContentCID,
			BuildCID:        row.BuildCID,
			Port:            row.Port,
			HealthCheckPath: row.HealthCheckPath,
			MemoryLimitMB:   row.MemoryLimitMB,
			CPULimitPercent: row.CPULimitPercent,
			RestartPolicy:   deployments.RestartPolicy(row.RestartPolicy),
			MaxRestartCount: row.MaxRestartCount,
		}
		if row.Environment != "" {
			json.Unmarshal([]byte(row.Environment), &dep.Environment)
		}

		for _, nodeID := range newNodes {
			hc.logger.Info("Provisioning replacement replica",
				zap.String("deployment", row.Name),
				zap.String("target_node", nodeID),
			)
			go hc.provisioner.SetupDynamicReplica(ctx, dep, nodeID)
		}
	}
}

// isRQLiteLeader checks whether this node is the current Raft leader.
func (hc *HealthChecker) isRQLiteLeader(ctx context.Context) bool {
	dsn := hc.rqliteDSN
	if dsn == "" {
		dsn = "http://localhost:10100"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dsn+"/status", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var status struct {
		Store struct {
			Raft struct {
				State string `json:"state"`
			} `json:"raft"`
		} `json:"store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return false
	}

	return status.Store.Raft.State == "Leader"
}

// GetHealthStatus gets recent health checks for a deployment.
func (hc *HealthChecker) GetHealthStatus(ctx context.Context, deploymentID string, limit int) ([]HealthCheck, error) {
	type healthRow struct {
		Status         string    `db:"status"`
		CheckedAt      time.Time `db:"checked_at"`
		ResponseTimeMs int       `db:"response_time_ms"`
	}

	var rows []healthRow
	query := `
		SELECT status, checked_at, response_time_ms
		FROM deployment_health_checks
		WHERE deployment_id = ?
		ORDER BY checked_at DESC
		LIMIT ?
	`

	err := hc.db.Query(ctx, &rows, query, deploymentID, limit)
	if err != nil {
		return nil, err
	}

	checks := make([]HealthCheck, len(rows))
	for i, row := range rows {
		checks[i] = HealthCheck{
			Status:         row.Status,
			CheckedAt:      row.CheckedAt,
			ResponseTimeMs: row.ResponseTimeMs,
		}
	}

	return checks, nil
}

// HealthCheck represents a health check result.
type HealthCheck struct {
	Status         string    `json:"status"`
	CheckedAt      time.Time `json:"checked_at"`
	ResponseTimeMs int       `json:"response_time_ms"`
}
