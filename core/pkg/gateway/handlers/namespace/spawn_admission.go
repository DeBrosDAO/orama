package namespace

import (
	"context"
	"errors"
	"net/http"

	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"go.uber.org/zap"
)

// SpawnAdmitter decides whether this node may start a unit of a namespace, and
// holds the namespace's lock for the caller until its release is called
// (ClusterManager.AdmitSpawn).
type SpawnAdmitter interface {
	AdmitSpawn(ctx context.Context, namespace, clusterID string) (release func(), err error)
}

// SetSpawnAdmitter wires the admission every action that starts a unit goes
// through.
func (h *SpawnHandler) SetSpawnAdmitter(a SpawnAdmitter) { h.admitter = a }

// startsUnits lists the actions that start or restart a unit of the namespace.
// The stop-* and teardown-* actions are not here: stopping what a delete is
// stopping anyway is harmless, and a teardown takes the lock itself.
var startsUnits = map[string]bool{
	"spawn-rqlite":    true,
	"spawn-olric":     true,
	"spawn-gateway":   true,
	"restart-gateway": true,
	"spawn-sfu":       true,
}

// admit takes the namespace's lock and checks, under it, that the namespace is
// not being deleted. It reports false after answering the request itself. The
// release it returns is called when the action is done.
//
// The request's cluster_id (absent from a provisioner on the previous release,
// which is then checked only for deletion) must be the namespace's current
// cluster. A node on the previous release ignores the field. A provisioner whose
// namespace was deleted while it was provisioning, or deleted and re-created, is
// told 409, which fails its provisioning and has it undo what it started; a node on
// the previous release did not check, so an old provisioner talking to this node
// gets an error it already handles for any failed spawn, and this node talking
// to an old provisioner is unchanged.
func (h *SpawnHandler) admit(w http.ResponseWriter, r *http.Request, req SpawnRequest) (release func(), ok bool) {
	if h.admitter == nil {
		writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: "this node has no spawn admission, so it cannot tell whether the namespace is being deleted"})
		return nil, false
	}
	release, err := h.admitter.AdmitSpawn(r.Context(), req.Namespace, req.ClusterID)
	switch {
	case err == nil:
		return release, true
	case errors.Is(err, namespacepkg.ErrNamespaceBeingDeleted), errors.Is(err, namespacepkg.ErrClusterMismatch):
		h.logger.Warn("Refused a spawn: the namespace is being deleted or is another cluster's now",
			zap.String("action", req.Action), zap.String("namespace", req.Namespace), zap.Error(err))
		writeSpawnResponse(w, http.StatusConflict, SpawnResponse{Error: err.Error()})
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		h.logger.Error("Refused a spawn: the namespace's lock was not free in time",
			zap.String("action", req.Action), zap.String("namespace", req.Namespace), zap.Error(err))
		writeSpawnResponse(w, http.StatusServiceUnavailable, SpawnResponse{Error: err.Error()})
	default:
		h.logger.Error("Refused a spawn: admission failed",
			zap.String("action", req.Action), zap.String("namespace", req.Namespace), zap.Error(err))
		writeSpawnResponse(w, http.StatusInternalServerError, SpawnResponse{Error: err.Error()})
	}
	return nil, false
}
