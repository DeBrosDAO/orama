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
	if err := h.ipfsClient.Unpin(ctx, path); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to unpin CID",
			zap.Error(err), zap.String("cid", path))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to unpin: %v", err))
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path})
}
