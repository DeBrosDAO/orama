package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// IssueAPIKeyHandler issues an API key after signature verification.
// Similar to VerifyHandler but only returns the API key without JWT tokens.
// For non-default namespaces, may trigger cluster provisioning and return 202 Accepted.
//
// POST /v1/auth/api-key
// Request body: APIKeyRequest
// Response: { "api_key", "namespace", "plan", "wallet" }
// Or 202 Accepted: { "status": "provisioning", "cluster_id", "poll_url" }
func (h *Handlers) IssueAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
	var req APIKeyRequest
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

	// Issuing a key does not provision anything. See VerifyHandler: creating a
	// namespace is POST /v1/namespaces, and that is what provisions it.

	apiKey, err := h.authService.GetOrCreateAPIKey(ctx, wallet, namespace)
	if err != nil {
		writeCredentialError(w, namespace, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": namespace,
		"plan": func() string {
			if strings.TrimSpace(req.Plan) == "" {
				return "free"
			}
			return req.Plan
		}(),
		"wallet": wallet,
	})
}
