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
// Response 200: { "access_token", "token_type", "expires_in", "refresh_token", "subject", "namespace", "nonce", "signature_verified" }
// plus "api_key", except in the lobby namespace, which has none.
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
	if strings.TrimSpace(req.Message) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "message and signature are required: sign the message "+
			"returned by /v1/auth/challenge and send it back verbatim")
		return
	}

	ctx := r.Context()
	in, ok := h.signIn(w, r, req.Message, req.Signature)
	if !ok {
		return
	}
	wallet, namespace := in.Wallet, in.Namespace

	// Refuse before anything is issued or provisioned: a namespace that belongs
	// to another wallet is not this caller's to sign in to.
	if err := h.authService.RequireNamespaceOwner(ctx, wallet, namespace); err != nil {
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

	token, refresh, expUnix, err := h.authService.IssueTokens(ctx, wallet, namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The lobby has no keys. A wallet signing in there gets a session and
	// nothing else; the one thing that session reaches is POST /v1/namespaces,
	// which creates a namespace and makes the caller its owner.
	apiKey := ""
	if !authsvc.IsLobbyNamespace(namespace) {
		apiKey, err = h.authService.GetOrCreateAPIKey(ctx, wallet, namespace)
		if err != nil {
			writeCredentialError(w, namespace, err)
			return
		}
	}

	h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     wallet,
		Action:    authsvc.AuditVerifySucceeded,
		Result:    authsvc.AuditSuccess,
	})

	body := map[string]any{
		"access_token":       token,
		"token_type":         "Bearer",
		"expires_in":         int(expUnix - time.Now().Unix()),
		"refresh_token":      refresh,
		"subject":            wallet,
		"namespace":          namespace,
		"nonce":              in.Message.Nonce,
		"signature_verified": true,
	}
	if apiKey != "" {
		body["api_key"] = apiKey
	}
	writeJSON(w, http.StatusOK, body)
}
