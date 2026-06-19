package push

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// validProviders is the allowlist for the `provider` field on RegisterDevice.
// Keep in sync with what the dispatcher actually has registered at startup.
//
// "apns_voip" (bugboard #408) is the PushKit/CallKit variant of "apns" —
// same underlying credentials, distinct dispatcher entry. Tenants
// register a second PushDevice row per iPhone with the PushKit
// voipPushToken to enable CallKit-triggering incoming-call pushes,
// keyed by a distinct device_id (typically `<base>:voip`) so the
// `device_id` PK doesn't collide with the alert-path row.
var validProviders = map[string]struct{}{
	"ntfy":      {},
	"expo":      {},
	"apns":      {},
	"apns_voip": {},
}

// MaxTokenBytes caps the device-token length to prevent abuse.
// Real ntfy topic paths and Expo tokens are well under this.
const MaxTokenBytes = 512

// RegisterDeviceHandler handles POST /v1/push/devices.
//
// The caller must be authenticated; their JWT subject (Sub) is used as the
// user_id. API-key callers are allowed only if the body explicitly carries
// a user_id — currently rejected to keep the surface small.
func (h *Handlers) RegisterDeviceHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "push: device store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	userID := resolveCallerUserID(r)
	if userID == "" {
		// We require a JWT-authenticated user to bind the device to.
		// API-key-only callers can't register devices on behalf of users.
		writeError(w, http.StatusUnauthorized, "user authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body RegisterDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	body.Provider = strings.TrimSpace(body.Provider)
	body.Token = strings.TrimSpace(body.Token)

	if body.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id required")
		return
	}
	if _, ok := validProviders[body.Provider]; !ok {
		writeError(w, http.StatusBadRequest, "unknown provider: "+body.Provider)
		return
	}
	if body.Token == "" {
		writeError(w, http.StatusBadRequest, "token required")
		return
	}
	if len(body.Token) > MaxTokenBytes {
		writeError(w, http.StatusBadRequest, "token too long")
		return
	}

	now := time.Now().Unix()
	dev := push.PushDevice{
		Namespace: ns,
		UserID:    userID,
		DeviceID:  body.DeviceID,
		Provider:  body.Provider,
		Token:     body.Token,
		Platform:  body.Platform,
		AppVer:    body.AppVersion,
		LastSeen:  now,
	}
	if err := h.store.Upsert(boundCtx(r), dev); err != nil {
		h.logger.ComponentWarn("push", "device upsert failed",
			zap.String("namespace", ns),
			zap.String("user_id", userID),
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	writeJSON(w, http.StatusOK, RegisterDeviceResponse{Status: "ok"})
}

// ListDevicesHandler handles GET /v1/push/devices.
//
// Returns the caller's own devices; tokens are NEVER included in the
// response. Other namespaces / other users are inaccessible.
func (h *Handlers) ListDevicesHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "push: device store not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	userID := resolveCallerUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user authentication required")
		return
	}

	devs, err := h.store.ListForUser(boundCtx(r), ns, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	views := make([]PushDeviceView, len(devs))
	for i, d := range devs {
		views[i] = PushDeviceView{
			ID:         d.ID,
			DeviceID:   d.DeviceID,
			Provider:   d.Provider,
			Platform:   d.Platform,
			AppVersion: d.AppVer,
			CreatedAt:  d.CreatedAt,
			UpdatedAt:  d.UpdatedAt,
			LastSeen:   d.LastSeen,
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"devices": views})
}

// DeleteDeviceHandler handles DELETE /v1/push/devices/{id}.
//
// `{id}` is the database row ID returned at registration / by ListDevices.
// Only devices belonging to the caller (matched by namespace + user_id +
// the device ID lookup) can be deleted.
func (h *Handlers) DeleteDeviceHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "push: device store not configured")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	userID := resolveCallerUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user authentication required")
		return
	}

	id := extractIDFromPath(r.URL.Path, "/v1/push/devices/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "device id required in path")
		return
	}

	// Authorization check: confirm the device belongs to the caller.
	devs, err := h.store.ListForUser(boundCtx(r), ns, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	owns := false
	for _, d := range devs {
		if d.ID == id {
			owns = true
			break
		}
	}
	if !owns {
		// 404, not 403 — don't leak whether the ID exists in another scope.
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.store.Delete(boundCtx(r), ns, id); err != nil {
		h.logger.ComponentWarn("push", "device delete failed",
			zap.String("namespace", ns),
			zap.String("device_row_id", id),
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SendHandler handles POST /v1/push/send.
//
// SECURITY: this endpoint sends arbitrary push messages to any user_id
// in the caller's namespace. It MUST be gated to a small set of trusted
// callers — typically only the namespace's own serverless functions
// (which can send via the WASM `push_send` hostfunc directly without
// going through HTTP) and the namespace operator.
//
// The current implementation accepts any JWT-authenticated caller within
// the namespace. **Add an explicit allow-list or admin-scope check before
// exposing this in production.** The WASM hostfunc bypasses this issue
// because trigger registration already gates which functions exist.
func (h *Handlers) SendHandler(w http.ResponseWriter, r *http.Request) {
	// Either the per-namespace manager (preferred) or the legacy single
	// dispatcher must be present. Both nil = push not configured at all.
	if h.manager == nil && h.dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "push: dispatcher not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	if resolveCallerUserID(r) == "" {
		writeError(w, http.StatusUnauthorized, "user authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // generous for Data payloads
	var body SendRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.UserID = strings.TrimSpace(body.UserID)
	if body.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id required")
		return
	}

	msg := push.PushMessage{
		Title:     body.Title,
		Body:      body.Body,
		Channel:   body.Channel,
		Priority:  pickPriority(body.Priority),
		Badge:     body.Badge,
		Sound:     body.Sound,
		Data:      body.Data,
		MessageID: body.MessageID,
	}
	// Prefer the per-namespace Manager when present so per-namespace
	// config (set via PUT /v1/push/config) takes effect. Fall back to the
	// legacy single dispatcher only when no Manager is wired.
	var sendErr error
	if h.manager != nil {
		sendErr = h.manager.SendToUser(boundCtx(r), ns, body.UserID, msg)
		if errors.Is(sendErr, push.ErrPushNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, sendErr.Error())
			return
		}
	} else {
		sendErr = h.dispatcher.SendToUser(boundCtx(r), ns, body.UserID, msg)
	}
	if sendErr != nil {
		// Treat as non-fatal: some devices may have failed but others may
		// have succeeded. Surface as 502 to signal partial trouble; logs
		// have the per-device detail.
		h.logger.ComponentWarn("push", "send to user partially failed",
			zap.String("namespace", ns),
			zap.String("user_id", body.UserID),
			zap.Error(sendErr))
		writeError(w, http.StatusBadGateway, "one or more devices failed")
		return
	}
	writeJSON(w, http.StatusOK, SendResponse{Status: "ok"})
}

// extractIDFromPath returns the trailing path segment after `prefix`, or
// empty string if the path doesn't match. Used because the gateway uses
// the standard `net/http` mux which doesn't extract path params.
func extractIDFromPath(urlPath, prefix string) string {
	if !strings.HasPrefix(urlPath, prefix) {
		return ""
	}
	rest := urlPath[len(prefix):]
	// Drop any query string (shouldn't normally appear in path here).
	if i := strings.IndexAny(rest, "?#/"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
