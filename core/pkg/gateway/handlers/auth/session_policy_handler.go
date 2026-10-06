package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// GET /v1/namespace/session-policy   the namespace's device policy
// PUT /v1/namespace/session-policy   {"device_policy": "optional"|"required"|"approval"}
//
// The route policy requires the namespace-write permission; this handler reads
// the namespace from the caller's credential, never from the request.

// SessionPolicyRequest sets a namespace's device policy.
type SessionPolicyRequest struct {
	DevicePolicy string `json:"device_policy"`
}

// SessionPolicyHandler reads or sets the calling namespace's session policy.
func (h *Handlers) SessionPolicyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	namespace := h.callerNamespace(r)
	if authsvc.IsLobbyNamespace(namespace) {
		writeError(w, http.StatusForbidden, "the lobby has no session policy; set one on a namespace you own")
		return
	}

	switch r.Method {
	case http.MethodGet:
		policy, err := h.authService.DevicePolicyOf(r.Context(), namespace)
		if err != nil {
			h.writeUnavailable(w, "read the session policy", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"namespace": namespace, "device_policy": string(policy)})
	case http.MethodPut:
		h.setSessionPolicy(w, r, namespace)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET to read, PUT {\"device_policy\": ...} to set")
	}
}

func (h *Handlers) setSessionPolicy(w http.ResponseWriter, r *http.Request, namespace string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req SessionPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body: expected {\"device_policy\": \"optional|required|approval\"}")
		return
	}
	policy, err := authsvc.ParseDevicePolicy(req.DevicePolicy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := authsvc.ActorFromRequest(r)
	revokedKeys, err := h.authService.SetDevicePolicy(r.Context(), namespace, policy, actor)
	sweepIncomplete := errors.Is(err, authsvc.ErrSignInKeySweepIncomplete)
	if err != nil && !sweepIncomplete {
		h.writeUnavailable(w, "record the session policy", err)
		return
	}
	h.recordSessionPolicy(r, namespace, actor, policy, revokedKeys, sweepIncomplete)
	body := map[string]any{
		"namespace":            namespace,
		"device_policy":        string(policy),
		"revoked_sign_in_keys": revokedKeys,
	}
	if !sweepIncomplete {
		writeJSON(w, http.StatusOK, body)
		return
	}
	if h.logger != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "the session policy is set but its sign-in key sweep stopped",
			zap.String("namespace", namespace), zap.Error(err))
	}
	body["code"] = ErrCodePolicySweepIncomplete
	body["error"] = "the policy is set, but " + strconv.Itoa(revokedKeys) +
		" sign-in keys were revoked before the sweep stopped; repeat the request to revoke the rest"
	writeJSON(w, http.StatusServiceUnavailable, body)
}

// recordSessionPolicy audits a policy that was set, whether or not every
// existing sign-in key was revoked with it.
func (h *Handlers) recordSessionPolicy(r *http.Request, namespace, actor string, policy authsvc.DevicePolicy, revokedKeys int, sweepIncomplete bool) {
	metadata := map[string]string{"revoked_sign_in_keys": strconv.Itoa(revokedKeys)}
	if sweepIncomplete {
		metadata["sign_in_key_sweep"] = "incomplete"
	}
	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     actor,
		Action:    authsvc.AuditSessionPolicySet,
		Result:    authsvc.AuditSuccess,
		Resource:  string(policy),
		Metadata:  metadata,
	})
}
