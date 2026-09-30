package operator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// maxInviteBody bounds the invite request body; it only ever holds an expiry.
const maxInviteBody = 4096

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

	// Optional expiry from the body (default and cap: 60 min). An empty body,
	// chunked or not, takes the default; a body that is not the request is
	// refused rather than silently ignored.
	expiryMinutes := 60
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInviteBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read the request body")
		return
	}
	if len(body) > 0 {
		var req InviteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body: expected {\"expiry_minutes\": N} or no body at all")
			return
		}
		if req.ExpiryMinutes > 0 {
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
	_, err = h.rqliteClient.Exec(ctx,
		"INSERT INTO invite_tokens (token, created_by, expires_at, operator_wallet) VALUES (?, ?, ?, ?)",
		HashInviteToken(token), fmt.Sprintf("operator:%s", wallet), expiresAtStr, wallet)
	if err != nil {
		h.logger.Error("failed to store invite token", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create invite token")
		return
	}

	// An invite is a credential for every secret this cluster holds, and
	// nothing recorded that one had been minted.
	h.audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Actor:    wallet,
		Action:   auth.AuditOperatorAction,
		Resource: "invite",
		Result:   auth.AuditSuccess,
		Metadata: map[string]string{"expires_at": expiresAtStr},
	})

	writeJSON(w, http.StatusOK, InviteResponse{
		Token:     token,
		ExpiresAt: expiresAtStr,
	})
}
