package gateway

// push_routes.go provides the always-registered push HTTP entrypoints.
// When the gateway has no push provider configured (no NtfyBaseURL,
// no ExpoAccessToken, etc. — and therefore no g.pushHandlers), these
// methods return a canonical 503 envelope instead of letting the route
// fall through to the default 404 handler.
//
// Bug #220: tenants saw `404 Page Not Found` on /v1/push/devices and
// couldn't tell whether the path was wrong or push wasn't enabled.
// 503 + a clear message points operators directly at the config knob.

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// pushNotConfiguredMessage is the human-readable 503 body. Same text
// reused by every push entrypoint so operators see consistent guidance.
const pushNotConfiguredMessage = "push notifications are not configured on this namespace gateway. " +
	"Set `ntfy_base_url` or `expo_access_token` in the gateway config and restart, " +
	"then call this endpoint again. See core/docs/SERVERLESS.md for details."

// pushDevicesHandler dispatches GET (list) / POST (register) on
// /v1/push/devices. Returns 503 when push isn't configured.
func (g *Gateway) pushDevicesHandler(w http.ResponseWriter, r *http.Request) {
	if g.pushHandlers == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable, pushNotConfiguredMessage)
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.pushHandlers.ListDevicesHandler(w, r)
	case http.MethodPost:
		g.pushHandlers.RegisterDeviceHandler(w, r)
	default:
		httputil.WriteRPCError(w, http.StatusMethodNotAllowed,
			httputil.ErrCodeValidationFailed, "method not allowed: use GET to list or POST to register")
	}
}

// pushDevicesByIDHandler handles DELETE /v1/push/devices/{id}. Returns
// 503 when push isn't configured.
func (g *Gateway) pushDevicesByIDHandler(w http.ResponseWriter, r *http.Request) {
	if g.pushHandlers == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable, pushNotConfiguredMessage)
		return
	}
	g.pushHandlers.DeleteDeviceHandler(w, r)
}

// pushSendHandler handles POST /v1/push/send. Returns 503 when push
// isn't configured.
func (g *Gateway) pushSendHandler(w http.ResponseWriter, r *http.Request) {
	if g.pushHandlers == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable, pushNotConfiguredMessage)
		return
	}
	g.pushHandlers.SendHandler(w, r)
}

// pushConfigHandler dispatches GET / PUT / DELETE on /v1/push/config — the
// tenant-self-service entrypoint for per-namespace push provider config
// (bug #220 follow-up). When push is fully disabled returns 503 with the
// same actionable message as the device endpoints.
func (g *Gateway) pushConfigHandler(w http.ResponseWriter, r *http.Request) {
	if g.pushHandlers == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable, pushNotConfiguredMessage)
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.pushHandlers.GetConfigHandler(w, r)
	case http.MethodPut, http.MethodPost:
		g.pushHandlers.PutConfigHandler(w, r)
	case http.MethodDelete:
		g.pushHandlers.DeleteConfigHandler(w, r)
	default:
		httputil.WriteRPCError(w, http.StatusMethodNotAllowed,
			httputil.ErrCodeValidationFailed,
			"method not allowed: use GET to read, PUT to update, or DELETE to clear")
	}
}
