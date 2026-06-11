// Package ratelimit provides the HTTP handlers for tenant-self-service
// rate-limit configuration. Feature #69 — mirrors the push-config
// handler shape so the operational pattern stays uniform across
// per-namespace config endpoints.
package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/ratelimit"
	"go.uber.org/zap"
)

// Handlers mounts the three endpoints. Construct via NewHandlers and pass
// the same *ratelimit.Manager and ConfigStore the gateway is using —
// after PUT/DELETE the manager's cache is invalidated so the next
// request rebuilds with fresh values.
type Handlers struct {
	store   ratelimit.ConfigStore
	manager *ratelimit.Manager
	logger  *logging.ColoredLogger
}

func NewHandlers(store ratelimit.ConfigStore, manager *ratelimit.Manager, logger *logging.ColoredLogger) *Handlers {
	return &Handlers{store: store, manager: manager, logger: logger}
}

// PutRequest is the body of PUT /v1/namespace/rate-limit. Both fields
// are required; partial updates are not supported (this is a small flat
// config, no merge semantics to muddy).
type PutRequest struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	Burst             int `json:"burst"`
}

// GetResponse is the shape of GET /v1/namespace/rate-limit. Always
// returns the EFFECTIVE values (the override if present, else the
// gateway defaults), plus the operator-imposed maxima so the tenant
// knows the ceiling. `Source` distinguishes the two.
//
// `Scope` documents the bucket scope. As of v1 it is always
// "per-gateway", meaning the configured rate-per-minute applies to ONE
// gateway's bucket; in an N-gateway deployment the effective
// cluster-wide cap is N × the configured value. We surface this in
// every response so tenants don't get surprised by what looks like
// rate-limit overage when in fact they're hitting N gateways under one
// configured limit.
type GetResponse struct {
	Namespace            string `json:"namespace"`
	RequestsPerMinute    int    `json:"requests_per_minute"`
	Burst                int    `json:"burst"`
	Source               string `json:"source"`            // "override" | "default"
	Scope                string `json:"scope"`             // "per-gateway" — see doc
	MaxRequestsPerMinute int    `json:"max_requests_per_minute,omitempty"`
	MaxBurst             int    `json:"max_burst,omitempty"`
	UpdatedAt            int64  `json:"updated_at,omitempty"`
	UpdatedBy            string `json:"updated_by,omitempty"`
}

// scopePerGateway is the only Scope value we currently emit. A future
// shared-bucket implementation would change this — clients should treat
// it as opaque metadata and rely on the documented values.
const scopePerGateway = "per-gateway"

// MaxBodyBytes caps PUT body size. The body is two integers; 1 KiB
// is comically generous and safely rejects unbounded payloads.
const MaxBodyBytes = 1024

// GetConfigHandler — GET /v1/namespace/rate-limit. Always 200 when the
// store is available; reports effective values + their source.
func (h *Handlers) GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil || h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "rate-limit config not available on this gateway")
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

	cfg, err := h.store.Get(boundCtx(r), ns)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "rate-limit config GET failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load config")
		return
	}

	defs := h.manager.Defaults()
	resp := GetResponse{
		Namespace:            ns,
		Scope:                scopePerGateway,
		MaxRequestsPerMinute: defs.MaxRequestsPerMinute,
		MaxBurst:             defs.MaxBurst,
	}
	if cfg != nil {
		resp.RequestsPerMinute = cfg.RequestsPerMinute
		resp.Burst = cfg.Burst
		resp.Source = "override"
		resp.UpdatedAt = cfg.UpdatedAt
		resp.UpdatedBy = cfg.UpdatedBy
	} else {
		resp.RequestsPerMinute = defs.RequestsPerMinute
		resp.Burst = defs.Burst
		resp.Source = "default"
	}
	writeJSON(w, http.StatusOK, resp)
}

// PutConfigHandler — PUT /v1/namespace/rate-limit. Sets the namespace's
// override. Rejected if the requested values exceed the operator's
// MaxRequestsPerMinute / MaxBurst ceiling (a tenant CANNOT raise their
// own quota above the platform cap).
func (h *Handlers) PutConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil || h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "rate-limit config not available on this gateway")
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
	caller := resolveCallerUserID(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "user authentication required (JWT)")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	var body PutRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {requests_per_minute, burst}")
		return
	}
	if body.RequestsPerMinute <= 0 || body.Burst <= 0 {
		writeError(w, http.StatusBadRequest, "requests_per_minute and burst must be positive integers")
		return
	}

	// Operator ceiling check. The operator's Max* values are the absolute
	// maximums a tenant can request; setting them to 0 in the YAML means
	// "no cap, trust tenant input" (use only in trusted-tenant
	// deployments). Anything else: hard reject if exceeded.
	defs := h.manager.Defaults()
	if defs.MaxRequestsPerMinute > 0 && body.RequestsPerMinute > defs.MaxRequestsPerMinute {
		writeError(w, http.StatusBadRequest,
			"requests_per_minute exceeds operator-configured maximum")
		return
	}
	if defs.MaxBurst > 0 && body.Burst > defs.MaxBurst {
		writeError(w, http.StatusBadRequest, "burst exceeds operator-configured maximum")
		return
	}

	cfg := ratelimit.Config{
		Namespace:         ns,
		RequestsPerMinute: body.RequestsPerMinute,
		Burst:             body.Burst,
		UpdatedAt:         time.Now().Unix(),
		UpdatedBy:         caller,
	}
	if err := h.store.Upsert(boundCtx(r), cfg); err != nil {
		if errors.Is(err, ratelimit.ErrAboveOperatorCap) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.ComponentWarn(logging.ComponentGeneral, "rate-limit config PUT failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}
	// Drop the cached limiter so the next request rebuilds with new values.
	h.manager.Invalidate(ns)

	h.logger.ComponentInfo(logging.ComponentGeneral, "rate-limit config updated",
		zap.String("namespace", ns),
		zap.Int("rpm", cfg.RequestsPerMinute),
		zap.Int("burst", cfg.Burst),
		zap.String("by", caller))

	// Return the new effective config so the client sees what's in place.
	writeJSON(w, http.StatusOK, GetResponse{
		Namespace:            ns,
		RequestsPerMinute:    cfg.RequestsPerMinute,
		Burst:                cfg.Burst,
		Source:               "override",
		Scope:                scopePerGateway,
		UpdatedAt:            cfg.UpdatedAt,
		UpdatedBy:            cfg.UpdatedBy,
		MaxRequestsPerMinute: defs.MaxRequestsPerMinute,
		MaxBurst:             defs.MaxBurst,
	})
}

// DeleteConfigHandler — DELETE /v1/namespace/rate-limit. Removes the
// override; subsequent requests fall back to the gateway defaults.
// Idempotent: 200 even if no override existed.
func (h *Handlers) DeleteConfigHandler(w http.ResponseWriter, r *http.Request) {
	if h.store == nil || h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "rate-limit config not available on this gateway")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (use DELETE)")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	caller := resolveCallerUserID(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "user authentication required (JWT)")
		return
	}
	if err := h.store.Delete(boundCtx(r), ns); err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "rate-limit config DELETE failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete config")
		return
	}
	h.manager.Invalidate(ns)
	h.logger.ComponentInfo(logging.ComponentGeneral, "rate-limit config cleared",
		zap.String("namespace", ns), zap.String("by", caller))

	defs := h.manager.Defaults()
	writeJSON(w, http.StatusOK, GetResponse{
		Namespace:            ns,
		RequestsPerMinute:    defs.RequestsPerMinute,
		Burst:                defs.Burst,
		Source:               "default",
		Scope:                scopePerGateway,
		MaxRequestsPerMinute: defs.MaxRequestsPerMinute,
		MaxBurst:             defs.MaxBurst,
	})
}

// ---------- helpers (kept private to the package; mirror push handlers) ----------

func resolveNamespace(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func resolveCallerUserID(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			return claims.Sub
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func boundCtx(r *http.Request) context.Context { return r.Context() }
