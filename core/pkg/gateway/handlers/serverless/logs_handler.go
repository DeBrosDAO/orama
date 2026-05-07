package serverless

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// GetFunctionLogs handles GET /v1/functions/{name}/logs
//
// Returns invocation history (always populated when the function has been
// invoked) with any associated WASM-emitted log entries nested per record.
// This is the answer to "what happened when this function ran" — the older
// behavior (only WASM-emitted entries) was useless on functions that
// don't call log_info / log_error and surfaced as "No logs found" to users.
//
// Optional query params:
//   - limit:        max records (default 50, capped at 500)
//   - wasm_only=1:  return ONLY WASM-emitted log rows (legacy view)
//
// Response:
//
//	{
//	  "name": "...",
//	  "namespace": "...",
//	  "invocations": [ ...records... ],   // when wasm_only is unset
//	  "logs":        [ ...LogEntry... ],   // when wasm_only=1
//	  "count":       N
//	}
func (h *ServerlessHandlers) GetFunctionLogs(w http.ResponseWriter, r *http.Request, name string) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 500 {
		limit = 500
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Legacy "WASM-emitted only" view. Kept for backward compat — most
	// dashboards / clients should use the default invocations view.
	if r.URL.Query().Get("wasm_only") == "1" {
		logs, err := h.registry.GetLogs(ctx, namespace, name, limit)
		if err != nil {
			h.logger.Error("Failed to get WASM logs",
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
		return
	}

	invocations, err := h.registry.GetInvocations(ctx, namespace, name, limit)
	if err != nil {
		h.logger.Error("Failed to get function invocations",
			zap.String("name", name),
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to get invocations")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":        name,
		"namespace":   namespace,
		"invocations": invocations,
		"count":       len(invocations),
	})
}
