package operator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// InviteRequest is the optional body for POST /v1/operator/invite.
type InviteRequest struct {
	ExpiryMinutes int `json:"expiry_minutes,omitempty"` // Default: 60
}

// InviteResponse is returned on success.
type InviteResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// HandleInvite generates an invite token tagged with the operator's wallet.
// Requires wallet JWT authentication.
//
// POST /v1/operator/invite
func (h *Handler) HandleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	wallet, ok := h.requireOperator(w, r)
	if !ok {
		return
	}

	// Parse optional expiry from body (default: 60min, max: 7 days).
	expiryMinutes := 60
	if r.Body != nil && r.ContentLength > 0 {
		var req InviteRequest
		if err := decodeJSON(r, &req); err == nil && req.ExpiryMinutes > 0 {
			expiryMinutes = req.ExpiryMinutes
		}
	}
	// An invite token is a credential for every secret the cluster holds, so it
	// is short-lived by design. A week was long enough to outlive the reason it
	// was minted.
	const maxExpiryMinutes = 60
	if expiryMinutes > maxExpiryMinutes {
		expiryMinutes = maxExpiryMinutes
	}

	// Generate random 32-byte token. What is returned below is the only copy
	// of it that will exist: the registry stores a hash.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		h.logger.Error("failed to generate invite token", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	expiresAt := time.Now().UTC().Add(time.Duration(expiryMinutes) * time.Minute)
	expiresAtStr := expiresAt.Format("2006-01-02 15:04:05")

	ctx := r.Context()
	_, err := h.rqliteClient.Exec(ctx,
		"INSERT INTO invite_tokens (token, created_by, expires_at, operator_wallet) VALUES (?, ?, ?, ?)",
		HashInviteToken(token), fmt.Sprintf("operator:%s", wallet), expiresAtStr, wallet)
	if err != nil {
		h.logger.Error("failed to store invite token", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create invite token")
		return
	}

	writeJSON(w, http.StatusOK, InviteResponse{
		Token:     token,
		ExpiresAt: expiresAtStr,
	})
}
