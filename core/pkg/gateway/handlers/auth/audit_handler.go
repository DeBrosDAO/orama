package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A record nobody can read is not a record. The events are written to a
// Raft-replicated table that, until this endpoint, only a raw-database query
// could reach — which needs the admin grant on a route that serves the whole
// database, so "who minted this key" was not a question a namespace owner
// could ask without being handed something far larger.
//
// GET /v1/audit returns this namespace's own events, most recent first.

const (
	auditPageDefault = 50
	auditPageMax     = 200
)

// AuditEntry is one row as a caller sees it.
type AuditEntry struct {
	Action    string `json:"action"`
	Actor     string `json:"actor,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Result    string `json:"result"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Metadata  string `json:"metadata,omitempty"`
	CreatedAt string `json:"created_at"`
}

// AuditHandler serves GET /v1/audit.
//
// The namespace is taken from the caller's own credential, never from the
// query string: reading another namespace's audit trail would be a neat way to
// learn who its owners are and when they sign in.
func (h *Handlers) AuditHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	namespace := namespaceFromContext(r)
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "the namespace could not be resolved from this credential")
		return
	}

	limit := auditPageDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive whole number")
			return
		}
		limit = parsed
	}
	if limit > auditPageMax {
		limit = auditPageMax
	}

	// An optional filter, checked against the actions this gateway records so
	// a typo comes back as a refusal rather than an empty page.
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	if action != "" && !knownAuditAction(action) {
		writeError(w, http.StatusBadRequest, "unknown action: "+action)
		return
	}

	entries, err := h.readAuditEvents(r.Context(), namespace, action, limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the audit trail could not be read: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": namespace,
		"events":    entries,
		"count":     len(entries),
	})
}

func knownAuditAction(action string) bool {
	for _, known := range authsvc.AuditActions {
		if known == action {
			return true
		}
	}
	return false
}

// namespaceFromContext reads the namespace the caller's credential resolved to.
func namespaceFromContext(r *http.Request) string {
	if v := r.Context().Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// readAuditEvents returns this namespace's events, most recent first.
func (h *Handlers) readAuditEvents(ctx context.Context, namespace, action string, limit int) ([]AuditEntry, error) {
	db := h.auditDB()
	if db == nil {
		return nil, errNoAuditDatabase
	}

	query := `SELECT action, actor, resource, result, ip, user_agent, metadata, created_at
	          FROM audit_events WHERE namespace = ?`
	args := []interface{}{namespace}
	if action != "" {
		query += " AND action = ?"
		args = append(args, action)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	res, err := db.Query(h.internalCtx(ctx), query, args...)
	if err != nil {
		return nil, err
	}

	entries := make([]AuditEntry, 0)
	if res == nil {
		return entries, nil
	}
	for _, raw := range res.Rows {
		row, ok := raw.([]interface{})
		if !ok || len(row) < 8 {
			continue
		}
		entries = append(entries, AuditEntry{
			Action:    stringCell(row[0]),
			Actor:     stringCell(row[1]),
			Resource:  stringCell(row[2]),
			Result:    stringCell(row[3]),
			IP:        stringCell(row[4]),
			UserAgent: stringCell(row[5]),
			Metadata:  stringCell(row[6]),
			CreatedAt: stringCell(row[7]),
		})
	}
	return entries, nil
}

// auditDB is the database the events were written to: the same handle the auth
// service writes through.
func (h *Handlers) auditDB() DatabaseClient {
	if h.netClient == nil {
		return nil
	}
	return h.netClient.Database()
}

func (h *Handlers) internalCtx(ctx context.Context) context.Context {
	if h.internalAuthFn != nil {
		return h.internalAuthFn(ctx)
	}
	return ctx
}

func stringCell(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

var errNoAuditDatabase = errString("this gateway has no database to read the audit trail from")

type errString string

func (e errString) Error() string { return string(e) }
