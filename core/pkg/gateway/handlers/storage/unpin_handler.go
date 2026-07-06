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
		// Idempotent reclaim (bugboard #140/#151): a CID that is already absent
		// from the cluster pinset (never pinned, or already GC'd) is the desired
		// end state, so treat "not pinned / not found" as success rather than a
		// 500. A retention cron re-unpinning already-gone CIDs must not error.
		if isAlreadyUnpinned(err) {
			if uerr := h.updatePinStatus(ctx, path, namespace, false); uerr != nil {
				h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
					zap.Error(uerr), zap.String("cid", path))
			}
			httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path, "already_unpinned": true})
			return
		}
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

// isAlreadyUnpinned reports whether an Unpin error actually means the CID is
// already absent from the cluster pinset — the reclaim goal is already met, so
// it is an idempotent success rather than a failure. IPFS-Cluster reports this
// as a 404 and/or a body containing "not part of the pinset" (bugboard #140).
func isAlreadyUnpinned(err error) bool {
	if err == nil {
		return true
	}
	// Match ONLY the definitive IPFS-Cluster "already absent from the pinset"
	// phrases. A bare 404 / "not found" is ambiguous — it could be an unrelated
	// routing / namespace / restart failure — and must NOT be swallowed: doing
	// so would flip a still-pinned CID to unpinned and orphan the blob
	// (bugboard #140/#151). IPFS-Cluster reports this exact case as
	// "<cid> not part of the pinset".
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not part of the pinset") ||
		strings.Contains(msg, "not pinned")
}
