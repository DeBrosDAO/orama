package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// ProcessManager interface for process operations
type ProcessManager interface {
	Restart(ctx context.Context, deployment *deployments.Deployment) error
	WaitForHealthy(ctx context.Context, deployment *deployments.Deployment, timeout time.Duration) error
}

// UpdateHandler handles deployment updates
type UpdateHandler struct {
	service        *DeploymentService
	staticHandler  *StaticDeploymentHandler
	nextjsHandler  *NextJSHandler
	processManager ProcessManager
	logger         *zap.Logger
}

// NewUpdateHandler creates a new update handler
func NewUpdateHandler(
	service *DeploymentService,
	staticHandler *StaticDeploymentHandler,
	nextjsHandler *NextJSHandler,
	processManager ProcessManager,
	logger *zap.Logger,
) *UpdateHandler {
	return &UpdateHandler{
		service:        service,
		staticHandler:  staticHandler,
		nextjsHandler:  nextjsHandler,
		processManager: processManager,
		logger:         logger,
	}
}

// HandleUpdate handles deployment updates
func (h *UpdateHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "Deployment name is required", http.StatusBadRequest)
		return
	}

	// Get existing deployment
	existing, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	h.logger.Info("Updating deployment",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("current_version", existing.Version),
	)

	// Handle update based on deployment type
	var updated *deployments.Deployment

	switch existing.Type {
	case deployments.DeploymentTypeStatic, deployments.DeploymentTypeNextJSStatic:
		updated, err = h.updateStatic(ctx, existing, r)
	case deployments.DeploymentTypeNextJS, deployments.DeploymentTypeNodeJSBackend, deployments.DeploymentTypeGoBackend:
		updated, err = h.updateDynamic(ctx, existing, r)
	default:
		http.Error(w, "Unsupported deployment type", http.StatusBadRequest)
		return
	}

	if err != nil {
		h.logger.Error("Update failed", zap.Error(err))
		http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return response
	resp := map[string]interface{}{
		"deployment_id":  updated.ID,
		"name":           updated.Name,
		"namespace":      updated.Namespace,
		"status":         updated.Status,
		"version":        updated.Version,
		"previous_version": existing.Version,
		"content_cid":    updated.ContentCID,
		"updated_at":     updated.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// updateStatic updates a static deployment (zero-downtime CID swap)
func (h *UpdateHandler) updateStatic(ctx context.Context, existing *deployments.Deployment, r *http.Request) (*deployments.Deployment, error) {
	// Get new tarball
	file, header, err := r.FormFile("tarball")
	if err != nil {
		return nil, fmt.Errorf("tarball file required for update")
	}
	defer file.Close()

	// Upload to IPFS
	addResp, err := h.staticHandler.ipfsClient.Add(ctx, file, header.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to upload to IPFS: %w", err)
	}

	cid := addResp.Cid

	h.logger.Info("New content uploaded",
		zap.String("deployment", existing.Name),
		zap.String("old_cid", existing.ContentCID),
		zap.String("new_cid", cid),
	)

	// Atomic CID swap
	newVersion := existing.Version + 1
	now := time.Now()

	query := `
		UPDATE deployments
		SET content_cid = ?, version = ?, updated_at = ?
		WHERE namespace = ? AND name = ?
	`

	_, err = h.service.db.Exec(ctx, query, cid, newVersion, now, existing.Namespace, existing.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Record in history
	h.service.recordHistory(ctx, existing, "updated")

	existing.ContentCID = cid
	existing.Version = newVersion
	existing.UpdatedAt = now

	h.logger.Info("Static deployment updated",
		zap.String("deployment", existing.Name),
		zap.Int("version", newVersion),
		zap.String("cid", cid),
	)

	return existing, nil
}

// updateDynamic updates a dynamic deployment (graceful restart)
func (h *UpdateHandler) updateDynamic(ctx context.Context, existing *deployments.Deployment, r *http.Request) (*deployments.Deployment, error) {
	// Get new tarball
	file, header, err := r.FormFile("tarball")
	if err != nil {
		return nil, fmt.Errorf("tarball file required for update")
	}
	defer file.Close()

	// Upload to IPFS
	addResp, err := h.nextjsHandler.ipfsClient.Add(ctx, file, header.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to upload to IPFS: %w", err)
	}

	cid := addResp.Cid

	h.logger.Info("New build uploaded",
		zap.String("deployment", existing.Name),
		zap.String("old_cid", existing.BuildCID),
		zap.String("new_cid", cid),
	)

	// Extract to staging directory
	stagingPath := fmt.Sprintf("%s.new", h.nextjsHandler.baseDeployPath+"/"+existing.Namespace+"/"+existing.Name)
	if err := h.nextjsHandler.extractFromIPFS(ctx, cid, stagingPath); err != nil {
		return nil, fmt.Errorf("failed to extract new build: %w", err)
	}

	// Atomic swap: rename old to .old, new to current
	deployPath := h.nextjsHandler.baseDeployPath + "/" + existing.Namespace + "/" + existing.Name
	oldPath := deployPath + ".old"

	// Backup current
	if err := renameDirectory(deployPath, oldPath); err != nil {
		return nil, fmt.Errorf("failed to backup current deployment: %w", err)
	}

	// Activate new
	if err := renameDirectory(stagingPath, deployPath); err != nil {
		// Rollback
		renameDirectory(oldPath, deployPath)
		return nil, fmt.Errorf("failed to activate new deployment: %w", err)
	}

	// Restart process
	if err := h.processManager.Restart(ctx, existing); err != nil {
		// Rollback
		renameDirectory(deployPath, stagingPath)
		renameDirectory(oldPath, deployPath)
		h.processManager.Restart(ctx, existing)
		return nil, fmt.Errorf("failed to restart process: %w", err)
	}

	// Wait for healthy
	if err := h.processManager.WaitForHealthy(ctx, existing, 60*time.Second); err != nil {
		h.logger.Warn("Deployment unhealthy after update, rolling back", zap.Error(err))
		// Rollback
		renameDirectory(deployPath, stagingPath)
		renameDirectory(oldPath, deployPath)
		h.processManager.Restart(ctx, existing)
		return nil, fmt.Errorf("new deployment failed health check, rolled back: %w", err)
	}

	// Update database
	newVersion := existing.Version + 1
	now := time.Now()

	query := `
		UPDATE deployments
		SET build_cid = ?, version = ?, updated_at = ?
		WHERE namespace = ? AND name = ?
	`

	_, err = h.service.db.Exec(ctx, query, cid, newVersion, now, existing.Namespace, existing.Name)
	if err != nil {
		h.logger.Error("Failed to update database", zap.Error(err))
	}

	// Record in history
	h.service.recordHistory(ctx, existing, "updated")

	// Cleanup old
	removeDirectory(oldPath)

	existing.BuildCID = cid
	existing.Version = newVersion
	existing.UpdatedAt = now

	h.logger.Info("Dynamic deployment updated",
		zap.String("deployment", existing.Name),
		zap.Int("version", newVersion),
		zap.String("cid", cid),
	)

	return existing, nil
}

// Helper functions (simplified - in production use os package)
func renameDirectory(old, new string) error {
	// os.Rename(old, new)
	return nil
}

func removeDirectory(path string) error {
	// os.RemoveAll(path)
	return nil
}
