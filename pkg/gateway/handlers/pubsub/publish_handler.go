package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// PublishHandler handles POST /v1/pubsub/publish {topic, data_base64}
func (p *PubSubHandlers) PublishHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	var body PublishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Topic == "" || body.DataB64 == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {topic,data_base64}")
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 data")
		return
	}

	// Check for local websocket subscribers FIRST and deliver directly
	p.mu.RLock()
	localSubs := p.getLocalSubscribers(body.Topic, ns)
	p.mu.RUnlock()

	localDeliveryCount := 0
	if len(localSubs) > 0 {
		for _, sub := range localSubs {
			select {
			case sub.msgChan <- data:
				localDeliveryCount++
				p.logger.ComponentDebug("gateway", "delivered to local subscriber",
					zap.String("topic", body.Topic))
			default:
				// Drop if buffer full
				p.logger.ComponentWarn("gateway", "local subscriber buffer full, dropping message",
					zap.String("topic", body.Topic))
			}
		}
	}

	p.logger.ComponentInfo("gateway", "pubsub publish: processing message",
		zap.String("topic", body.Topic),
		zap.String("namespace", ns),
		zap.Int("data_len", len(data)),
		zap.Int("local_subscribers", len(localSubs)),
		zap.Int("local_delivered", localDeliveryCount))

	// Publish to libp2p asynchronously for cross-node delivery
	// This prevents blocking the HTTP response if libp2p network is slow
	go func() {
		publishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		ctx := pubsub.WithNamespace(client.WithInternalAuth(publishCtx), ns)
		if err := p.client.PubSub().Publish(ctx, body.Topic, data); err != nil {
			p.logger.ComponentWarn("gateway", "async libp2p publish failed",
				zap.String("topic", body.Topic),
				zap.Error(err))
		} else {
			p.logger.ComponentDebug("gateway", "async libp2p publish succeeded",
				zap.String("topic", body.Topic))
		}
	}()

	// Return immediately after local delivery
	// Local WebSocket subscribers already received the message
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// TopicsHandler lists topics within the caller's namespace
func (p *PubSubHandlers) TopicsHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	// Apply namespace isolation
	ctx := pubsub.WithNamespace(client.WithInternalAuth(r.Context()), ns)
	all, err := p.client.PubSub().ListTopics(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Client returns topics already trimmed to its namespace; return as-is
	writeJSON(w, http.StatusOK, map[string]any{"topics": all})
}

// writeError writes an error response
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
