package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
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

	// Mark nonce used
	nsID, _ := h.resolveNamespace(ctx, req.Namespace)
	h.markNonceUsed(ctx, nsID, strings.ToLower(req.Wallet), req.Nonce)

	// Optional device assertion (bugboard feat-384). Verified against the SAME
	// challenge the account just signed, so both signatures describe one login
	// event rather than two independently replayable facts.
	//
	// A malformed or non-verifying assertion is a hard 401, never a silent
	// downgrade to an account-only token: a client that meant to prove a device
	// and failed must find out, not receive a token that quietly lacks the
	// claim and get denied later by a function for reasons it cannot see.
	device, devStatus, devErr := h.bindDeviceIfAsserted(ctx, &req)
	if devErr != nil {
		writeError(w, devStatus, devicePublicError(devStatus))
		return
	}

	// Check if namespace cluster provisioning is needed (for non-default namespaces)
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = "default"
	}

	if h.clusterProvisioner != nil && namespace != "default" {
		clusterID, status, needsProvisioning, checkErr := h.clusterProvisioner.CheckNamespaceCluster(ctx, namespace)
		if checkErr != nil {
			_ = checkErr // Log but don't fail
		} else if needsProvisioning || status == "provisioning" {
			// Issue tokens and API key before returning provisioning status
			token, refresh, expUnix, tokenErr := h.authService.IssueTokensForDevice(ctx, req.Wallet, req.Namespace, device)
			if tokenErr != nil {
				writeError(w, http.StatusInternalServerError, tokenErr.Error())
				return
			}
			apiKey, keyErr := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
			if keyErr != nil {
				writeError(w, http.StatusInternalServerError, keyErr.Error())
				return
			}

			pollURL := ""
			if needsProvisioning {
				nsIDInt := 0
				if id, ok := nsID.(int); ok {
					nsIDInt = id
				} else if id, ok := nsID.(int64); ok {
					nsIDInt = int(id)
				} else if id, ok := nsID.(float64); ok {
					nsIDInt = int(id)
				}

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

	token, refresh, expUnix, err := h.authService.IssueTokensForDevice(ctx, req.Wallet, req.Namespace, device)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	apiKey, err := h.authService.GetOrCreateAPIKey(ctx, req.Wallet, req.Namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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

// bindDeviceIfAsserted verifies an optional device assertion and records the
// binding, returning nil when the request carried no assertion.
//
// Presenting only one of the two fields is rejected rather than ignored: it is
// always a client bug, and silently treating it as "no device" would hand back
// a token missing the claim the client believes it just obtained.
func (h *Handlers) bindDeviceIfAsserted(ctx context.Context, req *VerifyRequest) (*authsvc.DeviceBinding, int, error) {
	pub := strings.TrimSpace(req.DevicePublicKey)
	sig := strings.TrimSpace(req.DeviceSignature)
	if pub == "" && sig == "" {
		return nil, http.StatusOK, nil
	}
	if pub == "" || sig == "" {
		// A client bug, not a failed authentication: 400 so it is not mistaken
		// for rejected credentials and retried as a re-login.
		return nil, http.StatusBadRequest, fmt.Errorf("device_public_key and device_signature must be provided together")
	}

	fingerprint, err := authsvc.VerifyDeviceAssertion(req.Nonce, pub, sig)
	if err != nil {
		return nil, http.StatusUnauthorized, err
	}

	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = "default"
	}
	binding, err := h.authService.BindDevice(ctx, namespace, req.Wallet, pub, fingerprint)
	if err != nil {
		// Infrastructure failure is retryable, not a credential verdict — a
		// 401 here would send every client into a re-SIWE loop during a leader
		// re-election (bugboard #125).
		if errors.Is(err, authsvc.ErrDeviceBindTransient) {
			h.logDeviceBindFailure(namespace, err)
			return nil, http.StatusServiceUnavailable, err
		}
		h.logDeviceBindFailure(namespace, err)
		return nil, http.StatusUnauthorized, err
	}
	return binding, http.StatusOK, nil
}

// devicePublicError is the client-facing message for a device-assertion
// failure. Deliberately fixed text: the wrapped errors carry SQL and
// namespace-resolution detail, and this endpoint is reachable unauthenticated.
func devicePublicError(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "device_public_key and device_signature must be provided together"
	case http.StatusServiceUnavailable:
		return "device binding temporarily unavailable, retry"
	default:
		return "device assertion verification failed"
	}
}

// logDeviceBindFailure records the real cause server-side, since the client
// only receives the generic message above.
func (h *Handlers) logDeviceBindFailure(namespace string, err error) {
	if h.logger == nil {
		return
	}
	h.logger.Warn("device binding failed",
		zap.String("namespace", namespace), zap.Error(err))
}
