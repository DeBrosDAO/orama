package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// addTriggerRequest is the request body for adding a PubSub trigger.
type addTriggerRequest struct {
	Topic string `json:"topic"`
}

// HandleAddTrigger handles POST /v1/functions/{name}/triggers
// Adds a PubSub trigger that invokes this function when a message is published to the topic.
func (h *ServerlessHandlers) HandleAddTrigger(w http.ResponseWriter, r *http.Request, functionName string) {
	if h.triggerStore == nil {
		writeError(w, http.StatusNotImplemented, "PubSub triggers not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	var req addTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	if req.Topic == "" {
		writeError(w, http.StatusBadRequest, "topic required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Look up function to get its ID
	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	triggerID, err := h.triggerStore.Add(ctx, fn.ID, req.Topic)
	if err != nil {
		h.logger.Error("Failed to add PubSub trigger",
			zap.String("function", functionName),
			zap.String("topic", req.Topic),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to add trigger: "+err.Error())
		return
	}

	// Invalidate cache for this topic
	if h.dispatcher != nil {
		h.dispatcher.InvalidateCache(ctx, namespace, req.Topic)
	}

	h.logger.Info("PubSub trigger added via API",
		zap.String("function", functionName),
		zap.String("topic", req.Topic),
		zap.String("trigger_id", triggerID),
	)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"trigger_id": triggerID,
		"function":   functionName,
		"topic":      req.Topic,
	})
}

// HandleListTriggers handles GET /v1/functions/{name}/triggers
// Lists all PubSub triggers for a function.
func (h *ServerlessHandlers) HandleListTriggers(w http.ResponseWriter, r *http.Request, functionName string) {
	if h.triggerStore == nil {
		writeError(w, http.StatusNotImplemented, "PubSub triggers not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Look up function to get its ID
	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	triggers, err := h.triggerStore.ListByFunction(ctx, fn.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list triggers")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"triggers": triggers,
		"count":    len(triggers),
	})
}

// HandleDeleteTrigger handles DELETE /v1/functions/{name}/triggers/{triggerID}
// Removes a PubSub trigger.
func (h *ServerlessHandlers) HandleDeleteTrigger(w http.ResponseWriter, r *http.Request, functionName, triggerID string) {
	if h.triggerStore == nil {
		writeError(w, http.StatusNotImplemented, "PubSub triggers not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Look up the trigger's topic before deleting (for cache invalidation)
	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	// Get current triggers to find the topic for cache invalidation
	triggers, err := h.triggerStore.ListByFunction(ctx, fn.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to look up triggers")
		return
	}

	// Find the topic for the trigger being deleted
	var triggerTopic string
	for _, t := range triggers {
		if t.ID == triggerID {
			triggerTopic = t.Topic
			break
		}
	}

	if err := h.triggerStore.Remove(ctx, triggerID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to remove trigger: "+err.Error())
		return
	}

	// Invalidate cache for the topic
	if h.dispatcher != nil && triggerTopic != "" {
		h.dispatcher.InvalidateCache(ctx, namespace, triggerTopic)
	}

	h.logger.Info("PubSub trigger removed via API",
		zap.String("function", functionName),
		zap.String("trigger_id", triggerID),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Trigger removed",
	})
}
