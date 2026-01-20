package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// IssueAPIKeyHandler issues an API key after signature verification.
// Similar to VerifyHandler but only returns the API key without JWT tokens.
//
// POST /v1/auth/api-key
// Request body: APIKeyRequest
// Response: { "api_key", "namespace", "plan", "wallet" }
func (h *Handlers) IssueAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req APIKeyRequest
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
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// Mark nonce used
	nsID, _ := h.resolveNamespace(ctx, req.Namespace)
	h.markNonceUsed(ctx, nsID, strings.ToLower(req.Wallet), req.Nonce)

	apiKey, err := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": req.Namespace,
		"plan": func() string {
			if strings.TrimSpace(req.Plan) == "" {
				return "free"
			}
			return req.Plan
		}(),
		"wallet": strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
	})
}

// SimpleAPIKeyHandler generates an API key without signature verification.
// This is a simplified flow for development/testing purposes.
//
// POST /v1/auth/simple-key
// Request body: SimpleAPIKeyRequest
// Response: { "api_key", "namespace", "wallet", "created" }
func (h *Handlers) SimpleAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req SimpleAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Wallet) == "" {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}

	apiKey, err := h.authService.GetOrCreateAPIKey(r.Context(), req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":   apiKey,
		"namespace": req.Namespace,
		"wallet":    strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(req.Wallet, "0x"), "0X")),
		"created":   time.Now().Format(time.RFC3339),
	})
}
