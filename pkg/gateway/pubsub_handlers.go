package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// For early development we accept any origin; tighten later.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// pubsubWebsocketHandler upgrades to WS, subscribes to a namespaced topic, and
// forwards received PubSub messages to the client. Messages sent by the client
// are published to the same namespaced topic.
func (g *Gateway) pubsubWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		g.logger.ComponentWarn("gateway", "pubsub ws: client not initialized")
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodGet {
		g.logger.ComponentWarn("gateway", "pubsub ws: method not allowed")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Resolve namespace from auth context
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		g.logger.ComponentWarn("gateway", "pubsub ws: namespace not resolved")
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	topic := r.URL.Query().Get("topic")
	if topic == "" {
		g.logger.ComponentWarn("gateway", "pubsub ws: missing topic")
		writeError(w, http.StatusBadRequest, "missing 'topic'")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		g.logger.ComponentWarn("gateway", "pubsub ws: upgrade failed")
		return
	}
	defer conn.Close()

	// Channel to deliver PubSub messages to WS writer
	msgs := make(chan []byte, 128)
	
	// NEW: Register as local subscriber for direct message delivery
	localSub := &localSubscriber{
		msgChan:   msgs,
		namespace: ns,
	}
	topicKey := fmt.Sprintf("%s.%s", ns, topic)
	
	g.mu.Lock()
	g.localSubscribers[topicKey] = append(g.localSubscribers[topicKey], localSub)
	subscriberCount := len(g.localSubscribers[topicKey])
	g.mu.Unlock()
	
	g.logger.ComponentInfo("gateway", "pubsub ws: registered local subscriber",
		zap.String("topic", topic),
		zap.String("namespace", ns),
		zap.Int("total_subscribers", subscriberCount))
	
	// Unregister on close
	defer func() {
		g.mu.Lock()
		subs := g.localSubscribers[topicKey]
		for i, sub := range subs {
			if sub == localSub {
				g.localSubscribers[topicKey] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		remainingCount := len(g.localSubscribers[topicKey])
		if remainingCount == 0 {
			delete(g.localSubscribers, topicKey)
		}
		g.mu.Unlock()
		g.logger.ComponentInfo("gateway", "pubsub ws: unregistered local subscriber",
			zap.String("topic", topic),
			zap.Int("remaining_subscribers", remainingCount))
	}()
	
	// Use internal auth context when interacting with client to avoid circular auth requirements
	ctx := client.WithInternalAuth(r.Context())
	// Apply namespace isolation
	ctx = pubsub.WithNamespace(ctx, ns)
	
	// Writer loop - START THIS FIRST before libp2p subscription
	done := make(chan struct{})
	go func() {
		g.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine started",
			zap.String("topic", topic))
		defer g.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine exiting",
			zap.String("topic", topic))
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case b, ok := <-msgs:
				if !ok {
					g.logger.ComponentWarn("gateway", "pubsub ws: message channel closed",
						zap.String("topic", topic))
					_ = conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(5*time.Second))
					close(done)
					return
				}
				
				g.logger.ComponentInfo("gateway", "pubsub ws: sending message to client",
					zap.String("topic", topic),
					zap.Int("data_len", len(b)))
				
				// Format message as JSON envelope with data (base64 encoded), timestamp, and topic
				// This matches the SDK's Message interface: {data: string, timestamp: number, topic: string}
				envelope := map[string]interface{}{
					"data":      base64.StdEncoding.EncodeToString(b),
					"timestamp": time.Now().UnixMilli(),
					"topic":     topic,
				}
				envelopeJSON, err := json.Marshal(envelope)
				if err != nil {
					g.logger.ComponentWarn("gateway", "pubsub ws: failed to marshal envelope",
						zap.String("topic", topic),
						zap.Error(err))
					continue
				}
				
				g.logger.ComponentDebug("gateway", "pubsub ws: envelope created",
					zap.String("topic", topic),
					zap.Int("envelope_len", len(envelopeJSON)))
				
				conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, envelopeJSON); err != nil {
					g.logger.ComponentWarn("gateway", "pubsub ws: failed to write to websocket",
						zap.String("topic", topic),
						zap.Error(err))
					close(done)
					return
				}
				
				g.logger.ComponentInfo("gateway", "pubsub ws: message sent successfully",
					zap.String("topic", topic))
			case <-ticker.C:
				// Ping keepalive
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			case <-ctx.Done():
				close(done)
				return
			}
		}
	}()

	// Subscribe to libp2p for cross-node messages (in background, non-blocking)
	go func() {
		h := func(_ string, data []byte) error {
			g.logger.ComponentInfo("gateway", "pubsub ws: received message from libp2p",
				zap.String("topic", topic),
				zap.Int("data_len", len(data)))
			
			select {
			case msgs <- data:
				g.logger.ComponentInfo("gateway", "pubsub ws: forwarded to client",
					zap.String("topic", topic),
					zap.String("source", "libp2p"))
				return nil
			default:
				// Drop if client is slow to avoid blocking network
				g.logger.ComponentWarn("gateway", "pubsub ws: client slow, dropping message",
					zap.String("topic", topic))
				return nil
			}
		}
		if err := g.client.PubSub().Subscribe(ctx, topic, h); err != nil {
			g.logger.ComponentWarn("gateway", "pubsub ws: libp2p subscribe failed (will use local-only)",
				zap.String("topic", topic),
				zap.Error(err))
			return
		}
		g.logger.ComponentInfo("gateway", "pubsub ws: libp2p subscription established",
			zap.String("topic", topic))
		
		// Keep subscription alive until done
		<-done
		_ = g.client.PubSub().Unsubscribe(ctx, topic)
		g.logger.ComponentInfo("gateway", "pubsub ws: libp2p subscription closed",
			zap.String("topic", topic))
	}()

	// Reader loop: treat any client message as publish to the same topic
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
			continue
		}
		
		// Filter out WebSocket heartbeat messages
		// Don't publish them to the topic
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err == nil {
			if msgType, ok := msg["type"].(string); ok && msgType == "ping" {
				g.logger.ComponentInfo("gateway", "pubsub ws: filtering out heartbeat ping")
				continue
			}
		}
		
		if err := g.client.PubSub().Publish(ctx, topic, data); err != nil {
			// Best-effort notify client
			_ = conn.WriteMessage(websocket.TextMessage, []byte("publish_error"))
		}
	}
	<-done
}

// pubsubPublishHandler handles POST /v1/pubsub/publish {topic, data_base64}
func (g *Gateway) pubsubPublishHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
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
	var body struct {
		Topic   string `json:"topic"`
		DataB64 string `json:"data_base64"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Topic == "" || body.DataB64 == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {topic,data_base64}")
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	
	// NEW: Check for local websocket subscribers FIRST and deliver directly
	g.mu.RLock()
	localSubs := g.getLocalSubscribers(body.Topic, ns)
	g.mu.RUnlock()
	
	localDeliveryCount := 0
	if len(localSubs) > 0 {
		for _, sub := range localSubs {
			select {
			case sub.msgChan <- data:
				localDeliveryCount++
				g.logger.ComponentDebug("gateway", "delivered to local subscriber",
					zap.String("topic", body.Topic))
			default:
				// Drop if buffer full
				g.logger.ComponentWarn("gateway", "local subscriber buffer full, dropping message",
					zap.String("topic", body.Topic))
			}
		}
	}
	
	g.logger.ComponentInfo("gateway", "pubsub publish: processing message",
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
		if err := g.client.PubSub().Publish(ctx, body.Topic, data); err != nil {
			g.logger.ComponentWarn("gateway", "async libp2p publish failed",
				zap.String("topic", body.Topic),
				zap.Error(err))
		} else {
			g.logger.ComponentDebug("gateway", "async libp2p publish succeeded",
				zap.String("topic", body.Topic))
		}
	}()
	
	// Return immediately after local delivery
	// Local WebSocket subscribers already received the message
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// pubsubTopicsHandler lists topics within the caller's namespace
func (g *Gateway) pubsubTopicsHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
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
	all, err := g.client.PubSub().ListTopics(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Client returns topics already trimmed to its namespace; return as-is
	writeJSON(w, http.StatusOK, map[string]any{"topics": all})
}

// resolveNamespaceFromRequest gets namespace from context set by auth middleware
func resolveNamespaceFromRequest(r *http.Request) string {
	if v := r.Context().Value(ctxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func namespacePrefix(ns string) string {
	return "ns::" + ns + "::"
}

func namespacedTopic(ns, topic string) string {
	return namespacePrefix(ns) + topic
}
