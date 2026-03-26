package deployments

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// StatsHandler handles on-demand deployment resource stats
type StatsHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	logger         *zap.Logger
	baseDeployPath string
}

// NewStatsHandler creates a new stats handler
func NewStatsHandler(service *DeploymentService, processManager *process.Manager, logger *zap.Logger, baseDeployPath string) *StatsHandler {
	if baseDeployPath == "" {
		baseDeployPath = filepath.Join(os.Getenv("HOME"), ".orama", "deployments")
	}
	return &StatsHandler{
		service:        service,
		processManager: processManager,
		logger:         logger,
		baseDeployPath: baseDeployPath,
	}
}

// HandleStats returns on-demand resource usage for a deployment
func (h *StatsHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
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

	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	deployPath := filepath.Join(h.baseDeployPath, deployment.Namespace, deployment.Name)

	resp := map[string]interface{}{
		"name":   deployment.Name,
		"type":   string(deployment.Type),
		"status": string(deployment.Status),
	}

	if deployment.Port == 0 {
		// Static deployment — only disk
		stats, _ := h.processManager.GetStats(ctx, deployment, deployPath)
		if stats != nil {
			resp["disk_mb"] = float64(stats.DiskBytes) / (1024 * 1024)
		}
	} else {
		// Dynamic deployment — full stats
		stats, err := h.processManager.GetStats(ctx, deployment, deployPath)
		if err != nil {
			h.logger.Warn("Failed to get stats", zap.Error(err))
		}
		if stats != nil {
			resp["pid"] = stats.PID
			resp["uptime_seconds"] = stats.UptimeSecs
			resp["cpu_percent"] = stats.CPUPercent
			resp["memory_rss_mb"] = float64(stats.MemoryRSS) / (1024 * 1024)
			resp["disk_mb"] = float64(stats.DiskBytes) / (1024 * 1024)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
