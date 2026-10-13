package webrtc

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// maxConfigBody bounds a settings request: one boolean.
const maxConfigBody = 1024

// configBody is the namespace's WebRTC policy, as GET returns it and PUT sets it.
type configBody struct {
	// RequireAdmission is set when the namespace admits only users its
	// functions admitted (webrtc_admit). Off by default.
	RequireAdmission *bool `json:"require_admission"`
}

// ConfigHandler serves GET and PUT /v1/webrtc/config: the namespace's WebRTC
// policy. It is the namespace's own settings, so only a credential that may
// change them reaches it (route_policy.go).
func (h *WebRTCHandlers) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	if h.admissions == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway has no WebRTC admission store")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body configBody
		r.Body = http.MaxBytesReader(w, r.Body, maxConfigBody)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequireAdmission == nil {
			writeError(w, http.StatusBadRequest, `invalid body: expected {"require_admission": true|false}`)
			return
		}
		if err := h.admissions.SetRequireAdmission(ctx, ns, *body.RequireAdmission); err != nil {
			h.configFailure(w, ns, err)
			return
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	require, err := h.admissions.RequireAdmission(ctx, ns)
	if err != nil {
		h.configFailure(w, ns, err)
		return
	}
	writeJSON(w, http.StatusOK, configBody{RequireAdmission: &require})
}

func (h *WebRTCHandlers) configFailure(w http.ResponseWriter, ns string, err error) {
	h.logger.ComponentError(logging.ComponentGeneral, "WebRTC config request failed", zap.String("namespace", ns), zap.Error(err))
	writeError(w, http.StatusServiceUnavailable, "the namespace's WebRTC settings could not be read or saved; retry")
}

// contextWithTimeout derives a bounded context from a request.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
