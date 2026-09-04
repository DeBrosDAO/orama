package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// ChallengeHandler issues the message a wallet is asked to sign.
//
// It used to answer with a bare 32-byte nonce. A signature over that says only
// "the holder of this key signed these bytes" — nothing about who asked, what
// for, or when — so the wallet dialog showed the user an opaque blob and any
// signature they had ever made was, in principle, an Orama login. The answer is
// an EIP-4361 message now (or its Solana counterpart): the domain, the
// namespace, the nonce and the deadline are all inside the bytes that get
// signed. See pkg/gateway/auth/challenge.go.
//
// POST /v1/auth/challenge
// Request body: ChallengeRequest
// Response: { "message", "nonce", "wallet", "namespace", "chain_type", "issued_at", "expires_at" }
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

	chain, err := authsvc.ParseChain(req.ChainType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	domain, uri, err := requestOrigin(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	challenge, err := h.authService.CreateChallenge(r.Context(), authsvc.ChallengeParams{
		Wallet:    req.Wallet,
		Purpose:   req.Purpose,
		Namespace: req.Namespace,
		Chain:     chain,
		Domain:    domain,
		URI:       uri,
	})
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
		Namespace: challenge.Namespace,
		Actor:     req.Wallet,
		Action:    authsvc.AuditChallengeIssued,
		Result:    authsvc.AuditSuccess,
		Metadata:  map[string]string{"purpose": req.Purpose},
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"message":    challenge.Message,
		"nonce":      challenge.Nonce,
		"wallet":     req.Wallet,
		"namespace":  challenge.Namespace,
		"chain_type": string(chain),
		"purpose":    req.Purpose,
		"issued_at":  challenge.IssuedAt.UTC().Format(time.RFC3339),
		"expires_at": challenge.ExpiresAt.UTC().Format(time.RFC3339),
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
