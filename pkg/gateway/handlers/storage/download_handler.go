package storage

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// DownloadHandler handles GET /v1/storage/get/:cid.
// It retrieves content from IPFS by CID and streams it to the client.
// The content is returned as an octet-stream with a content-disposition header.
func (h *Handlers) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodGet) {
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/get/")
	if path == "" {
		httputil.WriteError(w, http.StatusBadRequest, "cid required")
		return
	}

	// Get namespace from context
	namespace := h.getNamespaceFromContext(r.Context())
	if namespace == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "namespace required")
		return
	}

	ctx := r.Context()

	// Check if namespace owns this CID (namespace isolation)
	hasAccess, err := h.checkCIDOwnership(ctx, path, namespace)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to check CID ownership",
			zap.Error(err), zap.String("cid", path), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusInternalServerError, "failed to verify access")
		return
	}
	if !hasAccess {
		h.logger.ComponentWarn(logging.ComponentGeneral, "namespace attempted to access CID they don't own",
			zap.String("cid", path), zap.String("namespace", namespace))
		httputil.WriteError(w, http.StatusForbidden, "access denied: CID not owned by namespace")
		return
	}

	// Get IPFS API URL from config
	ipfsAPIURL := h.config.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = "http://localhost:5001"
	}

	reader, err := h.ipfsClient.Get(ctx, path, ipfsAPIURL)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to get content from IPFS",
			zap.Error(err), zap.String("cid", path))

		// Check if error indicates content not found (404)
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			httputil.WriteError(w, http.StatusNotFound, fmt.Sprintf("content not found: %s", path))
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get content: %v", err))
		}
		return
	}
	defer reader.Close()

	// Set headers for file download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", path))

	// Stream content to client
	if _, err := io.Copy(w, reader); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to write content", zap.Error(err))
	}
}

// StatusHandler handles GET /v1/storage/status/:cid.
// It retrieves the pin status of a CID from the IPFS cluster,
// including replication information and peer distribution.
func (h *Handlers) StatusHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodGet) {
		return
	}

	// Extract CID from path
	path := strings.TrimPrefix(r.URL.Path, "/v1/storage/status/")
	if path == "" {
		httputil.WriteError(w, http.StatusBadRequest, "cid required")
		return
	}

	ctx := r.Context()
	status, err := h.ipfsClient.PinStatus(ctx, path)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to get pin status",
			zap.Error(err), zap.String("cid", path))

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			httputil.WriteError(w, http.StatusNotFound, fmt.Sprintf("pin not found: %s", path))
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get status: %v", err))
		}
		return
	}

	response := StorageStatusResponse{
		Cid:               status.Cid,
		Name:              status.Name,
		Status:            status.Status,
		ReplicationMin:    status.ReplicationMin,
		ReplicationMax:    status.ReplicationMax,
		ReplicationFactor: status.ReplicationFactor,
		Peers:             status.Peers,
		Error:             status.Error,
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}
