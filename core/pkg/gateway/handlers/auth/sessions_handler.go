package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// Seeing and ending the sessions signed in as you.
//
// GET  /v1/auth/sessions      the caller's own live sessions
// DELETE /v1/auth/sessions/{id}  end one of them
//
// The subject comes from the caller's own token, never from the request, so
// there is no version of these that reaches somebody else's sessions.

// SessionsHandler lists the caller's live sessions.
func (h *Handlers) SessionsHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (GET)")
		return
	}

	subject, namespace, ok := h.sessionOwner(w, r)
	if !ok {
		return
	}

	sessions, err := h.authService.ListSessions(r.Context(), namespace, subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, map[string]any{
			"id":         session.ID,
			"subject":    session.Subject,
			"audience":   session.Audience,
			"created_at": formatSessionTime(session.CreatedAt),
			"expires_at": formatSessionTime(session.ExpiresAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": namespace,
		"subject":   subject,
		"sessions":  out,
	})
}

// SessionByIDHandler ends one session.
func (h *Handlers) SessionByIDHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (DELETE /v1/auth/sessions/{id})")
		return
	}

	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/auth/sessions/"), "/")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "which session: DELETE /v1/auth/sessions/{id}, as listed by GET /v1/auth/sessions")
		return
	}

	subject, namespace, ok := h.sessionOwner(w, r)
	if !ok {
		return
	}

	if err := h.authService.EndSession(r.Context(), namespace, subject, id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     subject,
		Action:    authsvc.AuditLoggedOut,
		Result:    authsvc.AuditSuccess,
		Metadata:  map[string]string{"session": strconv.FormatInt(id, 10)},
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ended",
		"id":      id,
		"warning": "an access token already minted from this session keeps working until it expires, at most " + authsvc.AccessTokenLifetime.String(),
	})
}

// sessionOwner is whose sessions these are.
//
// A JWT names its subject. An API key does not name a wallet — it is a
// credential in its own right — so it cannot be used to list or end the
// sessions of the wallet that happened to mint it.
func (h *Handlers) sessionOwner(w http.ResponseWriter, r *http.Request) (subject, namespace string, ok bool) {
	ctx := r.Context()
	namespace = h.defaultNS
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, cast := v.(string); cast && s != "" {
			namespace = s
		}
	}

	claims, cast := ctx.Value(CtxKeyJWT).(*authsvc.JWTClaims)
	if !cast || claims == nil || strings.TrimSpace(claims.Sub) == "" {
		writeError(w, http.StatusForbidden,
			"sessions belong to a signed-in wallet: call this with a token from 'orama auth login', not with an API key")
		return "", "", false
	}
	return claims.Sub, namespace, true
}

// formatSessionTime renders a timestamp, or nothing when the column was empty.
func formatSessionTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UTC().Format(time.RFC3339)
}
