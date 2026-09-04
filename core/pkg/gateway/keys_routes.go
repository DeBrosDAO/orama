package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// Scoped API-key management (bugboard #148).
//
// These endpoints are admin-scoped (see requiredScope) AND namespace-scoped:
// the namespace is taken from the caller's own credential (never a request
// parameter), so an admin key can only manage keys within its own namespace.
// The raw key material is returned exactly once, on create.

// namespaceKeysHandler dispatches GET (list) / POST (create) on
// /v1/namespace/keys.
func (g *Gateway) namespaceKeysHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.listNamespaceKeys(w, r)
	case http.MethodPost:
		g.createNamespaceKey(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (GET to list, POST to create)")
	}
}

// namespaceKeysByIDHandler dispatches DELETE /v1/namespace/keys/{id} (revoke
// one) and POST /v1/namespace/keys/revoke-legacy (sweep-revoke every legacy,
// NULL-scope key — the cutover step).
func (g *Gateway) namespaceKeysByIDHandler(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/namespace/keys/"), "/")

	if sub == "revoke-legacy" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed (POST)")
			return
		}
		g.revokeLegacyKeys(w, r)
		return
	}

	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (DELETE /{id})")
		return
	}
	id, err := strconv.ParseInt(sub, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid key id in path")
		return
	}
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}
	if err := g.authService.RevokeKey(r.Context(), ns, id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "revoked", "id": id})
}

func (g *Gateway) createNamespaceKey(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Scope  string `json:"scope"`
		Scopes string `json:"scopes"`
		Label  string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {scope, label}")
		return
	}
	requested := strings.TrimSpace(body.Scope)
	if requested == "" {
		requested = strings.TrimSpace(body.Scopes)
	}
	stored, err := auth.NormalizeGrants(requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rawKey, id, err := g.authService.IssueScopedKey(r.Context(), ns, stored, strings.TrimSpace(body.Label))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g.logger.ComponentInfo("gateway", "scoped api key issued",
		zap.String("namespace", ns), zap.String("scopes", stored), zap.String("label", body.Label))
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        id,
		"api_key":   rawKey,
		"scopes":    stored,
		"namespace": ns,
		"label":     body.Label,
	})
}

func (g *Gateway) listNamespaceKeys(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}
	keys, err := g.authService.ListKeys(r.Context(), ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "keys": keys})
}

func (g *Gateway) revokeLegacyKeys(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}
	n, err := g.authService.RevokeAllLegacy(r.Context(), ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g.logger.ComponentInfo("gateway", "legacy api keys revoked (cutover)",
		zap.String("namespace", ns), zap.Int("revoked", n))
	writeJSON(w, http.StatusOK, map[string]any{"status": "revoked-legacy", "revoked": n, "namespace": ns})
}

// keysNamespace returns the namespace bound to the request's credential.
func keysNamespace(r *http.Request) string {
	if v := r.Context().Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
