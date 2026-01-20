package serverless

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// ListFunctions handles GET /v1/functions
// Lists all functions in a namespace.
func (h *ServerlessHandlers) ListFunctions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		// Get namespace from JWT if available
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	functions, err := h.registry.List(ctx, namespace)
	if err != nil {
		h.logger.Error("Failed to list functions",
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to list functions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"functions": functions,
		"count":     len(functions),
	})
}
