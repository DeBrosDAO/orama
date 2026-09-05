package storage

import (
	"context"
	"net/http"
	"strings"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
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

	// bugboard #153: opt-in privacy-grade immediate reclaim. Default (absent or
	// non-"true") keeps the cheap ~6h GC-sweep behaviour; ?immediate=true evicts
	// the blob cluster-wide the moment its last pin is removed.
	immediate := r.URL.Query().Get("immediate") == "true"

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

	if !h.authorizeCID(w, r, path, namespace, gwauth.ActionWrite) {
		return
	}

	// bugboard #156: the same CID can be pinned by multiple namespaces and
	// IPFS-Cluster dedups to ONE pin per CID. Removing the cluster pin while
	// another namespace still references the content would orphan that
	// namespace's data at the next GC. So gate the cluster-pin removal on a
	// cross-namespace reference check: only the LAST pinner actually removes the
	// cluster pin; a non-last unpin just marks this namespace's row unpinned.
	sharedByOthers, refErr := h.cidPinnedByOtherNamespace(ctx, path, namespace)
	if refErr != nil {
		// Can't confirm we're the last pinner — fail safe: do NOT remove the
		// shared cluster pin (a leaked pin is recoverable via a later unpin/GC;
		// deleting another namespace's live data is not). Still mark this
		// namespace logically unpinned.
		h.logger.ComponentWarn(logging.ComponentGeneral, "unpin: cross-namespace reference check failed; leaving cluster pin intact (fail-safe)",
			zap.Error(refErr), zap.String("cid", path), zap.String("namespace", namespace))
		if uerr := h.updatePinStatus(ctx, path, namespace, false); uerr != nil {
			h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
				zap.Error(uerr), zap.String("cid", path))
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path, "evicted": "skipped"})
		return
	}
	if sharedByOthers {
		// Another namespace still pins this CID — leave the cluster pin and the
		// blocks intact; this namespace is only logically unpinned.
		if uerr := h.updatePinStatus(ctx, path, namespace, false); uerr != nil {
			h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
				zap.Error(uerr), zap.String("cid", path))
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path, "shared": true, "evicted": "shared"})
		return
	}

	// Last pinner — safe to remove the cluster pin.
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
			evicted := h.maybeImmediateEvict(ctx, path, immediate)
			httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path, "already_unpinned": true, "evicted": evicted})
			return
		}
		h.logger.ComponentError(logging.ComponentGeneral, "failed to unpin CID",
			zap.Error(err), zap.String("cid", path))
		// Don't leak internal cluster/kubo error text to the tenant.
		httputil.WriteError(w, http.StatusInternalServerError, "failed to unpin")
		return
	}

	// Update pin status in database
	if err := h.updatePinStatus(ctx, path, namespace, false); err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
			zap.Error(err), zap.String("cid", path))
	}

	evicted := h.maybeImmediateEvict(ctx, path, immediate)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "cid": path, "evicted": evicted})
}

// maybeImmediateEvict performs privacy-grade immediate reclaim when the caller
// opted in via ?immediate=true (bugboard #153). It evicts ONLY a CID whose last
// pin was just removed — remainingPinsForCID == 0 across all namespaces — so a
// CID still referenced by another namespace (shared, deduped content) is never
// destroyed. Best-effort and never fails the unpin: the cluster pin is already
// gone, so eviction is disk reclaim + privacy hardening on top. Returns a status
// for the response: "skipped" (not requested / ref-check failed), "shared"
// (another namespace still pins it), "true" (all nodes reclaimed), or "partial".
func (h *Handlers) maybeImmediateEvict(ctx context.Context, cid string, immediate bool) string {
	if !immediate {
		return "skipped"
	}
	remaining, err := h.remainingPinsForCID(ctx, cid)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "immediate evict: reference check failed (skipping)",
			zap.String("cid", cid), zap.Error(err))
		return "skipped"
	}
	if remaining > 0 {
		return "shared"
	}
	if h.evictBlobEverywhere(ctx, cid) {
		return "true"
	}
	return "partial"
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
