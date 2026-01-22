package storage

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// UnpinHandler handles DELETE /v1/storage/unpin/:cid.
// It unpins a CID from the IPFS cluster, removing it from persistent storage
// and allowing it to be garbage collected.
func (h *Handlers) UnpinHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodDelete) {
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/unpin/")
	if path == "" {
		httputil.WriteError(w, http.StatusBadRequest, "cid required")
		return
	}

	ctx := r.Context()

	// Get namespace from context for ownership check
	namespace := h.getNamespaceFromContext(ctx)
	if namespace == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "namespace required")
		return
	}

	// Check if namespace owns this CID (namespace isolation)
	hasAccess, err := h.checkCIDOwnership(ctx, path, namespace)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to check CID ownership",
			zap.Error(err), zap.String("cid", path), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusInternalServerError, "failed to verify access")
		return
	}
	if !hasAccess {
		h.logger.ComponentWarn(logging.ComponentGeneral, "namespace attempted to unpin CID they don't own",
			zap.String("cid", path), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusForbidden, "access denied: CID not owned by namespace")
		return
	}

	if err := h.ipfsClient.Unpin(ctx, path); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to unpin CID",
			zap.Error(err), zap.String("cid", path))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to unpin: %v", err))
		return
	}

	// Update pin status in database
	if err := h.updatePinStatus(ctx, path, namespace, false); err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
			zap.Error(err), zap.String("cid", path))
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path})
}
