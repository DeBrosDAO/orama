package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	sessionIDRegex = regexp.MustCompile(`^[a-f0-9]{64}$`)
	namespaceRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]?$`)
)

// PhantomSessionHandler creates a new Phantom auth session.
// The CLI calls this to get a session ID and auth URL, then displays a QR code.
//
// POST /v1/auth/phantom/session
// Request body: { "namespace": "myns" }
// Response: { "session_id", "auth_url", "expires_at" }
func (h *Handlers) PhantomSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Namespace string `json:"namespace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = h.defaultNS
		if namespace == "" {
			namespace = "default"
		}
	}
	if !namespaceRegex.MatchString(namespace) {
		writeError(w, http.StatusBadRequest, "invalid namespace format")
		return
	}

	// Generate session ID
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate session ID")
		return
	}
	sessionID := hex.EncodeToString(buf)
	expiresAt := time.Now().Add(5 * time.Minute)

	// Store session in DB
	ctx := r.Context()
	internalCtx := h.internalAuthFn(ctx)
	db := h.netClient.Database()

	_, err := db.Query(internalCtx,
		"INSERT INTO phantom_auth_sessions(id, namespace, status, expires_at) VALUES (?, ?, 'pending', ?)",
		sessionID, namespace, expiresAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// PhantomSessionStatusHandler returns the current status of a Phantom auth session.
// The CLI polls this endpoint every 2 seconds waiting for completion.
//
// GET /v1/auth/phantom/session/{id}
// Response: { "session_id", "status", "wallet", "api_key", "namespace" }
func (h *Handlers) PhantomSessionStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract session ID from URL path: /v1/auth/phantom/session/{id}
	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/auth/phantom/session/")
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !sessionIDRegex.MatchString(sessionID) {
		writeError(w, http.StatusBadRequest, "invalid session_id format")
		return
	}

	ctx := r.Context()
	internalCtx := h.internalAuthFn(ctx)
	db := h.netClient.Database()

	res, err := db.Query(internalCtx,
		"SELECT id, namespace, status, wallet, api_key, error_message, expires_at FROM phantom_auth_sessions WHERE id = ? LIMIT 1",
		sessionID,
	)
	if err != nil || res == nil || res.Count == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	row, ok := res.Rows[0].([]interface{})
	if !ok || len(row) < 7 {
		writeError(w, http.StatusInternalServerError, "invalid session data")
		return
	}

	status := getString(row[2])
	wallet := getString(row[3])
	apiKey := getString(row[4])
	errorMsg := getString(row[5])
	expiresAtStr := getString(row[6])
	namespace := getString(row[1])

	// Check expiration if still pending
	if status == "pending" {
		if expiresAt, err := time.Parse("2006-01-02 15:04:05", expiresAtStr); err == nil {
			if time.Now().UTC().After(expiresAt) {
				status = "expired"
				// Update in DB
				_, _ = db.Query(internalCtx,
					"UPDATE phantom_auth_sessions SET status = 'expired' WHERE id = ? AND status = 'pending'",
					sessionID,
				)
			}
		}
	}

	resp := map[string]any{
		"session_id": sessionID,
		"status":     status,
		"namespace":  namespace,
	}
	if wallet != "" {
		resp["wallet"] = wallet
	}
	if apiKey != "" {
		resp["api_key"] = apiKey
	}
	if errorMsg != "" {
		resp["error"] = errorMsg
	}

	writeJSON(w, http.StatusOK, resp)
}

// PhantomCompleteHandler completes Phantom authentication.
// Called by the React auth app after the user signs with Phantom.
//
// POST /v1/auth/phantom/complete
// Request body: { "session_id", "wallet", "nonce", "signature", "namespace" }
// Response: { "success": true }
func (h *Handlers) PhantomCompleteHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Wallet    string `json:"wallet"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
		Namespace string `json:"namespace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if req.SessionID == "" || req.Wallet == "" || req.Nonce == "" || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "session_id, wallet, nonce and signature are required")
		return
	}

	if !sessionIDRegex.MatchString(req.SessionID) {
		writeError(w, http.StatusBadRequest, "invalid session_id format")
		return
	}

	ctx := r.Context()
	internalCtx := h.internalAuthFn(ctx)
	db := h.netClient.Database()

	// Validate session exists, is pending, and not expired
	res, err := db.Query(internalCtx,
		"SELECT status, expires_at FROM phantom_auth_sessions WHERE id = ? LIMIT 1",
		req.SessionID,
	)
	if err != nil || res == nil || res.Count == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	row, ok := res.Rows[0].([]interface{})
	if !ok || len(row) < 2 {
		writeError(w, http.StatusInternalServerError, "invalid session data")
		return
	}

	status := getString(row[0])
	expiresAtStr := getString(row[1])

	if status != "pending" {
		writeError(w, http.StatusConflict, "session is not pending (status: "+status+")")
		return
	}

	if expiresAt, err := time.Parse("2006-01-02 15:04:05", expiresAtStr); err == nil {
		if time.Now().UTC().After(expiresAt) {
			_, _ = db.Query(internalCtx,
				"UPDATE phantom_auth_sessions SET status = 'expired' WHERE id = ?",
				req.SessionID,
			)
			writeError(w, http.StatusGone, "session expired")
			return
		}
	}

	// Verify Ed25519 signature (Solana)
	verified, err := h.authService.VerifySignature(ctx, req.Wallet, req.Nonce, req.Signature, "SOL")
	if err != nil || !verified {
		h.updateSessionFailed(internalCtx, db, req.SessionID, "signature verification failed")
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Mark nonce used
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = "default"
	}
	nsID, _ := h.resolveNamespace(ctx, namespace)
	h.markNonceUsed(ctx, nsID, strings.ToLower(req.Wallet), req.Nonce)

	// Verify NFT ownership (server-side)
	if h.solanaVerifier != nil {
		owns, err := h.solanaVerifier.VerifyNFTOwnership(ctx, req.Wallet)
		if err != nil {
			h.updateSessionFailed(internalCtx, db, req.SessionID, "NFT verification error: "+err.Error())
			writeError(w, http.StatusInternalServerError, "NFT verification failed")
			return
		}
		if !owns {
			h.updateSessionFailed(internalCtx, db, req.SessionID, "wallet does not own required NFT")
			writeError(w, http.StatusForbidden, "wallet does not own an NFT from the required collection")
			return
		}
	}

	// Issue API key
	apiKey, err := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, namespace)
	if err != nil {
		h.updateSessionFailed(internalCtx, db, req.SessionID, "failed to issue API key")
		writeError(w, http.StatusInternalServerError, "failed to issue API key")
		return
	}

	// Update session to completed (AND status = 'pending' prevents race condition)
	_, _ = db.Query(internalCtx,
		"UPDATE phantom_auth_sessions SET status = 'completed', wallet = ?, api_key = ? WHERE id = ? AND status = 'pending'",
		strings.ToLower(req.Wallet), apiKey, req.SessionID,
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// updateSessionFailed marks a session as failed with an error message.
func (h *Handlers) updateSessionFailed(ctx context.Context, db DatabaseClient, sessionID, errMsg string) {
	_, _ = db.Query(ctx, "UPDATE phantom_auth_sessions SET status = 'failed', error_message = ? WHERE id = ?", errMsg, sessionID)
}

// getString extracts a string from an interface value.
func getString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
