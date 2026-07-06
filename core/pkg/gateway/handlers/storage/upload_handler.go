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
	"github.com/DeBrosOfficial/network/pkg/ipfs"
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
	var inputSize int64
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
		inputSize = header.Size

		// Parse pin flag from form (default: true)
		if pinValue := r.FormValue("pin"); pinValue != "" {
			shouldPin = strings.ToLower(pinValue) == "true"
		}
	} else {
		// Handle JSON request with base64 data
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
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
		inputSize = int64(len(data))
		// For JSON requests, pin defaults to true (can be extended if needed)
	}

	ctx := r.Context()

	// Server-side per-namespace storage quota (bugboard #141). Reject BEFORE we
	// add/pin so the namespace's RF-inclusive budget is a real ceiling, not a
	// client-trusted gate. A namespace with no configured budget is unlimited
	// (no-op). Fail-open on a transient quota-lookup error — availability over a
	// hard cap for a DB hiccup.
	//
	// inputSize is the server-observed byte count (multipart part size / decoded
	// JSON length), NOT a client-asserted field — a client can't understate it
	// without sending fewer bytes. The recorded ledger uses addResp.Size (the
	// IPFS DAG-wrapped size, marginally larger due to UnixFS framing), so the
	// enforced cap is approximate at the framing-overhead level — acceptable for
	// a coarse byte quota. (Pre-GA hardening: authoritative record-then-check
	// with rollback + per-namespace serialization to close the concurrent-burst
	// TOCTOU; deferred while enforcement is opt-in and unused.)
	if exceeded, budget, projected, qErr := h.storageQuotaExceeded(ctx, namespace, inputSize); qErr != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "storage quota check failed; allowing upload (fail-open)",
			zap.Error(qErr), zap.String("namespace", namespace))
	} else if exceeded {
		h.logger.ComponentWarn(logging.ComponentGeneral, "upload rejected: storage quota exceeded",
			zap.String("namespace", namespace),
			zap.Int64("budget_bytes", budget),
			zap.Int64("projected_bytes", projected))
		httputil.WriteRPCError(w, http.StatusRequestEntityTooLarge, httputil.ErrCodeStorageQuotaExceeded,
			fmt.Sprintf("namespace storage quota exceeded: projected %d bytes ((logical+new) × RF) exceeds budget %d bytes", projected, budget))
		return
	}

	// Add to IPFS
	addResp, err := h.ipfsClient.Add(ctx, reader, name)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to add content to IPFS", zap.Error(err))
		httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to add content: %v", err))
		return
	}

	// Record ownership in database for namespace isolation
	// Use wallet or API key as uploaded_by identifier
	uploadedBy := namespace // Could be enhanced to track wallet address if available
	if err := h.recordCIDOwnership(ctx, addResp.Cid, namespace, addResp.Name, uploadedBy, addResp.Size); err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "failed to record CID ownership (non-fatal)",
			zap.Error(err), zap.String("cid", addResp.Cid), zap.String("namespace", namespace))
		// Don't fail the upload - this is just for tracking
	}

	// Return response immediately - don't block on pinning
	response := StorageUploadResponse{
		Cid:  addResp.Cid,
		Name: addResp.Name,
		Size: addResp.Size,
	}

	// Pin asynchronously in background if requested
	if shouldPin {
		go h.pinAsync(addResp.Cid, name, replicationFactor, namespace)
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}

// pinVerify tuning for the async upload path. IPFS-Cluster's pin is async
// per-peer, so a Pin call returning success doesn't mean the block is durable
// yet. We best-effort poll PinStatus a few times to surface a stuck pin in the
// logs — but we never fail the upload (its response already went out).
const (
	pinVerifyAttempts = 3
	pinVerifyInterval = 2 * time.Second
)

// pinAsync pins a CID asynchronously in the background with retry logic.
// It retries once if the first attempt fails, then gives up.
func (h *Handlers) pinAsync(cid, name string, replicationFactor int, namespace string) {
	ctx := context.Background()

	// First attempt
	_, err := h.ipfsClient.Pin(ctx, cid, name, replicationFactor)
	if err == nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "async pin succeeded", zap.String("cid", cid))
		// Update pin status in database
		h.updatePinStatus(ctx, cid, namespace, true)
		h.verifyPinDurable(ctx, cid)
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
		// Update pin status in database
		h.updatePinStatus(ctx, cid, namespace, true)
		h.verifyPinDurable(ctx, cid)
	}
}

// verifyPinDurable best-effort polls PinStatus a few times after a successful
// pin to confirm the async cluster pin actually reached "pinned". It only logs
// a Warn on non-convergence — it never blocks or fails the upload (the response
// has already been returned to the client).
func (h *Handlers) verifyPinDurable(ctx context.Context, cid string) {
	for attempt := 1; attempt <= pinVerifyAttempts; attempt++ {
		status, err := h.ipfsClient.PinStatus(ctx, cid)
		if err == nil && status != nil && status.Status == ipfs.PinStatusPinned {
			return
		}
		if attempt < pinVerifyAttempts {
			time.Sleep(pinVerifyInterval)
		}
	}
	h.logger.ComponentWarn(logging.ComponentGeneral, "pin not confirmed durable after verify polls (may still be converging)",
		zap.String("cid", cid))
}

// base64Decode decodes a base64 string to bytes.
func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
