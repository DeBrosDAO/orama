package deployments

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// ListHandler handles listing deployments
type ListHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	ipfsClient     ipfs.IPFSClient
	logger         *zap.Logger
	baseDeployPath string
}

// NewListHandler creates a new list handler
func NewListHandler(service *DeploymentService, processManager *process.Manager, ipfsClient ipfs.IPFSClient, logger *zap.Logger, baseDeployPath string) *ListHandler {
	return &ListHandler{
		service:        service,
		processManager: processManager,
		ipfsClient:     ipfsClient,
		logger:         logger,
		baseDeployPath: baseDeployPath,
	}
}

// HandleList lists all deployments for a namespace
func (h *ListHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	type deploymentRow struct {
		ID         string    `db:"id"`
		Namespace  string    `db:"namespace"`
		Name       string    `db:"name"`
		Type       string    `db:"type"`
		Version    int       `db:"version"`
		Status     string    `db:"status"`
		ContentCID string    `db:"content_cid"`
		HomeNodeID string    `db:"home_node_id"`
		Port       int       `db:"port"`
		Subdomain  string    `db:"subdomain"`
		CreatedAt  time.Time `db:"created_at"`
		UpdatedAt  time.Time `db:"updated_at"`
	}

	var rows []deploymentRow
	query := `
		SELECT id, namespace, name, type, version, status, content_cid, home_node_id, port, subdomain, created_at, updated_at
		FROM deployments
		WHERE namespace = ?
		ORDER BY created_at DESC
	`

	err := h.service.db.Query(ctx, &rows, query, namespace)
	if err != nil {
		h.logger.Error("Failed to query deployments", zap.Error(err))
		http.Error(w, "Failed to query deployments", http.StatusInternalServerError)
		return
	}

	baseDomain := h.service.BaseDomain()
	deployments := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		shortNodeID := GetShortNodeID(row.HomeNodeID)
		urls := []string{
			"https://" + row.Name + "." + shortNodeID + "." + baseDomain,
		}
		if row.Subdomain != "" {
			urls = append(urls, "https://"+row.Subdomain+"."+baseDomain)
		}

		deployments[i] = map[string]interface{}{
			"id":           row.ID,
			"namespace":    row.Namespace,
			"name":         row.Name,
			"type":         row.Type,
			"version":      row.Version,
			"status":       row.Status,
			"content_cid":  row.ContentCID,
			"home_node_id": row.HomeNodeID,
			"port":         row.Port,
			"subdomain":    row.Subdomain,
			"urls":         urls,
			"created_at":   row.CreatedAt,
			"updated_at":   row.UpdatedAt,
		}
	}

	resp := map[string]interface{}{
		"namespace":   namespace,
		"deployments": deployments,
		"total":       len(deployments),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleGet gets a specific deployment
func (h *ListHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Support both 'name' and 'id' query parameters
	name := r.URL.Query().Get("name")
	id := r.URL.Query().Get("id")

	if name == "" && id == "" {
		http.Error(w, "name or id query parameter is required", http.StatusBadRequest)
		return
	}

	var deployment *deployments.Deployment
	var err error

	if id != "" {
		deployment, err = h.service.GetDeploymentByID(ctx, namespace, id)
	} else {
		deployment, err = h.service.GetDeployment(ctx, namespace, name)
	}
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			h.logger.Error("Failed to get deployment", zap.Error(err))
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	urls := h.service.BuildDeploymentURLs(deployment)

	resp := map[string]interface{}{
		"id":                    deployment.ID,
		"namespace":             deployment.Namespace,
		"name":                  deployment.Name,
		"type":                  deployment.Type,
		"version":               deployment.Version,
		"status":                deployment.Status,
		"content_cid":           deployment.ContentCID,
		"build_cid":             deployment.BuildCID,
		"home_node_id":          deployment.HomeNodeID,
		"port":                  deployment.Port,
		"subdomain":             deployment.Subdomain,
		"urls":                  urls,
		"memory_limit_mb":       deployment.MemoryLimitMB,
		"cpu_limit_percent":     deployment.CPULimitPercent,
		"disk_limit_mb":         deployment.DiskLimitMB,
		"health_check_path":     deployment.HealthCheckPath,
		"health_check_interval": deployment.HealthCheckInterval,
		"restart_policy":        deployment.RestartPolicy,
		"max_restart_count":     deployment.MaxRestartCount,
		"created_at":            deployment.CreatedAt,
		"updated_at":            deployment.UpdatedAt,
		"deployed_by":           deployment.DeployedBy,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleDelete deletes a deployment
func (h *ListHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	// Support both 'name' and 'id' query parameters
	name := r.URL.Query().Get("name")
	id := r.URL.Query().Get("id")

	if name == "" && id == "" {
		http.Error(w, "name or id query parameter is required", http.StatusBadRequest)
		return
	}

	h.logger.Info("Deleting deployment",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("id", id),
	)

	// Get deployment
	var deployment *deployments.Deployment
	var err error

	if id != "" {
		deployment, err = h.service.GetDeploymentByID(ctx, namespace, id)
	} else {
		deployment, err = h.service.GetDeployment(ctx, namespace, name)
	}
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	// 1. Stop systemd service
	if err := h.processManager.Stop(ctx, deployment); err != nil {
		h.logger.Warn("Failed to stop deployment service (may not exist)", zap.Error(err), zap.String("name", deployment.Name))
	}

	// 2. Remove deployment files from disk
	if h.baseDeployPath != "" {
		deployDir := filepath.Join(h.baseDeployPath, deployment.Namespace, deployment.Name)
		if err := os.RemoveAll(deployDir); err != nil {
			h.logger.Warn("Failed to remove deployment files", zap.Error(err), zap.String("path", deployDir))
		}
	}

	// 3. Unpin IPFS content
	if deployment.ContentCID != "" {
		if err := h.ipfsClient.Unpin(ctx, deployment.ContentCID); err != nil {
			h.logger.Warn("Failed to unpin IPFS content", zap.Error(err), zap.String("cid", deployment.ContentCID))
		}
	}

	// 4. Delete subdomain registry
	subdomainQuery := `DELETE FROM global_deployment_subdomains WHERE deployment_id = ?`
	_, _ = h.service.db.Exec(ctx, subdomainQuery, deployment.ID)

	// 5. Delete DNS records
	dnsQuery := `DELETE FROM dns_records WHERE deployment_id = ?`
	_, _ = h.service.db.Exec(ctx, dnsQuery, deployment.ID)

	// 6. Delete deployment record
	query := `DELETE FROM deployments WHERE namespace = ? AND name = ?`
	_, err = h.service.db.Exec(ctx, query, namespace, deployment.Name)
	if err != nil {
		h.logger.Error("Failed to delete deployment", zap.Error(err))
		http.Error(w, "Failed to delete deployment", http.StatusInternalServerError)
		return
	}

	h.logger.Info("Deployment deleted",
		zap.String("id", deployment.ID),
		zap.String("namespace", namespace),
		zap.String("name", name),
	)

	resp := map[string]interface{}{
		"message": "Deployment deleted successfully",
		"name":    name,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
