package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
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
	if err := process.ValidateInstance(namespace, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// An update, a rollback and an environment change each restart the unit;
	// taking turns keeps one from restarting it onto a mix of two of them.
	unlock, err := h.service.lockDeployment(ctx, namespace, name)
	if err != nil {
		http.Error(w, err.Error()+"; run the command again", http.StatusServiceUnavailable)
		return
	}
	defer unlock()

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
		var taken *instanceTakenError
		if errors.As(err, &taken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
		return
	}

	// An update replaces what is running under an existing name, so it is a
	// deploy in the record.
	h.service.RecordAudit(r, updated.Namespace, auth.AuditDeploymentCreated, updated.Name)

	// The update is not done until every replica runs it: until then the
	// hostname answers with two versions. A replica that did not apply it is
	// named, and running the update again retries.
	if err := h.service.updateReplicasWithin(ctx, w, updated, replicaUpdatePath); err != nil {
		h.logger.Error("Update not applied on every replica", zap.Error(err))
		http.Error(w, fmt.Sprintf(
			"updated to version %d on the home node, but %v. Those nodes still serve the old version; "+
				"run the same update again to retry", updated.Version, err), http.StatusBadGateway)
		return
	}

	// Return response
	resp := map[string]interface{}{
		"deployment_id":    updated.ID,
		"name":             updated.Name,
		"namespace":        updated.Namespace,
		"status":           updated.Status,
		"version":          updated.Version,
		"previous_version": existing.Version,
		"content_cid":      updated.ContentCID,
		"urls":             h.service.BuildDeploymentURLs(updated),
		"updated_at":       updated.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// updateStatic updates a static deployment (zero-downtime CID swap)
func (h *UpdateHandler) updateStatic(ctx context.Context, existing *deployments.Deployment, r *http.Request) (*deployments.Deployment, error) {
	// Get new tarball
	file, _, err := r.FormFile("tarball")
	if err != nil {
		return nil, fmt.Errorf("tarball file required for update")
	}
	defer file.Close()

	cid, err := uploadSite(ctx, h.staticHandler.ipfsClient, file)
	if err != nil {
		return nil, err
	}

	oldContentCID := existing.ContentCID

	h.logger.Info("New content uploaded",
		zap.String("deployment", existing.Name),
		zap.String("old_cid", oldContentCID),
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

	registered, err := h.service.registerCIDs(ctx, existing.Namespace, cid)
	if err != nil {
		return nil, err
	}
	_, err = h.service.db.Exec(ctx, query, cid, newVersion, now, existing.Namespace, existing.Name)
	if err != nil {
		h.service.unregisterCIDs(ctx, existing.Namespace, registered)
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Unpin old IPFS content (best-effort)
	if oldContentCID != "" && oldContentCID != cid {
		if unpinErr := h.service.releaseCID(ctx, h.staticHandler.ipfsClient, existing.ID, existing.Namespace, oldContentCID); unpinErr != nil {
			h.logger.Warn("Failed to unpin old content CID", zap.String("cid", oldContentCID), zap.Error(unpinErr))
		}
	}

	existing.ContentCID = cid
	existing.Version = newVersion
	existing.UpdatedAt = now

	// History holds one row per version, so the row is the deployment as it
	// now is, not as it was before this update.
	h.service.recordHistory(ctx, existing, "updated")

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

	oldBuildCID := existing.BuildCID

	// The new build is recorded before anything is replaced, and the record is
	// dropped again unless the database update below commits it, so an update
	// that fails half way does not hold the CID referenced for ever.
	registered, err := h.service.registerCIDs(ctx, existing.Namespace, cid)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			h.service.unregisterCIDs(ctx, existing.Namespace, registered)
		}
	}()

	h.logger.Info("New build uploaded",
		zap.String("deployment", existing.Name),
		zap.String("old_cid", oldBuildCID),
		zap.String("new_cid", cid),
	)

	// The directory on this host must be this deployment's before anything
	// replaces it (instance_claim.go).
	deployPath := process.DeployDir(h.nextjsHandler.baseDeployPath, existing.Namespace, existing.Name)
	if err := h.service.checkInstanceOwner(ctx, deployPath, existing.Namespace, existing.Name); err != nil {
		return nil, err
	}

	// Extract to staging directory
	stagingPath := deployPath + ".new"
	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	if err := h.nextjsHandler.extractFromIPFS(ctx, cid, stagingPath); err != nil {
		return nil, fmt.Errorf("failed to extract new build: %w", err)
	}
	// The staged directory replaces the claimed one, so it carries the marker.
	if err := writeOwnerMarker(stagingPath, existing.Namespace, existing.Name, false); err != nil {
		return nil, err
	}

	// Atomic swap: rename old to .old, new to current
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
	} else {
		committed = true
	}

	// Cleanup old
	removeDirectory(oldPath)

	// Unpin old IPFS build (best-effort)
	if committed && oldBuildCID != "" && oldBuildCID != cid {
		if unpinErr := h.service.releaseCID(ctx, h.nextjsHandler.ipfsClient, existing.ID, existing.Namespace, oldBuildCID); unpinErr != nil {
			h.logger.Warn("Failed to unpin old build CID", zap.String("cid", oldBuildCID), zap.Error(unpinErr))
		}
	}

	existing.BuildCID = cid
	existing.Version = newVersion
	existing.UpdatedAt = now

	// History holds one row per version: the deployment as it now is. A commit
	// that failed above leaves the version unchanged, so it has no row.
	if committed {
		h.service.recordHistory(ctx, existing, "updated")
	}

	h.logger.Info("Dynamic deployment updated",
		zap.String("deployment", existing.Name),
		zap.Int("version", newVersion),
		zap.String("cid", cid),
	)

	return existing, nil
}

// Helper functions for filesystem operations
func renameDirectory(old, new string) error {
	return os.Rename(old, new)
}

func removeDirectory(path string) error {
	return os.RemoveAll(path)
}
