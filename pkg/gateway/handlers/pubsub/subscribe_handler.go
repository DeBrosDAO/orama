package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WebsocketHandler upgrades to WS, subscribes to a namespaced topic, and
// forwards received PubSub messages to the client. Messages sent by the client
// are published to the same namespaced topic.
func (p *PubSubHandlers) WebsocketHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: client not initialized")
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodGet {
		p.logger.ComponentWarn("gateway", "pubsub ws: method not allowed")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Resolve namespace from auth context
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		p.logger.ComponentWarn("gateway", "pubsub ws: namespace not resolved")
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	topic := r.URL.Query().Get("topic")
	if topic == "" {
		p.logger.ComponentWarn("gateway", "pubsub ws: missing topic")
		writeError(w, http.StatusBadRequest, "missing 'topic'")
		return
	}

	// Presence handling
	enablePresence := r.URL.Query().Get("presence") == "true"
	memberID := r.URL.Query().Get("member_id")
	memberMetaStr := r.URL.Query().Get("member_meta")
	var memberMeta map[string]interface{}
	if memberMetaStr != "" {
		_ = json.Unmarshal([]byte(memberMetaStr), &memberMeta)
	}

	if enablePresence && memberID == "" {
		p.logger.ComponentWarn("gateway", "pubsub ws: presence enabled but missing member_id")
		writeError(w, http.StatusBadRequest, "missing 'member_id' for presence")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: upgrade failed")
		return
	}
	defer conn.Close()

	// Channel to deliver PubSub messages to WS writer
	msgs := make(chan []byte, 128)

	// Register as local subscriber for direct message delivery
	localSub := &localSubscriber{
		msgChan:   msgs,
		namespace: ns,
	}
	topicKey := fmt.Sprintf("%s.%s", ns, topic)

	p.mu.Lock()
	p.localSubscribers[topicKey] = append(p.localSubscribers[topicKey], localSub)
	subscriberCount := len(p.localSubscribers[topicKey])
	p.mu.Unlock()

	connID := uuid.New().String()
	if enablePresence {
		member := PresenceMember{
			MemberID: memberID,
			JoinedAt: time.Now().Unix(),
			Meta:     memberMeta,
			ConnID:   connID,
		}

		p.presenceMu.Lock()
		p.presenceMembers[topicKey] = append(p.presenceMembers[topicKey], member)
		p.presenceMu.Unlock()

		// Broadcast join event (will be received via PubSub by others AND via local delivery)
		p.broadcastPresenceEvent(ns, topic, "presence.join", memberID, memberMeta, member.JoinedAt)

		p.logger.ComponentInfo("gateway", "pubsub ws: member joined presence",
			zap.String("topic", topic),
			zap.String("member_id", memberID))
	}

	p.logger.ComponentInfo("gateway", "pubsub ws: registered local subscriber",
		zap.String("topic", topic),
		zap.String("namespace", ns),
		zap.Int("total_subscribers", subscriberCount))

	// Unregister on close
	defer func() {
		p.mu.Lock()
		subs := p.localSubscribers[topicKey]
		for i, sub := range subs {
			if sub == localSub {
				p.localSubscribers[topicKey] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		remainingCount := len(p.localSubscribers[topicKey])
		if remainingCount == 0 {
			delete(p.localSubscribers, topicKey)
		}
		p.mu.Unlock()

		if enablePresence {
			p.presenceMu.Lock()
			members := p.presenceMembers[topicKey]
			for i, m := range members {
				if m.ConnID == connID {
					p.presenceMembers[topicKey] = append(members[:i], members[i+1:]...)
					break
				}
			}
			if len(p.presenceMembers[topicKey]) == 0 {
				delete(p.presenceMembers, topicKey)
			}
			p.presenceMu.Unlock()

			// Broadcast leave event
			p.broadcastPresenceEvent(ns, topic, "presence.leave", memberID, nil, time.Now().Unix())

			p.logger.ComponentInfo("gateway", "pubsub ws: member left presence",
				zap.String("topic", topic),
				zap.String("member_id", memberID))
		}

		p.logger.ComponentInfo("gateway", "pubsub ws: unregistered local subscriber",
			zap.String("topic", topic),
			zap.Int("remaining_subscribers", remainingCount))
	}()

	// Use internal auth context when interacting with client to avoid circular auth requirements
	ctx := client.WithInternalAuth(r.Context())
	// Apply namespace isolation
	ctx = pubsub.WithNamespace(ctx, ns)

	// Writer loop - START THIS FIRST before libp2p subscription
	done := make(chan struct{})
	wsClient := newWSClient(conn, topic, p.logger)
	go p.writerLoop(ctx, wsClient, msgs, done)

	// Subscribe to libp2p for cross-node messages (in background, non-blocking)
	go p.libp2pSubscriber(ctx, topic, msgs, done)

	// Reader loop: treat any client message as publish to the same topic
	p.readerLoop(ctx, wsClient, topic, done)
}

// writerLoop handles writing messages from the msgs channel to the WebSocket client
func (p *PubSubHandlers) writerLoop(ctx context.Context, wsClient *wsClient, msgs chan []byte, done chan struct{}) {
	p.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine started",
		zap.String("topic", wsClient.topic))
	defer p.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine exiting",
		zap.String("topic", wsClient.topic))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case b, ok := <-msgs:
			if !ok {
				p.logger.ComponentWarn("gateway", "pubsub ws: message channel closed",
					zap.String("topic", wsClient.topic))
				_ = wsClient.writeControl(websocket.CloseMessage, []byte{}, time.Now().Add(5*time.Second))
				close(done)
				return
			}

			if err := wsClient.writeMessage(b); err != nil {
				close(done)
				return
			}

		case <-ticker.C:
			// Ping keepalive
			_ = wsClient.writeControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))

		case <-ctx.Done():
			close(done)
			return
		}
	}
}

// libp2pSubscriber handles subscribing to libp2p pubsub for cross-node messages
func (p *PubSubHandlers) libp2pSubscriber(ctx context.Context, topic string, msgs chan []byte, done chan struct{}) {
	h := func(_ string, data []byte) error {
		p.logger.ComponentInfo("gateway", "pubsub ws: received message from libp2p",
			zap.String("topic", topic),
			zap.Int("data_len", len(data)))

		select {
		case msgs <- data:
			p.logger.ComponentInfo("gateway", "pubsub ws: forwarded to client",
				zap.String("topic", topic),
				zap.String("source", "libp2p"))
			return nil
		default:
			// Drop if client is slow to avoid blocking network
			p.logger.ComponentWarn("gateway", "pubsub ws: client slow, dropping message",
				zap.String("topic", topic))
			return nil
		}
	}

	if err := p.client.PubSub().Subscribe(ctx, topic, h); err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: libp2p subscribe failed (will use local-only)",
			zap.String("topic", topic),
			zap.Error(err))
		return
	}
	p.logger.ComponentInfo("gateway", "pubsub ws: libp2p subscription established",
		zap.String("topic", topic))

	// Keep subscription alive until done
	<-done
	_ = p.client.PubSub().Unsubscribe(ctx, topic)
	p.logger.ComponentInfo("gateway", "pubsub ws: libp2p subscription closed",
		zap.String("topic", topic))
}

// readerLoop handles reading messages from the WebSocket client and publishing them
func (p *PubSubHandlers) readerLoop(ctx context.Context, wsClient *wsClient, topic string, done chan struct{}) {
	for {
		mt, data, err := wsClient.readMessage()
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
				p.logger.ComponentInfo("gateway", "pubsub ws: filtering out heartbeat ping")
				continue
			}
		}

		if err := p.client.PubSub().Publish(ctx, topic, data); err != nil {
			// Best-effort notify client
			_ = wsClient.conn.WriteMessage(websocket.TextMessage, []byte("publish_error"))
		}
	}
	<-done
}

// broadcastPresenceEvent broadcasts a presence join/leave event to all subscribers
func (p *PubSubHandlers) broadcastPresenceEvent(ns, topic, eventType, memberID string, meta map[string]interface{}, timestamp int64) {
	p.broadcastPresenceEventExcluding(ns, topic, eventType, memberID, meta, timestamp, "")
}

// broadcastPresenceEventExcluding broadcasts a presence event, optionally excluding a specific connection
func (p *PubSubHandlers) broadcastPresenceEventExcluding(ns, topic, eventType, memberID string, meta map[string]interface{}, timestamp int64, excludeConnID string) {
	event := map[string]interface{}{
		"type":      eventType,
		"member_id": memberID,
		"timestamp": timestamp,
	}
	if meta != nil {
		event["meta"] = meta
	}
	eventData, _ := json.Marshal(event)

	// Send to PubSub for remote delivery
	broadcastCtx := pubsub.WithNamespace(client.WithInternalAuth(context.Background()), ns)
	_ = p.client.PubSub().Publish(broadcastCtx, topic, eventData)

	// Also deliver directly to local subscribers on this gateway (non-blocking)
	topicKey := fmt.Sprintf("%s.%s", ns, topic)
	p.mu.RLock()
	localSubs := p.localSubscribers[topicKey]
	p.mu.RUnlock()

	for _, sub := range localSubs {
		// Skip the excluded connection if specified
		// Note: We don't have direct access to connID in localSubscriber, so we use a different approach
		// The excluded client already received its own event directly, so this is best-effort
		select {
		case sub.msgChan <- eventData:
		default:
			// Channel full, skip (client will see it via PubSub if they're still subscribed)
		}
	}
}
