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
	// ExpirySeconds wins over ExpiryMinutes; minutes alone could not say 30s,
	// so a sub-minute expiry became 0 and took the default hour.
	ExpirySeconds int `json:"expiry_seconds,omitempty"`
	ExpiryMinutes int `json:"expiry_minutes,omitempty"`
}

// Invite lifetimes. An invite token is a credential for every secret the
// cluster holds, so it is short-lived by design.
const (
	defaultInviteExpiry = time.Hour
	maxInviteExpiry     = time.Hour
)

// expiry is how long the requested invite lives: seconds if given, else
// minutes, else the default, and never past the cap. The count is clamped
// before it becomes a Duration, so a huge one cannot overflow into a negative
// lifetime.
func (req InviteRequest) expiry() time.Duration {
	switch {
	case req.ExpirySeconds > 0:
		return time.Duration(min(req.ExpirySeconds, int(maxInviteExpiry/time.Second))) * time.Second
	case req.ExpiryMinutes > 0:
		return time.Duration(min(req.ExpiryMinutes, int(maxInviteExpiry/time.Minute))) * time.Minute
	}
	return defaultInviteExpiry
}

// valid refuses a negative expiry: it can only be a mistake, and taking the
// default would mint an invite the caller did not ask for.
func (req InviteRequest) valid() error {
	if req.ExpirySeconds < 0 || req.ExpiryMinutes < 0 {
		return fmt.Errorf("expiry must not be negative")
	}
	return nil
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

	// Optional expiry from the body (default and cap: an hour). An empty body,
	// chunked or not, takes the default; a body that is not the request is
	// refused rather than silently ignored.
	var req InviteRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInviteBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read the request body")
		return
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body: expected {\"expiry_seconds\": N} or no body at all")
			return
		}
		if err := req.valid(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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

	expiresAt := time.Now().UTC().Add(req.expiry())
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
