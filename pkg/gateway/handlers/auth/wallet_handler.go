package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// WhoamiHandler returns the authenticated user's identity and method.
// This endpoint shows whether the request is authenticated via JWT or API key,
// and provides details about the authenticated principal.
//
// GET /v1/auth/whoami
// Response: { "authenticated", "method", "subject", "namespace", ... }
func (h *Handlers) WhoamiHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Determine namespace (may be overridden by auth layer)
	ns := h.defaultNS
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}

	// Prefer JWT if present
	if v := ctx.Value(CtxKeyJWT); v != nil {
		if claims, ok := v.(*authsvc.JWTClaims); ok && claims != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"method":        "jwt",
				"subject":       claims.Sub,
				"issuer":        claims.Iss,
				"audience":      claims.Aud,
				"issued_at":     claims.Iat,
				"not_before":    claims.Nbf,
				"expires_at":    claims.Exp,
				"namespace":     ns,
			})
			return
		}
	}

	// Fallback: API key identity
	var key string
	if v := ctx.Value(CtxKeyAPIKey); v != nil {
		if s, ok := v.(string); ok {
			key = s
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": key != "",
		"method":        "api_key",
		"api_key":       key,
		"namespace":     ns,
	})
}

// RegisterHandler registers a new application/client after wallet signature verification.
// This allows wallets to register applications and obtain client credentials.
//
// POST /v1/auth/register
// Request body: RegisterRequest
// Response: { "client_id", "app": { ... }, "signature_verified" }
func (h *Handlers) RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RegisterRequest
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

	// In a real app we'd derive the public key from the signature, but for simplicity here
	// we just use a placeholder or expect it in the request if needed.
	// For Ethereum, we can recover it.
	publicKey := "recovered-pk"

	appID, err := h.authService.RegisterApp(ctx, req.Wallet, req.Namespace, req.Name, publicKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": appID,
		"app": map[string]any{
			"app_id":    appID,
			"name":      req.Name,
			"namespace": req.Namespace,
			"wallet":    strings.ToLower(req.Wallet),
		},
		"signature_verified": true,
	})
}

