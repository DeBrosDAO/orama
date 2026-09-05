package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// The device authorization grant (RFC 8628), which is how a machine with no
// wallet on it signs in.
//
// Three endpoints, and the split between them is the whole point:
//
//   - POST /v1/auth/device        the machine that wants a session asks
//   - POST /v1/auth/device/approve  a human with a wallet approves the code
//   - POST /v1/auth/device/token    the waiting machine collects its tokens
//
// The first and third are open, because the caller has no credential yet — that
// is what it is asking for. The device code it is given is the credential for
// the third, and it collects a session exactly once.
//
// Approving costs a wallet signature over this gateway's own challenge: the
// same message /v1/auth/verify takes, verified by the same code. There is no
// second way to prove who you are, so there is no second thing to get wrong.
//
// This gateway does not return `verification_uri`. The RFC's field names a page
// a human opens, and there is no such page yet; `orama auth approve <code>` is
// the client for the approval endpoint today. A field naming a page that does
// not exist would send people somewhere that 404s.

// DeviceAuthorizationRequest asks for a pending login.
type DeviceAuthorizationRequest struct {
	// Namespace the waiting machine wants a session in. Optional: omitted
	// means whichever namespace the approver signs in to.
	Namespace string `json:"namespace"`
}

// DeviceTokenRequest collects an approved login.
type DeviceTokenRequest struct {
	DeviceCode string `json:"device_code"`
}

// DeviceApprovalRequest approves or refuses a pending login. See VerifyRequest
// for why the signed message is the whole credential.
type DeviceApprovalRequest struct {
	UserCode  string `json:"user_code"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
	// Deny refuses the login instead of approving it, so the machine waiting
	// on it stops rather than polling out its ten minutes.
	Deny bool `json:"deny"`
}

// DeviceAuthorizationHandler starts a pending login.
//
// POST /v1/auth/device
func (h *Handlers) DeviceAuthorizationHandler(w http.ResponseWriter, r *http.Request) {
	if !h.deviceReady(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req DeviceAuthorizationRequest
	// An empty body is a request for a session in whichever namespace the
	// approver signs in to, which is the common case from a fresh machine.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body: expected {\"namespace\": \"...\"} or no body at all")
			return
		}
	}

	pending, err := h.authService.StartDeviceAuthorization(r.Context(), req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: req.Namespace,
		Action:    authsvc.AuditDeviceLoginStarted,
		Result:    authsvc.AuditSuccess,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"device_code": pending.DeviceCode,
		"user_code":   pending.UserCode,
		"expires_in":  int(time.Until(pending.ExpiresAt).Seconds()),
		"interval":    pending.Interval,
	})
}

// DeviceApprovalHandler approves or refuses a pending login.
//
// POST /v1/auth/device/approve
func (h *Handlers) DeviceApprovalHandler(w http.ResponseWriter, r *http.Request) {
	if !h.deviceReady(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req DeviceApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.UserCode) == "" {
		writeError(w, http.StatusBadRequest, "user_code is required: it is the code the waiting machine printed")
		return
	}

	// The signature comes first. Refusing a login is as consequential as
	// approving one — it is how you would stop somebody else logging in — so
	// both cost the same proof.
	in, ok := h.signIn(w, r, req.Message, req.Signature)
	if !ok {
		return
	}

	ctx := r.Context()
	if err := h.authService.RequireNamespaceOwner(ctx, in.Wallet, in.Namespace); err != nil {
		writeCredentialError(w, in.Namespace, err)
		return
	}

	action := authsvc.AuditDeviceLoginApproved
	err := h.authService.ApproveDeviceAuthorization(ctx, req.UserCode, in.Wallet, in.Namespace)
	if req.Deny {
		action = authsvc.AuditDeviceLoginDenied
		err = h.authService.DenyDeviceAuthorization(ctx, req.UserCode)
	}
	if err != nil {
		h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
			Namespace: in.Namespace,
			Actor:     in.Wallet,
			Action:    action,
			Result:    authsvc.AuditFailure,
			Metadata:  map[string]string{"reason": err.Error()},
		})
		writeDeviceError(w, err)
		return
	}

	h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
		Namespace: in.Namespace,
		Actor:     in.Wallet,
		Action:    action,
		Result:    authsvc.AuditSuccess,
	})

	status := "approved"
	if req.Deny {
		status = "denied"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    status,
		"namespace": in.Namespace,
		"subject":   in.Wallet,
	})
}

// DeviceTokenHandler is the waiting machine's poll.
//
// POST /v1/auth/device/token
func (h *Handlers) DeviceTokenHandler(w http.ResponseWriter, r *http.Request) {
	if !h.deviceReady(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req DeviceTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body: expected {\"device_code\": \"...\"}")
		return
	}

	ctx := r.Context()
	claimed, err := h.authService.ClaimDeviceAuthorization(ctx, req.DeviceCode)
	if err != nil {
		writeDeviceError(w, err)
		return
	}

	// A session, not a key. The whole reason this flow exists is that the
	// documented way onto a server was a permanent key in an environment
	// variable; handing one back here would rebuild that by another route.
	token, refresh, expUnix, err := h.authService.IssueTokens(ctx, claimed.Subject, claimed.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
		Namespace: claimed.Namespace,
		Actor:     claimed.Subject,
		Action:    authsvc.AuditDeviceLoginClaimed,
		Result:    authsvc.AuditSuccess,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    int(expUnix - time.Now().Unix()),
		"refresh_token": refresh,
		"subject":       claimed.Subject,
		"namespace":     claimed.Namespace,
	})
}

// deviceReady refuses anything this endpoint cannot honour, before it reads a
// body.
func (h *Handlers) deviceReady(w http.ResponseWriter, r *http.Request) bool {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return false
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (POST)")
		return false
	}
	return true
}

// writeDeviceError turns a device-flow outcome into the response RFC 8628 §3.5
// names.
//
// The RFC's four are 400s carrying an `error` the client switches on, not
// failures of the request: "nobody has approved it yet" is the expected answer
// to almost every poll.
func writeDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authsvc.ErrDeviceAuthorizationPending),
		errors.Is(err, authsvc.ErrDeviceSlowDown),
		errors.Is(err, authsvc.ErrDeviceCodeExpired),
		errors.Is(err, authsvc.ErrDeviceAccessDenied),
		errors.Is(err, authsvc.ErrDeviceCodeUnknown):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":             err.Error(),
			"error_description": deviceErrorDescriptions[err.Error()],
		})
	case errors.Is(err, authsvc.ErrDeviceAlreadyApproved):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":             "already_approved",
			"error_description": "somebody already approved this code; the machine waiting on it has its session",
		})
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// deviceErrorDescriptions says, in the response, what the client should do —
// so a person reading a failed poll with curl is not left with one word.
var deviceErrorDescriptions = map[string]string{
	"authorization_pending": "nobody has approved this code yet; keep polling",
	"slow_down":             "polled faster than the interval this login was issued with",
	"expired_token":         "this login was not approved in time; ask for a new code",
	"access_denied":         "the approver refused this login",
	"invalid_grant":         "this code names no pending login, or its session was already collected",
}
