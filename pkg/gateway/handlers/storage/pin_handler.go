package storage

import (
	"encoding/json"
	"fmt"
	"net/http"

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

	var req StoragePinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode request: %v", err))
		return
	}

	if req.Cid == "" {
		httputil.WriteError(w, http.StatusBadRequest, "cid required")
		return
	}

	// Get replication factor from config (default: 3)
	replicationFactor := h.config.IPFSReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	ctx := r.Context()
	pinResp, err := h.ipfsClient.Pin(ctx, req.Cid, req.Name, replicationFactor)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to pin CID",
			zap.Error(err), zap.String("cid", req.Cid))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to pin: %v", err))
		return
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
