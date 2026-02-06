package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/database"
	"go.uber.org/zap"
)

// deploymentRow represents a deployment record for health checking
type deploymentRow struct {
	ID              string `db:"id"`
	Namespace       string `db:"namespace"`
	Name            string `db:"name"`
	Type            string `db:"type"`
	Port            int    `db:"port"`
	HealthCheckPath string `db:"health_check_path"`
	HomeNodeID      string `db:"home_node_id"`
}

// HealthChecker monitors deployment health
type HealthChecker struct {
	db      database.Database
	logger  *zap.Logger
	workers int
	mu      sync.RWMutex
	active  map[string]bool // deployment_id -> is_active
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(db database.Database, logger *zap.Logger) *HealthChecker {
	return &HealthChecker{
		db:      db,
		logger:  logger,
		workers: 10,
		active:  make(map[string]bool),
	}
}

// Start begins health monitoring
func (hc *HealthChecker) Start(ctx context.Context) error {
	hc.logger.Info("Starting health checker", zap.Int("workers", hc.workers))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			hc.logger.Info("Health checker stopped")
			return ctx.Err()
		case <-ticker.C:
			if err := hc.checkAllDeployments(ctx); err != nil {
				hc.logger.Error("Health check cycle failed", zap.Error(err))
			}
		}
	}
}

// checkAllDeployments checks all active deployments
func (hc *HealthChecker) checkAllDeployments(ctx context.Context) error {
	var rows []deploymentRow
	query := `
		SELECT id, namespace, name, type, port, health_check_path, home_node_id
		FROM deployments
		WHERE status = 'active' AND type IN ('nextjs', 'nodejs-backend', 'go-backend')
	`

	err := hc.db.Query(ctx, &rows, query)
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
		}(row)
	}

	wg.Wait()
	return nil
}

// checkDeployment checks a single deployment
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

// recordHealthCheck records the health check result
func (hc *HealthChecker) recordHealthCheck(ctx context.Context, deploymentID string, healthy bool) {
	status := "healthy"
	if !healthy {
		status = "unhealthy"
	}

	query := `
		INSERT INTO deployment_health_checks (deployment_id, status, checked_at, response_time_ms)
		VALUES (?, ?, ?, ?)
	`

	_, err := hc.db.Exec(ctx, query, deploymentID, status, time.Now(), 0)
	if err != nil {
		hc.logger.Error("Failed to record health check",
			zap.String("deployment", deploymentID),
			zap.Error(err),
		)
	}

	// Track consecutive failures
	hc.checkConsecutiveFailures(ctx, deploymentID, healthy)
}

// checkConsecutiveFailures marks deployment as failed after 3 consecutive failures
func (hc *HealthChecker) checkConsecutiveFailures(ctx context.Context, deploymentID string, currentHealthy bool) {
	if currentHealthy {
		return
	}

	type healthRow struct {
		Status string `db:"status"`
	}

	var rows []healthRow
	query := `
		SELECT status
		FROM deployment_health_checks
		WHERE deployment_id = ?
		ORDER BY checked_at DESC
		LIMIT 3
	`

	err := hc.db.Query(ctx, &rows, query, deploymentID)
	if err != nil {
		hc.logger.Error("Failed to query health history", zap.Error(err))
		return
	}

	// Check if last 3 checks all failed
	if len(rows) >= 3 {
		allFailed := true
		for _, row := range rows {
			if row.Status != "unhealthy" {
				allFailed = false
				break
			}
		}

		if allFailed {
			hc.logger.Error("Deployment has 3 consecutive failures, marking as failed",
				zap.String("deployment", deploymentID),
			)

			updateQuery := `
				UPDATE deployments
				SET status = 'failed', updated_at = ?
				WHERE id = ?
			`

			_, err := hc.db.Exec(ctx, updateQuery, time.Now(), deploymentID)
			if err != nil {
				hc.logger.Error("Failed to mark deployment as failed", zap.Error(err))
			}

			// Record event
			eventQuery := `
				INSERT INTO deployment_events (deployment_id, event_type, message, created_at)
				VALUES (?, 'health_failed', 'Deployment marked as failed after 3 consecutive health check failures', ?)
			`
			hc.db.Exec(ctx, eventQuery, deploymentID, time.Now())
		}
	}
}

// GetHealthStatus gets recent health checks for a deployment
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

// HealthCheck represents a health check result
type HealthCheck struct {
	Status         string    `json:"status"`
	CheckedAt      time.Time `json:"checked_at"`
	ResponseTimeMs int       `json:"response_time_ms"`
}
