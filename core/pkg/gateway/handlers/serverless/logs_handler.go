package serverless

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
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
	namespace, ok := managedNamespace(w, r)
	if !ok {
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
		if len(logs) == 0 && !h.functionKnown(ctx, w, namespace, name) {
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
	if len(invocations) == 0 && !h.functionKnown(ctx, w, namespace, name) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":        name,
		"namespace":   namespace,
		"invocations": invocations,
		"count":       len(invocations),
	})
}

// functionKnown is asked when a function has no history to show: an empty
// answer for a function that does not exist is a 404, not a 200 with nothing
// in it, which a caller cannot tell from a function that has not run yet. A
// function that was deleted keeps the history it made and never gets here. It
// reports false after writing the response.
func (h *ServerlessHandlers) functionKnown(ctx context.Context, w http.ResponseWriter, namespace, name string) bool {
	if _, err := h.registry.Get(ctx, namespace, name, 0); err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return false
	}
	return true
}
