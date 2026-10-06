package auth

import (
	"errors"
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// The refusals of device-bound sessions. Each says what went wrong with the
// device, because the client's next move differs: sign again, ask another
// device, or give up on this key for good.
const (
	// ErrCodeDeviceRequired: the namespace requires sessions bound to a
	// device, and the sign-in bound none.
	ErrCodeDeviceRequired = "DEVICE_REQUIRED"
	// ErrCodeDeviceKeyInvalid: the device key is not a P-256 or Ed25519
	// public JWK, or the message and the key name different devices.
	ErrCodeDeviceKeyInvalid = "DEVICE_KEY_INVALID"
	// ErrCodeDeviceSignatureInvalid: the device's signature over the sign-in
	// message does not verify.
	ErrCodeDeviceSignatureInvalid = "DEVICE_SIGNATURE_INVALID"
	// ErrCodeDeviceProofRequired: a device-bound credential was presented
	// without the device's proof.
	ErrCodeDeviceProofRequired = "DEVICE_PROOF_REQUIRED"
	// ErrCodeDeviceProofInvalid: the proof is stale, reused, or not signed by
	// the device.
	ErrCodeDeviceProofInvalid = "DEVICE_PROOF_INVALID"
	// ErrCodeDeviceRevoked: the device was revoked. Its key is done.
	ErrCodeDeviceRevoked = "DEVICE_REVOKED"
	// ErrCodeDevicePending: the device waits for another device's approval.
	ErrCodeDevicePending = "DEVICE_PENDING"
	// ErrCodeDeviceNotFound: the account has no such device.
	ErrCodeDeviceNotFound = "DEVICE_NOT_FOUND"
	// ErrCodeDeviceKeyTaken: the key is enrolled for another account.
	ErrCodeDeviceKeyTaken = "DEVICE_KEY_TAKEN"
	// ErrCodePolicySweepIncomplete: the session policy is set, but revoking
	// the end users' existing sign-in keys stopped partway. Repeat the request.
	ErrCodePolicySweepIncomplete = "POLICY_SWEEP_INCOMPLETE"
)

type deviceRefusal struct {
	status int
	code   string
	hint   string
}

// deviceRefusals is checked in order; the first error it matches decides.
var deviceRefusals = []struct {
	err     error
	refusal deviceRefusal
}{
	{authsvc.ErrDeviceRequired, deviceRefusal{http.StatusForbidden, ErrCodeDeviceRequired,
		"generate a device key, ask for a challenge naming it (device_id), and send device_key and device_signature"}},
	{authsvc.ErrDeviceKeyInvalid, deviceRefusal{http.StatusBadRequest, ErrCodeDeviceKeyInvalid,
		"send the public JWK of a P-256 or Ed25519 key, and a challenge whose device_id is that key's thumbprint"}},
	{authsvc.ErrDeviceSignatureInvalid, deviceRefusal{http.StatusUnauthorized, ErrCodeDeviceSignatureInvalid,
		"sign the sign-in message, byte for byte, with the device key as well as the wallet"}},
	{authsvc.ErrDeviceProofRequired, deviceRefusal{http.StatusUnauthorized, ErrCodeDeviceProofRequired,
		"sign a device proof for this request with the device key and send it as device_proof"}},
	{authsvc.ErrDeviceProofInvalid, deviceRefusal{http.StatusUnauthorized, ErrCodeDeviceProofInvalid,
		"make a fresh proof: a new id, the current time, signed by the device key over this request's credential"}},
	{authsvc.ErrDeviceRevoked, deviceRefusal{http.StatusForbidden, ErrCodeDeviceRevoked,
		"this device key can never sign in again; generate a new key and link it from a device that is still signed in"}},
	{authsvc.ErrDevicePending, deviceRefusal{http.StatusForbidden, ErrCodeDevicePending,
		"approve this device from one of the account's signed-in devices"}},
	{authsvc.ErrDeviceNotFound, deviceRefusal{http.StatusNotFound, ErrCodeDeviceNotFound,
		"list this account's devices with GET /v1/auth/devices"}},
	{authsvc.ErrDeviceBelongsToAnother, deviceRefusal{http.StatusForbidden, ErrCodeDeviceKeyTaken,
		"a device key belongs to one account; generate a new key for this one"}},
}

// writeUnavailable answers 503 without the database's own words, which name
// tables and hosts, and puts them in the log for the operator instead.
func (h *Handlers) writeUnavailable(w http.ResponseWriter, what string, err error) {
	if h.logger != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "could not "+what, zap.Error(err))
	}
	writeError(w, http.StatusServiceUnavailable, "could not "+what+"; retry")
}

// writeDeviceRefusal answers a device refusal and reports whether err was one.
// Anything else is left for the caller to answer.
func writeDeviceRefusal(w http.ResponseWriter, err error) bool {
	for _, candidate := range deviceRefusals {
		if !errors.Is(err, candidate.err) {
			continue
		}
		if candidate.refusal.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gateway", charset="UTF-8"`)
		}
		writeJSON(w, candidate.refusal.status, map[string]any{
			"error": err.Error(),
			"code":  candidate.refusal.code,
			"hint":  candidate.refusal.hint,
		})
		return true
	}
	return false
}
