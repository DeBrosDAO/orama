package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	if rest, ok := strings.CutSuffix(sub, "/rotate"); ok {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed (POST /{id}/rotate)")
			return
		}
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "which key: POST /v1/namespace/keys/{id}/rotate")
			return
		}
		g.rotateNamespaceKey(w, r, id)
		return
	}

	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (DELETE /{id}, POST /{id}/rotate)")
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
		// ExpiresInDays is how long the key lives. Omitted means the default;
		// a key that lives forever is not on offer, which is the point of the
		// column.
		ExpiresInDays int `json:"expires_in_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {scope, label, expires_in_days}")
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
	lifetime, err := keyLifetime(body.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rawKey, id, err := g.authService.IssueScopedKey(r.Context(), ns, stored, auth.KeyOptions{
		Label:    strings.TrimSpace(body.Label),
		Lifetime: lifetime,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	g.logger.ComponentInfo("gateway", "scoped api key issued",
		zap.String("namespace", ns), zap.String("scopes", stored), zap.String("label", body.Label))
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"api_key":    rawKey,
		"scopes":     stored,
		"namespace":  ns,
		"label":      body.Label,
		"expires_at": time.Now().Add(lifetime).UTC().Format(time.RFC3339),
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

// keyLifetime turns a requested number of days into a duration.
//
// Zero means the default. There is no way to ask for a key that never expires:
// that is the thing migration 051 exists to end, and an escape hatch for it
// would be used by the first person in a hurry and then by everybody.
func keyLifetime(days int) (time.Duration, error) {
	if days == 0 {
		return auth.KeyLifetime, nil
	}
	if days < 1 {
		return 0, fmt.Errorf("expires_in_days is at least 1; omit it for the default of %d",
			int(auth.KeyLifetime.Hours()/24))
	}
	lifetime := time.Duration(days) * 24 * time.Hour
	if lifetime > auth.MaxKeyLifetime {
		return 0, fmt.Errorf("expires_in_days is at most %d; past that an expiry does nothing a revocation would not do better",
			int(auth.MaxKeyLifetime.Hours()/24))
	}
	return lifetime, nil
}

// rotateNamespaceKey mints a successor to a key and keeps the original valid
// for an overlap.
//
// Rotating by minting a new key and revoking the old one in the same breath is
// an outage: whatever is deployed with the old key stops working the moment the
// new one exists, and there is no window in which to roll the new one out. The
// overlap is that window — both keys work, `keys list` shows the succession,
// and the old one expires at the end of it.
func (g *Gateway) rotateNamespaceKey(w http.ResponseWriter, r *http.Request, id int64) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body struct {
		OverlapDays   int `json:"overlap_days"`
		ExpiresInDays int `json:"expires_in_days"`
	}
	// An empty body is the common case: rotate with the default overlap.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {overlap_days, expires_in_days}")
		return
	}

	lifetime, err := keyLifetime(body.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	overlap, err := rotationOverlap(body.OverlapDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rawKey, newID, err := g.authService.RotateKey(r.Context(), ns, id, auth.RotateOptions{
		Lifetime: lifetime,
		Overlap:  overlap,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	g.logger.ComponentInfo("gateway", "api key rotated",
		zap.String("namespace", ns), zap.Int64("from", id), zap.Int64("to", newID))
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":               newID,
		"api_key":          rawKey,
		"rotated_from":     id,
		"namespace":        ns,
		"expires_at":       time.Now().Add(lifetime).UTC().Format(time.RFC3339),
		"previous_expires": time.Now().Add(overlap).UTC().Format(time.RFC3339),
	})
}

// rotationOverlap turns a requested overlap into a duration.
func rotationOverlap(days int) (time.Duration, error) {
	if days == 0 {
		return auth.DefaultRotationOverlap, nil
	}
	if days < 0 {
		return 0, fmt.Errorf("overlap_days is at least 0; 0 ends the old key immediately")
	}
	overlap := time.Duration(days) * 24 * time.Hour
	if overlap > auth.MaxRotationOverlap {
		return 0, fmt.Errorf("overlap_days is at most %d; a longer overlap is two live keys, not a rotation",
			int(auth.MaxRotationOverlap.Hours()/24))
	}
	return overlap, nil
}
