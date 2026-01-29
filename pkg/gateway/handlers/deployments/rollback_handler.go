package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// RollbackHandler handles deployment rollbacks
type RollbackHandler struct {
	service       *DeploymentService
	updateHandler *UpdateHandler
	logger        *zap.Logger
}

// NewRollbackHandler creates a new rollback handler
func NewRollbackHandler(service *DeploymentService, updateHandler *UpdateHandler, logger *zap.Logger) *RollbackHandler {
	return &RollbackHandler{
		service:       service,
		updateHandler: updateHandler,
		logger:        logger,
	}
}

// HandleRollback handles deployment rollback
func (h *RollbackHandler) HandleRollback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "deployment name is required", http.StatusBadRequest)
		return
	}

	if req.Version <= 0 {
		http.Error(w, "version must be positive", http.StatusBadRequest)
		return
	}

	h.logger.Info("Rolling back deployment",
		zap.String("namespace", namespace),
		zap.String("name", req.Name),
		zap.Int("target_version", req.Version),
	)

	// Get current deployment
	current, err := h.service.GetDeployment(ctx, namespace, req.Name)
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	// Validate version
	if req.Version >= current.Version {
		http.Error(w, fmt.Sprintf("Cannot rollback to version %d, current version is %d", req.Version, current.Version), http.StatusBadRequest)
		return
	}

	// Get historical version
	history, err := h.getHistoricalVersion(ctx, current.ID, req.Version)
	if err != nil {
		http.Error(w, fmt.Sprintf("Version %d not found in history", req.Version), http.StatusNotFound)
		return
	}

	h.logger.Info("Found historical version",
		zap.String("deployment", req.Name),
		zap.Int("version", req.Version),
		zap.String("cid", history.ContentCID),
	)

	// Perform rollback based on type
	var rolled *deployments.Deployment

	switch current.Type {
	case deployments.DeploymentTypeStatic, deployments.DeploymentTypeNextJSStatic:
		rolled, err = h.rollbackStatic(ctx, current, history)
	case deployments.DeploymentTypeNextJS, deployments.DeploymentTypeNodeJSBackend, deployments.DeploymentTypeGoBackend:
		rolled, err = h.rollbackDynamic(ctx, current, history)
	default:
		http.Error(w, "Unsupported deployment type", http.StatusBadRequest)
		return
	}

	if err != nil {
		h.logger.Error("Rollback failed", zap.Error(err))
		http.Error(w, fmt.Sprintf("Rollback failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return response
	resp := map[string]interface{}{
		"deployment_id":    rolled.ID,
		"name":             rolled.Name,
		"namespace":        rolled.Namespace,
		"status":           rolled.Status,
		"version":          rolled.Version,
		"rolled_back_from": current.Version,
		"rolled_back_to":   req.Version,
		"content_cid":      rolled.ContentCID,
		"updated_at":       rolled.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// getHistoricalVersion retrieves a specific version from history
func (h *RollbackHandler) getHistoricalVersion(ctx context.Context, deploymentID string, version int) (*struct {
	ContentCID string
	BuildCID   string
}, error) {
	type historyRow struct {
		ContentCID string `db:"content_cid"`
		BuildCID   string `db:"build_cid"`
	}

	var rows []historyRow
	query := `
		SELECT content_cid, build_cid
		FROM deployment_history
		WHERE deployment_id = ? AND version = ?
		LIMIT 1
	`

	err := h.service.db.Query(ctx, &rows, query, deploymentID, version)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("version not found")
	}

	return &struct {
		ContentCID string
		BuildCID   string
	}{
		ContentCID: rows[0].ContentCID,
		BuildCID:   rows[0].BuildCID,
	}, nil
}

// rollbackStatic rolls back a static deployment
func (h *RollbackHandler) rollbackStatic(ctx context.Context, current *deployments.Deployment, history *struct {
	ContentCID string
	BuildCID   string
}) (*deployments.Deployment, error) {
	// Atomic CID swap
	newVersion := current.Version + 1
	now := time.Now()

	query := `
		UPDATE deployments
		SET content_cid = ?, version = ?, updated_at = ?
		WHERE namespace = ? AND name = ?
	`

	_, err := h.service.db.Exec(ctx, query, history.ContentCID, newVersion, now, current.Namespace, current.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Record rollback in history
	historyQuery := `
		INSERT INTO deployment_history (
			id, deployment_id, version, content_cid, deployed_at, deployed_by, status, error_message, rollback_from_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	historyID := fmt.Sprintf("%s-v%d", current.ID, newVersion)
	_, err = h.service.db.Exec(ctx, historyQuery,
		historyID,
		current.ID,
		newVersion,
		history.ContentCID,
		now,
		current.Namespace,
		"rolled_back",
		"",
		&current.Version,
	)

	if err != nil {
		h.logger.Error("Failed to record rollback history", zap.Error(err))
	}

	current.ContentCID = history.ContentCID
	current.Version = newVersion
	current.UpdatedAt = now

	h.logger.Info("Static deployment rolled back",
		zap.String("deployment", current.Name),
		zap.Int("new_version", newVersion),
		zap.String("cid", history.ContentCID),
	)

	return current, nil
}

// rollbackDynamic rolls back a dynamic deployment
func (h *RollbackHandler) rollbackDynamic(ctx context.Context, current *deployments.Deployment, history *struct {
	ContentCID string
	BuildCID   string
}) (*deployments.Deployment, error) {
	// Download historical version from IPFS
	cid := history.BuildCID
	if cid == "" {
		cid = history.ContentCID
	}

	deployPath := h.updateHandler.nextjsHandler.baseDeployPath + "/" + current.Namespace + "/" + current.Name
	stagingPath := deployPath + ".rollback"

	// Extract historical version
	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	if err := h.updateHandler.nextjsHandler.extractFromIPFS(ctx, cid, stagingPath); err != nil {
		return nil, fmt.Errorf("failed to extract historical version: %w", err)
	}

	// Backup current
	oldPath := deployPath + ".old"
	if err := renameDirectory(deployPath, oldPath); err != nil {
		return nil, fmt.Errorf("failed to backup current: %w", err)
	}

	// Activate rollback
	if err := renameDirectory(stagingPath, deployPath); err != nil {
		renameDirectory(oldPath, deployPath)
		return nil, fmt.Errorf("failed to activate rollback: %w", err)
	}

	// Restart
	if err := h.updateHandler.processManager.Restart(ctx, current); err != nil {
		renameDirectory(deployPath, stagingPath)
		renameDirectory(oldPath, deployPath)
		h.updateHandler.processManager.Restart(ctx, current)
		return nil, fmt.Errorf("failed to restart: %w", err)
	}

	// Wait for healthy
	if err := h.updateHandler.processManager.WaitForHealthy(ctx, current, 60*time.Second); err != nil {
		h.logger.Warn("Rollback unhealthy, reverting", zap.Error(err))
		renameDirectory(deployPath, stagingPath)
		renameDirectory(oldPath, deployPath)
		h.updateHandler.processManager.Restart(ctx, current)
		return nil, fmt.Errorf("rollback failed health check: %w", err)
	}

	// Update database
	newVersion := current.Version + 1
	now := time.Now()

	query := `
		UPDATE deployments
		SET build_cid = ?, version = ?, updated_at = ?
		WHERE namespace = ? AND name = ?
	`

	_, err := h.service.db.Exec(ctx, query, cid, newVersion, now, current.Namespace, current.Name)
	if err != nil {
		h.logger.Error("Failed to update database", zap.Error(err))
	}

	// Record rollback in history
	historyQuery := `
		INSERT INTO deployment_history (
			id, deployment_id, version, build_cid, deployed_at, deployed_by, status, rollback_from_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	historyID := fmt.Sprintf("%s-v%d", current.ID, newVersion)
	_, _ = h.service.db.Exec(ctx, historyQuery,
		historyID,
		current.ID,
		newVersion,
		cid,
		now,
		current.Namespace,
		"rolled_back",
		&current.Version,
	)

	// Cleanup
	removeDirectory(oldPath)

	current.BuildCID = cid
	current.Version = newVersion
	current.UpdatedAt = now

	h.logger.Info("Dynamic deployment rolled back",
		zap.String("deployment", current.Name),
		zap.Int("new_version", newVersion),
	)

	return current, nil
}

// HandleListVersions lists all versions of a deployment
func (h *RollbackHandler) HandleListVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}
	name := r.URL.Query().Get("name")

	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	// Get deployment
	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Query history
	type versionRow struct {
		Version    int       `db:"version"`
		ContentCID string    `db:"content_cid"`
		BuildCID   string    `db:"build_cid"`
		DeployedAt time.Time `db:"deployed_at"`
		DeployedBy string    `db:"deployed_by"`
		Status     string    `db:"status"`
	}

	var rows []versionRow
	query := `
		SELECT version, content_cid, build_cid, deployed_at, deployed_by, status
		FROM deployment_history
		WHERE deployment_id = ?
		ORDER BY version DESC
		LIMIT 50
	`

	err = h.service.db.Query(ctx, &rows, query, deployment.ID)
	if err != nil {
		http.Error(w, "Failed to query history", http.StatusInternalServerError)
		return
	}

	versions := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		versions[i] = map[string]interface{}{
			"version":     row.Version,
			"content_cid": row.ContentCID,
			"build_cid":   row.BuildCID,
			"deployed_at": row.DeployedAt,
			"deployed_by": row.DeployedBy,
			"status":      row.Status,
			"is_current":  row.Version == deployment.Version,
		}
	}

	resp := map[string]interface{}{
		"deployment_id":  deployment.ID,
		"name":           deployment.Name,
		"current_version": deployment.Version,
		"versions":       versions,
		"total":          len(versions),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
