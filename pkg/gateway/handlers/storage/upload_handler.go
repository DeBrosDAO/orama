package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Note: Context keys are imported from the gateway package
// This avoids duplication and ensures compatibility with middleware

// UploadHandler handles POST /v1/storage/upload.
// It supports both multipart/form-data and JSON-based uploads with base64-encoded data.
// Files are added to IPFS and optionally pinned for persistence.
func (h *Handlers) UploadHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodPost) {
		return
	}

	// Get namespace from context
	namespace := h.getNamespaceFromContext(r.Context())
	if namespace == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "namespace required")
		return
	}

	// Get replication factor from config (default: 3)
	replicationFactor := h.config.IPFSReplicationFactor
	if replicationFactor == 0 {
		replicationFactor = 3
	}

	// Check if it's multipart/form-data or JSON
	contentType := r.Header.Get("Content-Type")
	var reader io.Reader
	var name string
	var shouldPin bool = true // Default to true

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Handle multipart upload
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse multipart form: %v", err))
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to get file: %v", err))
			return
		}
		defer file.Close()

		reader = file
		name = header.Filename

		// Parse pin flag from form (default: true)
		if pinValue := r.FormValue("pin"); pinValue != "" {
			shouldPin = strings.ToLower(pinValue) == "true"
		}
	} else {
		// Handle JSON request with base64 data
		var req StorageUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode request: %v", err))
			return
		}

		if req.Data == "" {
			httputil.WriteError(w, http.StatusBadRequest, "data field required")
			return
		}

		// Decode base64 data
		data, err := base64Decode(req.Data)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("failed to decode base64 data: %v", err))
			return
		}

		reader = bytes.NewReader(data)
		name = req.Name
		// For JSON requests, pin defaults to true (can be extended if needed)
	}

	// Add to IPFS
	ctx := r.Context()
	addResp, err := h.ipfsClient.Add(ctx, reader, name)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to add content to IPFS", zap.Error(err))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to add content: %v", err))
		return
	}

	// Return response immediately - don't block on pinning
	response := StorageUploadResponse{
		Cid:  addResp.Cid,
		Name: addResp.Name,
		Size: addResp.Size,
	}

	// Pin asynchronously in background if requested
	if shouldPin {
		go h.pinAsync(addResp.Cid, name, replicationFactor)
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}

// pinAsync pins a CID asynchronously in the background with retry logic.
// It retries once if the first attempt fails, then gives up.
func (h *Handlers) pinAsync(cid, name string, replicationFactor int) {
	ctx := context.Background()

	// First attempt
	_, err := h.ipfsClient.Pin(ctx, cid, name, replicationFactor)
	if err == nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "async pin succeeded", zap.String("cid", cid))
		return
	}

	// Log first failure
	h.logger.ComponentWarn(logging.ComponentGeneral, "async pin failed, retrying once",
		zap.Error(err), zap.String("cid", cid))

	// Retry once after a short delay
	time.Sleep(2 * time.Second)
	_, err = h.ipfsClient.Pin(ctx, cid, name, replicationFactor)
	if err != nil {
		// Final failure - log and give up
		h.logger.ComponentWarn(logging.ComponentGeneral, "async pin retry failed, giving up",
			zap.Error(err), zap.String("cid", cid))
	} else {
		h.logger.ComponentWarn(logging.ComponentGeneral, "async pin succeeded on retry", zap.String("cid", cid))
	}
}

// base64Decode decodes a base64 string to bytes.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
