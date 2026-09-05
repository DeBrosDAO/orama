package storage

import (
	"encoding/json"
	"fmt"
	"net/http"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// PinHandler handles POST /v1/storage/pin.
// It pins an existing CID in the IPFS cluster, ensuring the content
// is replicated across the configured number of cluster peers.
func (h *Handlers) PinHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodPost) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req StoragePinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode request: %v", err))
		return
	}

	if req.Cid == "" {
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
	hasAccess, err := h.checkCIDOwnership(ctx, req.Cid, namespace)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to check CID ownership",
			zap.Error(err), zap.String("cid", req.Cid), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusInternalServerError, "failed to verify access")
		return
	}
	if !hasAccess {
		h.logger.ComponentWarn(logging.ComponentGeneral, "namespace attempted to pin CID they don't own",
			zap.String("cid", req.Cid), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusForbidden, "access denied: CID not owned by namespace")
		return
	}

	// Pinning is what makes the platform keep an object, so it is a write.
	if !h.authorizeCID(w, r, req.Cid, namespace, gwauth.ActionWrite) {
		return
	}

	// Server-side storage quota (bugboard #141). The CID is already owned, so
	// re-pinning adds no NEW logical bytes (additional=0) — but reject if the
	// namespace is already over its configured budget. Unlimited when unset;
	// fail-open on a transient quota-lookup error.
	if exceeded, budget, projected, qErr := h.storageQuotaExceeded(ctx, namespace, 0); qErr != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "storage quota check failed; allowing pin (fail-open)",
			zap.Error(qErr), zap.String("namespace", namespace))
	} else if exceeded {
		h.logger.ComponentWarn(logging.ComponentGeneral, "pin rejected: storage quota exceeded",
			zap.String("namespace", namespace),
			zap.Int64("budget_bytes", budget),
			zap.Int64("projected_bytes", projected))
		httputil.WriteRPCError(w, http.StatusRequestEntityTooLarge, httputil.ErrCodeStorageQuotaExceeded,
			fmt.Sprintf("namespace storage quota exceeded: %d bytes (× RF) exceeds budget %d bytes", projected, budget))
		return
	}

	// Get replication factor from config (default: 3)
	replicationFactor := h.config.IPFSReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	pinResp, err := h.ipfsClient.Pin(ctx, req.Cid, req.Name, replicationFactor)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to pin CID",
			zap.Error(err), zap.String("cid", req.Cid))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to pin: %v", err))
		return
	}

	// Update pin status in database
	if err := h.updatePinStatus(ctx, req.Cid, namespace, true); err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "failed to update pin status in database (non-fatal)",
			zap.Error(err), zap.String("cid", req.Cid))
	}

	// Use name from request if response doesn't have it
	name := pinResp.Name
	if name == "" {
		name = req.Name
	}

	response := StoragePinResponse{
		Cid:  pinResp.Cid,
		Name: name,
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}
