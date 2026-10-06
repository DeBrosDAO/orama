package push

// credentials_handler.go — tenant-self-service per-provider push
// credentials. Feature #72.
//
// Endpoints (mounted under /v1/namespace/push-credentials/{provider}):
//
//	GET    /v1/namespace/push-credentials              → summary: which providers are configured
//	GET    /v1/namespace/push-credentials/{provider}   → provider-specific redacted view
//	PUT    /v1/namespace/push-credentials/{provider}   → validate + store (any JSON schema, owned by provider)
//	DELETE /v1/namespace/push-credentials/{provider}   → clear
//
// The handler itself is GENERIC: it never reads the credential JSON
// schema. Validation + redaction are delegated to the provider's
// Validator (registered at gateway startup). Adding a new provider —
// FCM, SMS, anything — requires zero changes to this file.
//
// Auth model: same as /v1/push/config (the existing PutConfigHandler).
// The caller must be JWT-authenticated; their namespace is resolved by
// the upstream middleware. API-key-only callers are rejected because
// credential changes are operator-level mutations.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push/credentials"

	"go.uber.org/zap"
)

// MaxCredentialsBodyBytes caps the PUT body size. p8 keys + Apple Team
// ID + Key ID + Bundle ID + JSON overhead fit comfortably under 16 KB.
// FCM service-account JSON tops out around 2 KB. 32 KB is generous and
// safely rejects absurd payloads.
const MaxCredentialsBodyBytes = 32 * 1024

// pathPrefixCredentials is the URL prefix this handler dispatches under.
// The trailing segment (if present) is the provider name; an absent
// segment selects the summary view.
const pathPrefixCredentials = "/v1/namespace/push-credentials"

// SetCredentialsManager wires the per-provider credential manager into
// the handlers. Called from the gateway dependency wiring; nil-safe
// (the handler returns 503 when the manager is absent, same shape as
// the other "subsystem not configured" 503s).
func (h *Handlers) SetCredentialsManager(m *credentials.Manager) {
	h.credentialsManager = m
}

// invalidatePushDispatcher is called after a successful PUT/DELETE on
// /v1/namespace/push-credentials/{provider} so the push.Manager
// rebuilds the namespace's dispatcher with the new credentials. This
// MUST be called in addition to credentialsManager.Invalidate —
// dropping the credential-cache entry alone isn't enough; the push
// dispatcher already holds an APNs/ntfy provider constructed from the
// old creds, and it stays in the dispatcher cache until the next TTL
// rebuild.
//
// nil-safe: if push.Manager isn't wired (e.g. cluster secret missing),
// this is a no-op.
func (h *Handlers) invalidatePushDispatcher(namespace string) {
	if h.manager != nil {
		h.manager.Invalidate(namespace)
	}
}

// CredentialsSummary is the GET (no provider) response shape.
//
// `Configured` is the list of provider names that have a stored
// credential row. `Supported` is the list of providers this gateway
// can accept PUTs for (i.e. has a registered Validator). Their
// intersection is "what's effective right now"; `Supported` minus
// `Configured` is "what the tenant could enable next".
type CredentialsSummary struct {
	Namespace  string   `json:"namespace"`
	Configured []string `json:"configured"`
	Supported  []string `json:"supported"`
}

// CredentialsSummaryHandler — GET /v1/namespace/push-credentials.
// Returns the list of providers that have a credential row for the
// namespace, plus the list of providers this gateway supports.
func (h *Handlers) CredentialsSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if h.credentialsManager == nil {
		writeError(w, http.StatusServiceUnavailable,
			"push credentials not available on this gateway")
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
	configured, err := h.credentialsManager.Store().ListProviders(boundCtx(r), ns)
	if err != nil {
		h.logger.ComponentWarn("push", "credentials summary failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list configured providers")
		return
	}
	// Stable shape: never return `null` for the array fields.
	if configured == nil {
		configured = []string{}
	}
	supported := credentials.RegisteredProviders()
	if supported == nil {
		supported = []string{}
	}
	writeJSON(w, http.StatusOK, CredentialsSummary{
		Namespace:  ns,
		Configured: configured,
		Supported:  supported,
	})
}

// CredentialsByProviderHandler — GET/PUT/DELETE on
// /v1/namespace/push-credentials/{provider}.
//
// Dispatches by method. `{provider}` is extracted from the URL path;
// unknown providers return 400 (clearer than 404 — they ARE valid
// resource shapes, just not enabled on this gateway).
func (h *Handlers) CredentialsByProviderHandler(w http.ResponseWriter, r *http.Request) {
	if h.credentialsManager == nil {
		writeError(w, http.StatusServiceUnavailable,
			"push credentials not available on this gateway")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	provider := extractProvider(r.URL.Path)
	if provider == "" {
		writeError(w, http.StatusBadRequest,
			"provider required in path: /v1/namespace/push-credentials/{provider}")
		return
	}
	v, ok := credentials.LookupValidator(provider)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"unsupported provider: "+provider+
				" (supported: "+strings.Join(credentials.RegisteredProviders(), ", ")+")")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCredentials(w, r, ns, provider, v)
	case http.MethodPut, http.MethodPost:
		h.putCredentials(w, r, ns, provider, v)
	case http.MethodDelete:
		h.deleteCredentials(w, r, ns, provider)
	default:
		writeError(w, http.StatusMethodNotAllowed,
			"method not allowed: use GET to read, PUT to update, or DELETE to clear")
	}
}

// getCredentials returns the redacted view of the provider's credential
// for the namespace, or an empty body with `configured: false` if no
// credential is stored.
func (h *Handlers) getCredentials(
	w http.ResponseWriter, r *http.Request,
	ns, provider string, v credentials.Validator,
) {
	cred, err := h.credentialsManager.Get(boundCtx(r), ns, provider)
	if err != nil {
		h.logger.ComponentWarn("push", "credentials GET failed",
			zap.String("namespace", ns),
			zap.String("provider", provider), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load credential")
		return
	}
	if cred == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"namespace":  ns,
			"provider":   provider,
			"configured": false,
		})
		return
	}
	redacted, err := v.Redact(cred.JSON)
	if err != nil {
		h.logger.ComponentWarn("push", "credentials redact failed",
			zap.String("namespace", ns),
			zap.String("provider", provider), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to redact credential")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespace":  ns,
		"provider":   provider,
		"configured": true,
		"updated_at": cred.UpdatedAt,
		"updated_by": cred.UpdatedBy,
		"redacted":   redacted,
	})
}

// putCredentials validates the body against the provider's schema and
// stores the encrypted blob. Body is the provider-specific JSON
// document — the handler does not inspect its fields.
func (h *Handlers) putCredentials(
	w http.ResponseWriter, r *http.Request,
	ns, provider string, v credentials.Validator,
) {
	caller := resolveAdminCaller(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxCredentialsBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "empty body; expected JSON")
		return
	}
	// Lightweight syntactic check before handing to the Validator. Cheap
	// and lets us return a clearer "not JSON" message than a custom
	// per-provider parse error.
	if !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "body is not valid JSON")
		return
	}
	if err := v.Validate(raw); err != nil {
		writeError(w, http.StatusBadRequest, "credential validation failed: "+err.Error())
		return
	}

	cred := credentials.Credential{
		Namespace: ns,
		Provider:  provider,
		JSON:      raw,
		UpdatedAt: time.Now().Unix(),
		UpdatedBy: caller,
	}
	if err := h.credentialsManager.Store().Upsert(boundCtx(r), cred); err != nil {
		h.logger.ComponentWarn("push", "credentials PUT failed",
			zap.String("namespace", ns),
			zap.String("provider", provider), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to save credential")
		return
	}
	// Drop BOTH caches: the credential-store cache (so the next Get
	// reads the new blob) AND the push.Manager dispatcher cache (so
	// the next SendToUser rebuilds with a provider constructed from
	// the new credentials). Missing the second invalidate was a real
	// bug — APNs key rotations would never take effect on the rotating
	// gateway until LRU eviction. Other gateways still rely on the
	// push.Manager's TTL for propagation.
	h.credentialsManager.Invalidate(ns, provider)
	h.invalidatePushDispatcher(ns)
	h.logger.ComponentInfo("push", "credentials updated",
		zap.String("namespace", ns),
		zap.String("provider", provider),
		zap.String("updated_by", caller))

	redacted, redactErr := v.Redact(raw)
	if redactErr != nil {
		// Storage succeeded but the response can't safely include the
		// redacted view. Log it and return success with a minimal body
		// — never leak the raw credential as a fallback.
		h.logger.ComponentWarn("push", "credentials redact failed post-PUT",
			zap.String("namespace", ns),
			zap.String("provider", provider), zap.Error(redactErr))
		redacted = map[string]interface{}{"redact_error": "see server logs"}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespace":  ns,
		"provider":   provider,
		"configured": true,
		"updated_at": cred.UpdatedAt,
		"updated_by": cred.UpdatedBy,
		"redacted":   redacted,
	})
}

// deleteCredentials clears the provider's credential row for the
// namespace. Idempotent — returns 200 even if no row existed, so
// callers can DELETE freely.
func (h *Handlers) deleteCredentials(
	w http.ResponseWriter, r *http.Request,
	ns, provider string,
) {
	caller := resolveAdminCaller(r)
	if caller == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := h.credentialsManager.Store().Delete(boundCtx(r), ns, provider); err != nil {
		h.logger.ComponentWarn("push", "credentials DELETE failed",
			zap.String("namespace", ns),
			zap.String("provider", provider), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	// Same dual-cache invalidation as PUT — see putCredentials.
	h.credentialsManager.Invalidate(ns, provider)
	h.invalidatePushDispatcher(ns)
	h.logger.ComponentInfo("push", "credentials cleared",
		zap.String("namespace", ns),
		zap.String("provider", provider),
		zap.String("cleared_by", caller))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespace":  ns,
		"provider":   provider,
		"configured": false,
	})
}

// extractProvider returns the provider segment after pathPrefixCredentials,
// or empty if absent.
func extractProvider(urlPath string) string {
	if !strings.HasPrefix(urlPath, pathPrefixCredentials) {
		return ""
	}
	rest := strings.TrimPrefix(urlPath, pathPrefixCredentials)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return ""
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
