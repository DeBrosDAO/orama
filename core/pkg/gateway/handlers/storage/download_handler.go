package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	gocid "github.com/ipfs/go-cid"
	"go.uber.org/zap"
)

const (
	// storageFetchTimeout bounds a download's IPFS retrieval, networked fetch
	// included. It sits below the main gateway's 30s proxy budget, so a fetch
	// that cannot finish is answered here with a classified TIMEOUT instead
	// of being cut off by the proxy, whose error cannot say what was slow.
	storageFetchTimeout = 20 * time.Second

	// pinPropagationWindow is how long after an upload or pin request a CID
	// missing from this node's pinset is reported as not yet visible
	// (retryable) rather than gone. The cluster's add pins before it returns;
	// the pinset is CRDT state, so another peer normally sees the pin within a
	// second, and one that missed the gossip catches up on the next
	// rebroadcast, one minute by default.
	pinPropagationWindow = 2 * time.Minute
)

// DownloadHandler handles GET /v1/storage/get/:cid.
// It retrieves content from IPFS by CID and streams it to the client.
// The content is returned as an octet-stream with a content-disposition header.
//
// Failures carry an RPC error code so a client can tell them apart
// (bugboard #414): NOT_FOUND is final once a fresh upload has had time to
// propagate, while TIMEOUT and SERVICE_UNAVAILABLE are worth retrying.
func (h *Handlers) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	if h.ipfsClient == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable, httputil.ErrCodeServiceUnavailable, "IPFS storage not available")
		return
	}

	if !httputil.CheckMethod(w, r, http.MethodGet) {
		return
	}

	cid := strings.TrimPrefix(r.URL.Path, "/v1/storage/get/")
	if cid == "" {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed, "cid required")
		return
	}

	namespace := h.getNamespaceFromContext(r.Context())
	if namespace == "" {
		httputil.WriteRPCError(w, http.StatusUnauthorized, httputil.ErrCodeUnauthorized, "namespace required")
		return
	}

	// Only the canonical spelling is accepted: it is what an upload records,
	// and a non-canonical one (an identity multihash can carry arbitrary
	// bytes) is not safe to hand on to IPFS.
	if parsed, err := gocid.Decode(cid); err != nil || parsed.String() != cid {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			fmt.Sprintf("%q is not a valid CID in canonical form", cid))
		return
	}

	if !h.authorizeDownload(w, r, cid, namespace) {
		return
	}

	reader, ok := h.fetchStored(r.Context(), w, cid, namespace)
	if !ok {
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", cid))

	if _, err := io.Copy(w, reader); err != nil {
		if r.Context().Err() != nil {
			h.logger.ComponentDebug(logging.ComponentGeneral, "client went away mid-download", zap.String("cid", cid))
			return
		}
		h.logger.ComponentError(logging.ComponentGeneral, "failed to write content", zap.Error(err))
	}
}

// authorizeDownload applies the namespace boundary (the namespace must own
// the CID) and then the grant's storage selector inside it, before anything
// asks IPFS about the CID. Writes the refusal itself.
func (h *Handlers) authorizeDownload(w http.ResponseWriter, r *http.Request, cid, namespace string) bool {
	hasAccess, err := h.checkCIDOwnership(r.Context(), cid, namespace)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to check CID ownership",
			zap.Error(err), zap.String("cid", cid), zap.String("namespace", namespace))
		httputil.WriteRPCError(w, http.StatusInternalServerError, httputil.ErrCodeInternal, "failed to verify access")
		return false
	}
	if !hasAccess {
		h.logger.ComponentWarn(logging.ComponentGeneral, "namespace attempted to access CID they don't own",
			zap.String("cid", cid), zap.String("namespace", namespace))
		httputil.WriteRPCError(w, http.StatusForbidden, httputil.ErrCodeForbidden, "access denied: CID not owned by namespace")
		return false
	}
	// Owning the CID is the namespace boundary; the selector is the one inside
	// it. A grant narrowed to `storage:avatars/*` owns everything its namespace
	// uploaded and may read only part of it.
	return h.authorizeCID(w, r, cid, namespace, gwauth.ActionRead)
}

// fetchStored retrieves an owned CID from IPFS, answering the client itself
// when it cannot.
//
// Content this node holds is served straight from disk. On a local miss the
// pinset is consulted before any network search: a CID nobody pins could only
// run out the fetch deadline, which used to cost the client 30 seconds and a
// TIMEOUT indistinguishable from a slow read. It is NOT_FOUND at once now.
func (h *Handlers) fetchStored(ctx context.Context, w http.ResponseWriter, cid, namespace string) (io.ReadCloser, bool) {
	// Cancelling fetchCtx on return is safe only because GetStored returns a
	// fully buffered reader; a streaming reader would be cut off here.
	fetchCtx, cancel := context.WithTimeout(ctx, storageFetchTimeout)
	defer cancel()
	ipfsAPIURL := h.config.IPFSAPIURL
	if ipfsAPIURL == "" {
		ipfsAPIURL = constants.LocalIPFSAPIURL()
	}
	reader, err := h.ipfsClient.GetStored(fetchCtx, cid, ipfsAPIURL)
	if err == nil {
		return reader, true
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		// The client went away; there is nobody to answer.
		h.logger.ComponentDebug(logging.ComponentGeneral, "download abandoned by the client", zap.String("cid", cid))
		return nil, false
	}

	switch {
	case errors.Is(err, ipfs.ErrNotInPinset):
		h.writeNotStored(ctx, w, cid, namespace)
	case errors.Is(err, ipfs.ErrPinsetUnavailable):
		h.logger.ComponentError(logging.ComponentGeneral, "failed to read the cluster pinset",
			zap.Error(err), zap.String("cid", cid))
		httputil.WriteRPCError(w, http.StatusServiceUnavailable, httputil.ErrCodeServiceUnavailable,
			"the storage cluster could not be reached to locate this content")
	case errors.Is(err, context.DeadlineExceeded):
		h.logger.ComponentError(logging.ComponentGeneral, "content fetch timed out",
			zap.Error(err), zap.String("cid", cid))
		httputil.WriteRPCError(w, http.StatusGatewayTimeout, httputil.ErrCodeTimeout,
			fmt.Sprintf("content %s could not be retrieved in time; retry later", cid))
	default:
		// The detail (node addresses, Kubo's own message) goes to the log only.
		h.logger.ComponentError(logging.ComponentGeneral, "failed to get content from IPFS",
			zap.Error(err), zap.String("cid", cid))
		httputil.WriteRPCError(w, http.StatusInternalServerError, httputil.ErrCodeInternal,
			fmt.Sprintf("failed to read content %s from the storage node", cid))
	}
	return nil, false
}

// writeNotStored answers NOT_FOUND for a CID the pinset does not hold. Right
// after its pin was requested that is a pin still propagating, and the answer
// is retryable; after the window it is final.
func (h *Handlers) writeNotStored(ctx context.Context, w http.ResponseWriter, cid, namespace string) {
	recent, err := h.pinRequestedWithin(ctx, cid, namespace, pinPropagationWindow)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to read the CID's pin-request time",
			zap.Error(err), zap.String("cid", cid))
		httputil.WriteRPCError(w, http.StatusInternalServerError, httputil.ErrCodeInternal, "failed to classify the missing content")
		return
	}
	if recent {
		httputil.WriteRPCError(w, http.StatusNotFound, httputil.ErrCodeNotFound,
			fmt.Sprintf("content %s was pinned moments ago and is not yet visible on this node; retry shortly", cid),
			httputil.WithRetryable())
		return
	}
	h.logger.ComponentDebug(logging.ComponentGeneral, "owned CID is not in the cluster pinset", zap.String("cid", cid))
	httputil.WriteRPCError(w, http.StatusNotFound, httputil.ErrCodeNotFound,
		fmt.Sprintf("content %s is not stored in the cluster (never pinned, or unpinned and reclaimed); it cannot be retrieved", cid))
}

// pinRequestedWithin reports whether namespace uploaded or re-pinned cid less
// than window ago. pin_requested_at is written by SQLite's datetime('now'),
// so the comparison is made there too, against the same clock and format. A
// row written by a gateway older than migration 058 has none; its upload time
// stands in.
func (h *Handlers) pinRequestedWithin(ctx context.Context, cid, namespace string, window time.Duration) (bool, error) {
	if h.db == nil {
		return false, nil
	}
	var rows []map[string]interface{}
	q := `SELECT COUNT(*) AS count FROM ipfs_content_ownership
	       WHERE cid = ? AND namespace = ?
	         AND COALESCE(pin_requested_at, uploaded_at) >= datetime('now', ?)`
	if err := h.db.Query(ctx, &rows, q, cid, namespace, fmt.Sprintf("-%d seconds", int(window.Seconds()))); err != nil {
		return false, fmt.Errorf("read pin-request time of %s in namespace %s: %w", cid, namespace, err)
	}
	return len(rows) > 0 && countFromRow(rows[0]["count"]) > 0, nil
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
