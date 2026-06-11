package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// addTriggerRequest is the request body for adding a PubSub or Cron trigger.
// Exactly one of `topic` or `cron_expression` must be set.
type addTriggerRequest struct {
	Topic          string `json:"topic"`
	CronExpression string `json:"cron_expression"`
}

// HandleAddTrigger handles POST /v1/functions/{name}/triggers
// Branches between PubSub (topic) and Cron (cron_expression) based on the
// request body. Both stores must be wired for their respective branches.
func (h *ServerlessHandlers) HandleAddTrigger(w http.ResponseWriter, r *http.Request, functionName string) {
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

	if req.Topic == "" && req.CronExpression == "" {
		writeError(w, http.StatusBadRequest, "topic or cron_expression required")
		return
	}
	if req.Topic != "" && req.CronExpression != "" {
		writeError(w, http.StatusBadRequest, "topic and cron_expression are mutually exclusive")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	if req.CronExpression != "" {
		if h.cronStore == nil {
			writeError(w, http.StatusNotImplemented, "Cron triggers not available")
			return
		}
		triggerID, err := h.cronStore.Add(ctx, fn.ID, req.CronExpression)
		if err != nil {
			h.logger.Error("Failed to add Cron trigger",
				zap.String("function", functionName),
				zap.String("cron_expression", req.CronExpression),
				zap.Error(err),
			)
			writeError(w, http.StatusBadRequest, "Failed to add trigger: "+err.Error())
			return
		}
		h.logger.Info("Cron trigger added via API",
			zap.String("function", functionName),
			zap.String("cron_expression", req.CronExpression),
			zap.String("trigger_id", triggerID),
		)
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"trigger_id":      triggerID,
			"function":        functionName,
			"cron_expression": req.CronExpression,
		})
		return
	}

	if h.triggerStore == nil {
		writeError(w, http.StatusNotImplemented, "PubSub triggers not available")
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
	if h.dispatcher != nil {
		// Refresh subscribes the dispatcher to libp2p for this newly-added
		// trigger's topic so future WASM publishes reach the handler
		// (bugboard #282). Best-effort — Refresh failures are logged
		// inside; the periodic refresh loop will retry within 60s.
		if rerr := h.dispatcher.Refresh(ctx); rerr != nil {
			h.logger.Warn("PubSubDispatcher Refresh after trigger add failed (periodic loop will retry)",
				zap.Error(rerr))
		}
		// Legacy no-op — kept for back-compat with anything still
		// calling it; can be removed in a future cleanup.
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
// Returns the merged set of PubSub and Cron triggers for a function.
// Each row carries enough metadata for the CLI's `triggers list` to render
// it; the kind is implied by which fields are populated (Topic vs CronExpression).
func (h *ServerlessHandlers) HandleListTriggers(w http.ResponseWriter, r *http.Request, functionName string) {
	if h.triggerStore == nil && h.cronStore == nil {
		writeError(w, http.StatusNotImplemented, "Triggers not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	merged := []map[string]interface{}{}
	if h.triggerStore != nil {
		pubsubTriggers, err := h.triggerStore.ListByFunction(ctx, fn.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list pubsub triggers")
			return
		}
		for _, t := range pubsubTriggers {
			merged = append(merged, map[string]interface{}{
				"id":      t.ID,
				"kind":    "pubsub",
				"topic":   t.Topic,
				"enabled": t.Enabled,
			})
		}
	}
	if h.cronStore != nil {
		cronTriggers, err := h.cronStore.ListByFunction(ctx, fn.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list cron triggers")
			return
		}
		for _, t := range cronTriggers {
			merged = append(merged, map[string]interface{}{
				"id":              t.ID,
				"kind":            "cron",
				"cron_expression": t.CronExpression,
				"next_run_at":     t.NextRunAt,
				"last_run_at":     t.LastRunAt,
				"enabled":         t.Enabled,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"triggers": merged,
		"count":    len(merged),
	})
}

// HandleDeleteTrigger handles DELETE /v1/functions/{name}/triggers/{triggerID}
// Removes either a PubSub or Cron trigger. Tries PubSub first (the more
// common case) and falls back to Cron — trigger IDs are UUIDs and can't
// collide between stores, so order is just an optimisation.
func (h *ServerlessHandlers) HandleDeleteTrigger(w http.ResponseWriter, r *http.Request, functionName, triggerID string) {
	if h.triggerStore == nil && h.cronStore == nil {
		writeError(w, http.StatusNotImplemented, "Triggers not available")
		return
	}

	namespace := h.getNamespaceFromRequest(r)
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	fn, err := h.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to look up function")
		}
		return
	}

	// Walk the PubSub list first to capture the topic for cache invalidation.
	var triggerTopic string
	if h.triggerStore != nil {
		triggers, listErr := h.triggerStore.ListByFunction(ctx, fn.ID)
		if listErr == nil {
			for _, t := range triggers {
				if t.ID == triggerID {
					triggerTopic = t.Topic
					break
				}
			}
		}
	}

	if triggerTopic != "" {
		if err := h.triggerStore.Remove(ctx, triggerID); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to remove trigger: "+err.Error())
			return
		}
		if h.dispatcher != nil {
			// Refresh prunes the dispatcher's libp2p subscription if this
			// was the last trigger on that topic (bugboard #282).
			if rerr := h.dispatcher.Refresh(ctx); rerr != nil {
				h.logger.Warn("PubSubDispatcher Refresh after trigger remove failed (periodic loop will retry)",
					zap.Error(rerr))
			}
			h.dispatcher.InvalidateCache(ctx, namespace, triggerTopic)
		}
		h.logger.Info("PubSub trigger removed via API",
			zap.String("function", functionName),
			zap.String("trigger_id", triggerID),
		)
		writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Trigger removed"})
		return
	}

	// Not a PubSub trigger — try cron.
	if h.cronStore != nil {
		if err := h.cronStore.Remove(ctx, triggerID); err == nil {
			h.logger.Info("Cron trigger removed via API",
				zap.String("function", functionName),
				zap.String("trigger_id", triggerID),
			)
			writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Trigger removed"})
			return
		}
	}

	writeError(w, http.StatusNotFound, "Trigger not found")
}
