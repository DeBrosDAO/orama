package storage

import (
	"fmt"
	"net/http"
	"strings"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// writePinNotFound is the one answer for a CID with no pin status to show,
// whether nobody pinned it or the caller's namespace does not reference it.
func writePinNotFound(w http.ResponseWriter, cid string) {
	httputil.WriteError(w, http.StatusNotFound, fmt.Sprintf("pin not found: %s", cid))
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

	namespace := h.getNamespaceFromContext(r.Context())
	if namespace == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "namespace required")
		return
	}
	if !h.authorizeStatus(w, r, path, namespace) {
		return
	}
	h.writePinStatus(w, r, path)
}

// authorizeStatus reports whether namespace may see the pin status of cid,
// and writes the refusal when it may not.
//
// The pin status carries the object's name and the peers holding it, and
// the cluster keeps one pin per CID for every namespace. A namespace is told
// about a CID only if it references it; for any other CID the answer is
// exactly the one for a CID nobody pinned, so the endpoint is no oracle for
// what other tenants store.
func (h *Handlers) authorizeStatus(w http.ResponseWriter, r *http.Request, cid, namespace string) bool {
	ctx := r.Context()
	hasAccess, err := h.checkCIDOwnership(ctx, cid, namespace)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to check CID ownership",
			zap.Error(err), zap.String("cid", cid), zap.String("namespace", namespace))
		writeStoreError(w, "failed to verify access", err)
		return false
	}
	if !hasAccess {
		h.logger.ComponentDebug(logging.ComponentGeneral, "status asked for a CID the namespace does not reference",
			zap.String("cid", cid), zap.String("namespace", namespace))
		writePinNotFound(w, cid)
		return false
	}
	return h.authorizeCID(w, r, cid, namespace, gwauth.ActionRead)
}

// writePinStatus answers with the cluster's pin status of cid.
func (h *Handlers) writePinStatus(w http.ResponseWriter, r *http.Request, cid string) {
	status, err := h.ipfsClient.PinStatus(r.Context(), cid)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to get pin status",
			zap.Error(err), zap.String("cid", cid))

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "invalid") {
			writePinNotFound(w, cid)
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get status: %v", err))
		}
		return
	}

	httputil.WriteJSON(w, http.StatusOK, StorageStatusResponse{
		Cid:               status.Cid,
		Name:              status.Name,
		Status:            status.Status,
		ReplicationMin:    status.ReplicationMin,
		ReplicationMax:    status.ReplicationMax,
		ReplicationFactor: status.ReplicationFactor,
		Peers:             status.Peers,
		Error:             status.Error,
	})
}
