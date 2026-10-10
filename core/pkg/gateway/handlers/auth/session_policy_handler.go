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

// GET /v1/namespace/session-policy   the namespace's device and sign-in policy
// PUT /v1/namespace/session-policy   {"device_policy": "optional"|"required"|"approval",
//                                     "sign_in": "members"|"open"}
//
// Both fields of a PUT are optional but at least one is sent, and the one left
// out keeps what it was. Both are validated before either is written.
//
// The route policy requires the namespace-write permission; this handler reads
// the namespace from the caller's credential, never from the request.

// SessionPolicyRequest sets a namespace's device policy, its sign-in policy, or
// both. A field left out is left as it is.
type SessionPolicyRequest struct {
	DevicePolicy *string `json:"device_policy"`
	SignIn       *string `json:"sign_in"`
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
		if body, ok := h.sessionPolicyBody(w, r, namespace); ok {
			writeJSON(w, http.StatusOK, body)
		}
	case http.MethodPut:
		h.setSessionPolicy(w, r, namespace)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET to read, PUT {\"device_policy\": ..., \"sign_in\": ...} to set")
	}
}

// sessionPolicyBody is the namespace's policy as recorded now. It answers the
// request itself when the registry cannot be read.
func (h *Handlers) sessionPolicyBody(w http.ResponseWriter, r *http.Request, namespace string) (map[string]any, bool) {
	device, err := h.authService.DevicePolicyOf(r.Context(), namespace)
	if err != nil {
		h.writeUnavailable(w, "read the session policy", err)
		return nil, false
	}
	signIn, err := h.authService.SignInPolicyOf(r.Context(), namespace)
	if err != nil {
		h.writeUnavailable(w, "read the sign-in policy", err)
		return nil, false
	}
	return map[string]any{"namespace": namespace, "device_policy": string(device), "sign_in": string(signIn)}, true
}

// parseSessionPolicyRequest reads and validates a PUT. A nil policy is a field
// the request did not send.
func parseSessionPolicyRequest(w http.ResponseWriter, r *http.Request) (*authsvc.DevicePolicy, *authsvc.SignInPolicy, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req SessionPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body: expected {\"device_policy\": \"optional|required|approval\", \"sign_in\": \"members|open\"}")
		return nil, nil, false
	}
	if req.DevicePolicy == nil && req.SignIn == nil {
		writeError(w, http.StatusBadRequest, "send device_policy (optional|required|approval), sign_in (members|open), or both")
		return nil, nil, false
	}
	var device *authsvc.DevicePolicy
	var signIn *authsvc.SignInPolicy
	if req.DevicePolicy != nil {
		p, err := authsvc.ParseDevicePolicy(*req.DevicePolicy)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, nil, false
		}
		device = &p
	}
	if req.SignIn != nil {
		p, err := authsvc.ParseSignInPolicy(*req.SignIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, nil, false
		}
		signIn = &p
	}
	return device, signIn, true
}

func (h *Handlers) setSessionPolicy(w http.ResponseWriter, r *http.Request, namespace string) {
	device, signIn, ok := parseSessionPolicyRequest(w, r)
	if !ok {
		return
	}
	actor := authsvc.ActorFromRequest(r)
	// The two fields are two writes. Opening sign-in goes last and closing it
	// first, so a request that fails between them leaves the namespace closed,
	// never open under a device policy the request meant to replace.
	opening := signIn != nil && *signIn == authsvc.SignInOpen
	if signIn != nil && !opening && !h.applySignInPolicy(w, r, namespace, *signIn, actor) {
		return
	}
	revokedKeys, sweepErr := 0, error(nil)
	if device != nil {
		var ok bool
		if revokedKeys, sweepErr, ok = h.applyDevicePolicy(w, r, namespace, *device, actor); !ok {
			return
		}
	}
	if opening && !h.applySignInPolicy(w, r, namespace, *signIn, actor) {
		return
	}
	h.writeSessionPolicyResult(w, r, namespace, device != nil, revokedKeys, sweepErr)
}

// applySignInPolicy records a sign-in policy and audits it. It answers the
// request itself when the write fails.
func (h *Handlers) applySignInPolicy(w http.ResponseWriter, r *http.Request, namespace string, policy authsvc.SignInPolicy, actor string) bool {
	if err := h.authService.SetSignInPolicy(r.Context(), namespace, policy, actor); err != nil {
		h.writeUnavailable(w, "record the sign-in policy", err)
		return false
	}
	h.recordSignInPolicy(r, namespace, actor, policy)
	return true
}

// applyDevicePolicy records a device policy and audits it. A sweep of the end
// users' sign-in keys that stopped partway is returned, not answered: the
// policy is set, and the rest of the request goes on.
func (h *Handlers) applyDevicePolicy(w http.ResponseWriter, r *http.Request, namespace string, policy authsvc.DevicePolicy, actor string) (int, error, bool) {
	revokedKeys, err := h.authService.SetDevicePolicy(r.Context(), namespace, policy, actor)
	sweepIncomplete := errors.Is(err, authsvc.ErrSignInKeySweepIncomplete)
	if err != nil && !sweepIncomplete {
		h.writeUnavailable(w, "record the session policy", err)
		return 0, nil, false
	}
	h.recordSessionPolicy(r, namespace, actor, policy, revokedKeys, sweepIncomplete)
	if sweepIncomplete {
		return revokedKeys, err, true
	}
	return revokedKeys, nil, true
}

// writeSessionPolicyResult answers a PUT with the policy as recorded now.
func (h *Handlers) writeSessionPolicyResult(w http.ResponseWriter, r *http.Request, namespace string, deviceSet bool, revokedKeys int, sweepErr error) {
	body, ok := h.sessionPolicyBody(w, r, namespace)
	if !ok {
		return
	}
	if deviceSet {
		body["revoked_sign_in_keys"] = revokedKeys
	}
	if sweepErr == nil {
		writeJSON(w, http.StatusOK, body)
		return
	}
	if h.logger != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "the session policy is set but its sign-in key sweep stopped",
			zap.String("namespace", namespace), zap.Error(sweepErr))
	}
	body["code"] = ErrCodePolicySweepIncomplete
	body["error"] = "the policy is set, but " + strconv.Itoa(revokedKeys) +
		" sign-in keys were revoked before the sweep stopped; repeat the request to revoke the rest"
	writeJSON(w, http.StatusServiceUnavailable, body)
}

// recordSignInPolicy audits a sign-in policy that was set. Opening a namespace
// to wallets nobody invited is the change worth a durable record.
func (h *Handlers) recordSignInPolicy(r *http.Request, namespace, actor string, policy authsvc.SignInPolicy) {
	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     actor,
		Action:    authsvc.AuditSignInPolicySet,
		Result:    authsvc.AuditSuccess,
		Resource:  string(policy),
	})
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
