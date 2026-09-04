package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// ChallengeHandler generates a cryptographic nonce for wallet signature challenges.
// This is the first step in the authentication flow where clients request a nonce
// to sign with their wallet.
//
// POST /v1/auth/challenge
// Request body: ChallengeRequest
// Response: { "wallet", "namespace", "nonce", "purpose", "expires_at" }
func (h *Handlers) ChallengeHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
	var req ChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}

	// The wallet in the body is not the caller's to prove, and each challenge
	// writes a nonce row for it. Limiting the address alone caps one client,
	// not a distributed grind against one victim's wallet.
	if h.challengeLimiter != nil && !h.challengeLimiter.allow(req.Wallet) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests,
			"too many challenges for this wallet — wait a minute and try again")
		return
	}

	nonce, err := h.authService.CreateNonce(r.Context(), req.Wallet, req.Purpose, req.Namespace)
	if err != nil {
		h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
			Namespace: req.Namespace,
			Actor:     req.Wallet,
			Action:    authsvc.AuditChallengeIssued,
			Result:    authsvc.AuditFailure,
			Metadata:  map[string]string{"reason": err.Error()},
		})
		writeChallengeError(w, req.Namespace, err)
		return
	}

	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: req.Namespace,
		Actor:     req.Wallet,
		Action:    authsvc.AuditChallengeIssued,
		Result:    authsvc.AuditSuccess,
		Metadata:  map[string]string{"purpose": req.Purpose},
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":     req.Wallet,
		"namespace":  req.Namespace,
		"nonce":      nonce,
		"purpose":    req.Purpose,
		"expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
	})
}

// writeJSON writes JSON with status code
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a standardized JSON error
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
