package serverless

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// GetFunctionLogs handles GET /v1/functions/{name}/logs
// Retrieves execution logs for a specific function.
func (h *ServerlessHandlers) GetFunctionLogs(w http.ResponseWriter, r *http.Request, name string) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil {
			limit = l
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	logs, err := h.registry.GetLogs(ctx, namespace, name, limit)
	if err != nil {
		h.logger.Error("Failed to get function logs",
			zap.String("name", name),
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to get logs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"logs":      logs,
		"count":     len(logs),
	})
}
