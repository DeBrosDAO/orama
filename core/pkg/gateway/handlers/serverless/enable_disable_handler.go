package serverless

import (
	"context"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// SetEnabledFunction handles POST /v1/functions/{name}/disable and
// POST /v1/functions/{name}/enable.
//
// Plan 11.5 — operators flip a function's status without redeploying
// during incident response. Targets ALL versions by name; the registry
// SetEnabled call does the UPDATE atomically.
//
// On success returns {"status":"ok","function":<name>,"enabled":<bool>}.
// On 404 returns {"error":"function not found"}.
//
// SECURITY NOTE: this is an operator-scope endpoint. The auth middleware
// upstream gates by namespace (JWT or API-key); within a namespace any
// authenticated caller can flip. Tighten with an explicit admin-scope
// check before exposing to multi-tenant production.
func (h *ServerlessHandlers) SetEnabledFunction(w http.ResponseWriter, r *http.Request, name string, enabled bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.registry.SetEnabled(ctx, namespace, name, enabled); err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to set function enabled state")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"function": name,
		"enabled":  enabled,
	})
}
