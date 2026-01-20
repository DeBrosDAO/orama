package serverless

import (
	"context"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// DeleteFunction handles DELETE /v1/functions/{name}
// Deletes a function from the registry.
func (h *ServerlessHandlers) DeleteFunction(w http.ResponseWriter, r *http.Request, name string, version int) {
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

	if err := h.registry.Delete(ctx, namespace, name, version); err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to delete function")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Function deleted successfully",
	})
}
