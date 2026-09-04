package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// setSecretRequest is the request body for setting a secret.
type setSecretRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HandleSetSecret handles PUT /v1/functions/secrets
// Stores an encrypted secret scoped to the caller's namespace.
func (h *ServerlessHandlers) HandleSetSecret(w http.ResponseWriter, r *http.Request) {
	if h.secretsManager == nil {
		writeError(w, http.StatusNotImplemented, "Secrets management not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	var req setSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "secret name required")
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "secret value required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.secretsManager.Set(ctx, namespace, req.Name, req.Value); err != nil {
		h.logger.Error("Failed to set secret",
			zap.String("namespace", namespace),
			zap.String("name", req.Name),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to set secret: "+err.Error())
		return
	}

	h.logger.Info("Secret set via API",
		zap.String("namespace", namespace),
		zap.String("name", req.Name),
	)

	// The name only. A secret's value is the one thing that must never reach a
	// replicated table.
	h.recordAudit(r, namespace, auth.AuditSecretSet, req.Name)

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Secret set",
		"name":      req.Name,
		"namespace": namespace,
	})
}

// HandleListSecrets handles GET /v1/functions/secrets
// Lists all secret names in the caller's namespace (values are never returned).
func (h *ServerlessHandlers) HandleListSecrets(w http.ResponseWriter, r *http.Request) {
	if h.secretsManager == nil {
		writeError(w, http.StatusNotImplemented, "Secrets management not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	names, err := h.secretsManager.List(ctx, namespace)
	if err != nil {
		h.logger.Error("Failed to list secrets",
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to list secrets")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"secrets": names,
		"count":   len(names),
	})
}

// HandleDeleteSecret handles DELETE /v1/functions/secrets/{name}
// Deletes a secret from the caller's namespace.
func (h *ServerlessHandlers) HandleDeleteSecret(w http.ResponseWriter, r *http.Request, secretName string) {
	if h.secretsManager == nil {
		writeError(w, http.StatusNotImplemented, "Secrets management not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.secretsManager.Delete(ctx, namespace, secretName); err != nil {
		if errors.Is(err, serverless.ErrSecretNotFound) {
			writeError(w, http.StatusNotFound, "Secret not found")
			return
		}
		h.logger.Error("Failed to delete secret",
			zap.String("namespace", namespace),
			zap.String("name", secretName),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to delete secret: "+err.Error())
		return
	}

	h.logger.Info("Secret deleted via API",
		zap.String("namespace", namespace),
		zap.String("name", secretName),
	)

	h.recordAudit(r, namespace, auth.AuditSecretDeleted, secretName)

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Secret deleted",
	})
}
