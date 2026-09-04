package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
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

	if h.clusterProvisioner != nil && namespace != "default" {
		clusterID, status, needsProvisioning, checkErr := h.clusterProvisioner.CheckNamespaceCluster(ctx, namespace)
		if checkErr != nil {
			_ = checkErr // Log but don't fail
		} else if needsProvisioning || status == "provisioning" {
			// Issue tokens and API key before returning provisioning status
			token, refresh, expUnix, tokenErr := h.authService.IssueTokens(ctx, req.Wallet, req.Namespace)
			if tokenErr != nil {
				writeError(w, http.StatusInternalServerError, tokenErr.Error())
				return
			}
			apiKey, keyErr := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
			if keyErr != nil {
				writeCredentialError(w, namespace, keyErr)
				return
			}

			pollURL := ""
			if needsProvisioning {
				nsIDInt := h.namespaceIDForProvisioning(ctx, namespace)

				newClusterID, newPollURL, provErr := h.clusterProvisioner.ProvisionNamespaceCluster(ctx, nsIDInt, namespace, req.Wallet)
				if provErr != nil {
					writeError(w, http.StatusInternalServerError, "failed to start cluster provisioning")
					return
				}
				clusterID = newClusterID
				pollURL = newPollURL
			} else {
				pollURL = "/v1/namespace/status?id=" + clusterID
			}

			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":                 "provisioning",
				"cluster_id":             clusterID,
				"poll_url":               pollURL,
				"estimated_time_seconds": 60,
				"access_token":           token,
				"token_type":             "Bearer",
				"expires_in":             int(expUnix - time.Now().Unix()),
				"refresh_token":          refresh,
				"api_key":                apiKey,
				"namespace":              req.Namespace,
				"subject":                req.Wallet,
				"nonce":                  req.Nonce,
				"signature_verified":     true,
			})
			return
		}
	}

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
