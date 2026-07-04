package push

// config_handler.go — tenant-self-service push provider configuration.
//
// Endpoints (mounted under /v1/push/config; namespace-ownership middleware applies):
//
//	GET    /v1/push/config   → current config (secrets redacted: only "has_X" booleans)
//	PUT    /v1/push/config   → set/update fields; sensitive credentials encrypted at rest
//	DELETE /v1/push/config   → clear the namespace's row (push reverts to gateway YAML defaults)
//
// Bug #220 follow-up. Eliminates the "tenant must file an ops ticket"
// workflow: once this lands, AnChat (and every future tenant) self-serves
// their push provider config via authenticated HTTP, no operator
// involvement.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// configManager is the subset of *push.Manager the config handlers need —
// kept narrow for testability.
type configManager interface {
	IsConfigured(ctx contextLike, namespace string) bool
	Invalidate(namespace string)
}

// contextLike avoids importing context everywhere — the handler is
// already in package serverless which has request contexts.
type contextLike = interface {
	Done() <-chan struct{}
}

// PutConfigRequest is the body of PUT /v1/push/config.
//
// Field semantics:
//   - Unset fields (zero value) leave the existing value alone.
//   - Empty-string fields explicitly clear the value.
//   - To clear the entire row use DELETE — that's clearer than empty PUT.
type PutConfigRequest struct {
	NtfyBaseURL     *string `json:"ntfy_base_url,omitempty"`
	NtfyAuthToken   *string `json:"ntfy_auth_token,omitempty"`
	ExpoAccessToken *string `json:"expo_access_token,omitempty"`
}

// MaxConfigBodyBytes caps the PUT body size. Push tokens are typically
// well under 1 KB but we leave headroom.
const MaxConfigBodyBytes = 16 * 1024

// pushConfigManager is the concrete dependency the Handlers struct holds
// — a *push.Manager. We extract it via a small interface for tests.
type pushConfigManager interface {
	IsConfigured(ctx interface{ Done() <-chan struct{} }, namespace string) bool
	Invalidate(namespace string)
}

// GetConfigHandler — GET /v1/push/config. Returns the namespace's current
// push provider config with sensitive fields REDACTED to boolean flags.
//
// Always 200 + canonical envelope-free body when the request is well-formed
// (clients can rely on the shape: an absent provider just shows
// `has_ntfy_auth_token: false`). 503 only when the config store itself
// isn't available on this gateway (e.g. push subsystem disabled).
func (h *Handlers) GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.configStore == nil {
		writeError(w, http.StatusServiceUnavailable,
			"push config store not available on this gateway")
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

	cfg, err := h.configStore.Get(boundCtx(r), ns)
	if err != nil && !errors.Is(err, push.ErrConfigNotFound) {
		h.logger.ComponentWarn("push", "config GET failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	// Not found → return empty redacted config. Clients distinguish
	// "configured" via the boolean fields.
	if cfg == nil {
		writeJSON(w, http.StatusOK, push.RedactedConfig{Namespace: ns})
		return
	}
	writeJSON(w, http.StatusOK, cfg.Redacted())
}

// PutConfigHandler — PUT /v1/push/config. Updates the namespace's push
// provider config. Field-level semantics: nil JSON values leave the
// existing field untouched; explicit empty-strings clear it.
//
// On success returns the redacted config (same shape as GET) so clients
// can confirm what's now in place without echoing back the credentials.
//
// Invalidates the manager's cached dispatcher for this namespace so the
// next push send rebuilds with the fresh config.
func (h *Handlers) PutConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.configStore == nil {
		writeError(w, http.StatusServiceUnavailable,
			"push config store not available on this gateway")
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (use PUT)")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	caller := resolveAdminCaller(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxConfigBodyBytes)
	var body PutConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON")
		return
	}

	// Reject a base URL that targets an internal/reserved host — a tenant must
	// not be able to turn the gateway's push sender into an SSRF proxy (cloud
	// metadata, WireGuard mesh, loopback). This is the config-SET path, so the
	// DNS-resolving check is fine here; the hot send path never runs it.
	if body.NtfyBaseURL != nil && *body.NtfyBaseURL != "" {
		if err := push.CheckBaseURLResolvable(r.Context(), *body.NtfyBaseURL); err != nil {
			writeError(w, http.StatusBadRequest, "ntfy_base_url rejected: "+err.Error())
			return
		}
	}

	// Read existing for merge — PUT semantics are field-level, not
	// whole-document replace.
	existing, err := h.configStore.Get(boundCtx(r), ns)
	if err != nil && !errors.Is(err, push.ErrConfigNotFound) {
		h.logger.ComponentWarn("push", "config GET-before-PUT failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	cfg := push.Config{Namespace: ns, UpdatedAt: time.Now().Unix(), UpdatedBy: caller}
	if existing != nil {
		cfg.NtfyBaseURL = existing.NtfyBaseURL
		cfg.NtfyAuthToken = existing.NtfyAuthToken
		cfg.ExpoAccessToken = existing.ExpoAccessToken
	}
	if body.NtfyBaseURL != nil {
		cfg.NtfyBaseURL = *body.NtfyBaseURL
	}
	if body.NtfyAuthToken != nil {
		cfg.NtfyAuthToken = *body.NtfyAuthToken
	}
	if body.ExpoAccessToken != nil {
		cfg.ExpoAccessToken = *body.ExpoAccessToken
	}

	if err := h.configStore.Upsert(boundCtx(r), cfg); err != nil {
		h.logger.ComponentWarn("push", "config PUT failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}
	if h.manager != nil {
		h.manager.Invalidate(ns)
	}

	h.logger.ComponentInfo("push", "config updated",
		zap.String("namespace", ns),
		zap.String("updated_by", caller),
		zap.Bool("has_ntfy_url", cfg.NtfyBaseURL != ""),
		zap.Bool("has_ntfy_auth_token", cfg.NtfyAuthToken != ""),
		zap.Bool("has_expo_access_token", cfg.ExpoAccessToken != ""),
	)
	writeJSON(w, http.StatusOK, cfg.Redacted())
}

// DeleteConfigHandler — DELETE /v1/push/config. Clears the namespace's
// row entirely; push reverts to gateway YAML defaults (or 503 "not
// configured" if no defaults).
func (h *Handlers) DeleteConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.configStore == nil {
		writeError(w, http.StatusServiceUnavailable,
			"push config store not available on this gateway")
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
	caller := resolveAdminCaller(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := h.configStore.Delete(boundCtx(r), ns); err != nil {
		h.logger.ComponentWarn("push", "config DELETE failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete config")
		return
	}
	if h.manager != nil {
		h.manager.Invalidate(ns)
	}
	h.logger.ComponentInfo("push", "config cleared",
		zap.String("namespace", ns),
		zap.String("cleared_by", caller),
	)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
