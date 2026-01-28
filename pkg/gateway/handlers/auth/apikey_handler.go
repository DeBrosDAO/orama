package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
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

	// Check if namespace cluster provisioning is needed (for non-default namespaces)
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = "default"
	}

	if h.clusterProvisioner != nil && namespace != "default" {
		clusterID, status, needsProvisioning, err := h.clusterProvisioner.CheckNamespaceCluster(ctx, namespace)
		if err != nil {
			// Log but don't fail - cluster provisioning is optional (error may just mean no cluster yet)
			_ = err
		} else if needsProvisioning {
			// Trigger provisioning for new namespace
			nsIDInt := 0
			if id, ok := nsID.(int); ok {
				nsIDInt = id
			} else if id, ok := nsID.(int64); ok {
				nsIDInt = int(id)
			} else if id, ok := nsID.(float64); ok {
				nsIDInt = int(id)
			}

			newClusterID, pollURL, provErr := h.clusterProvisioner.ProvisionNamespaceCluster(ctx, nsIDInt, namespace, req.Wallet)
			if provErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to start cluster provisioning")
				return
			}

			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":                 "provisioning",
				"cluster_id":             newClusterID,
				"poll_url":               pollURL,
				"estimated_time_seconds": 60,
				"message":                "Namespace cluster is being provisioned. Poll the status URL for updates.",
			})
			return
		} else if status == "provisioning" {
			// Already provisioning, return poll URL
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status":                 "provisioning",
				"cluster_id":             clusterID,
				"poll_url":               "/v1/namespace/status?id=" + clusterID,
				"estimated_time_seconds": 60,
				"message":                "Namespace cluster is being provisioned. Poll the status URL for updates.",
			})
			return
		}
		// If status is "ready" or "default", proceed with API key generation
	}

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
