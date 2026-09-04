package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

// DeviceRevokeRequest is the body of POST /v1/auth/device/revoke.
type DeviceRevokeRequest struct {
	// Subject is the account whose device is being revoked — the JWT `sub`
	// that device authenticates as.
	Subject string `json:"subject"`
	// DeviceFingerprint is the value the gateway stamped as the `device_fp`
	// claim.
	DeviceFingerprint string `json:"device_fingerprint"`
}

// DeviceRevokeResponse reports what the revocation actually did.
type DeviceRevokeResponse struct {
	Subject           string `json:"subject"`
	DeviceFingerprint string `json:"device_fingerprint"`
	// RefreshTokensRevoked is how many live refresh chains were cut. Returned
	// because it is the difference between "marked revoked" and "actually
	// stopped": a device with a live chain keeps minting device-stamped access
	// tokens until those rows are revoked.
	RefreshTokensRevoked int64 `json:"refresh_tokens_revoked"`
}

// DeviceRevokeHandler revokes a device binding and every refresh token that
// device obtained.
//
// POST /v1/auth/device/revoke
//
// This is the seam that lets a namespace enforce "a revoked device stops being
// served" WITHOUT the gateway knowing anything about rosters (bugboard
// feat-384). The app decides a device is no longer current — that is its
// policy, from its own signed roster — and tells the gateway, rather than the
// gateway asking the app on every request. Asking would put a fail-open WASM
// invoke with a 2s timeout inside the authorization path.
//
// After this returns, the device is out within one access-token TTL (15
// minutes): its chain is dead so it cannot refresh, and its current access
// token expires on its own.
//
// Authorization: namespace-scoped admin credentials, the same gate the other
// namespace-administrative endpoints use. An end-user JWT must not be able to
// revoke devices, or a compromised device could revoke the legitimate ones.
func (h *Handlers) DeviceRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespace := namespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "namespace could not be resolved from credentials")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req DeviceRevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	subject := strings.TrimSpace(req.Subject)
	fingerprint := strings.TrimSpace(req.DeviceFingerprint)
	if subject == "" || fingerprint == "" {
		writeError(w, http.StatusBadRequest, "subject and device_fingerprint are required")
		return
	}

	revoked, err := h.authService.RevokeDevice(r.Context(), namespace, subject, fingerprint)
	if err != nil {
		// 404 when nothing matched: the caller asked to stop a device that is
		// not there, and must not read that as "stopped".
		if errors.Is(err, authsvc.ErrDeviceNotFound) {
			writeError(w, http.StatusNotFound, "no device binding matched that subject and fingerprint")
			return
		}
		// Everything else is infrastructure. The wrapped error carries SQL and
		// namespace-resolution detail, so log it and return fixed text.
		if h.logger != nil {
			h.logger.Warn("device revocation failed",
				zap.String("namespace", namespace), zap.Error(err))
		}
		writeError(w, http.StatusInternalServerError, "device revocation failed")
		return
	}

	writeJSON(w, http.StatusOK, DeviceRevokeResponse{
		Subject:              subject,
		DeviceFingerprint:    fingerprint,
		RefreshTokensRevoked: revoked,
	})
}

// namespaceFromRequest resolves the namespace the caller is authorized for.
//
// Read from the context the auth middleware populated, never from the request
// body: a body-supplied namespace would let any authenticated caller revoke
// devices in someone else's namespace.
func namespaceFromRequest(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
