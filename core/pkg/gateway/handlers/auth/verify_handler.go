package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// VerifyHandler verifies a wallet signature and issues JWT tokens and an API key.
// This completes the authentication flow by validating the signed nonce and returning
// access credentials. For non-default namespaces, may trigger cluster provisioning
// and return 202 Accepted with credentials + poll URL.
//
// POST /v1/auth/verify
// Request body: VerifyRequest
// Response 200: { "access_token", "token_type", "expires_in", "refresh_token", "subject", "namespace", "api_key", "nonce", "signature_verified" }
// Response 202: { "status": "provisioning", "cluster_id", "poll_url", "access_token", "refresh_token", "api_key", ... }
func (h *Handlers) VerifyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" || strings.TrimSpace(req.Nonce) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce and signature are required")
		return
	}

	ctx := r.Context()
	verified, err := h.authService.VerifySignature(ctx, req.Wallet, req.Nonce, req.Signature, req.ChainType)
	if err != nil || !verified {
		h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
			Namespace: req.Namespace,
			Actor:     req.Wallet,
			Action:    authsvc.AuditVerifySucceeded,
			Result:    authsvc.AuditFailure,
			Metadata:  map[string]string{"reason": "signature verification failed"},
		})
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Claim the challenge. A valid signature over a stale nonce is a replay.
	if !h.consumeNonce(ctx, w, req.Wallet, req.Nonce, req.Namespace) {
		return
	}

	// Check if namespace cluster provisioning is needed (for non-default namespaces)
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = "default"
	}

	// Refuse before anything is issued or provisioned: a namespace that belongs
	// to another wallet is not this caller's to sign in to.
	if err := h.authService.RequireNamespaceOwner(ctx, req.Wallet, namespace); err != nil {
		writeCredentialError(w, namespace, err)
		return
	}

	// Signing in does not provision anything. It used to: a challenge created
	// the namespace and verifying the signature spun up its cluster, so an
	// anonymous caller could create infrastructure by naming a name. Creating a
	// namespace is POST /v1/namespaces, and that is what provisions it.
	//
	// A namespace whose cluster is still coming up is reported by
	// /v1/namespace/status, which the create path hands back a poll URL for.

	token, refresh, expUnix, err := h.authService.IssueTokens(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	apiKey, err := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeCredentialError(w, namespace, err)
		return
	}

	h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     req.Wallet,
		Action:    authsvc.AuditVerifySucceeded,
		Result:    authsvc.AuditSuccess,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":       token,
		"token_type":         "Bearer",
		"expires_in":         int(expUnix - time.Now().Unix()),
		"refresh_token":      refresh,
		"subject":            req.Wallet,
		"namespace":          req.Namespace,
		"api_key":            apiKey,
		"nonce":              req.Nonce,
		"signature_verified": true,
	})
}
