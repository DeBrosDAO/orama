package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
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

	// Before the upgrade: a refusal after it is a WebSocket close frame the
	// client has to decode, where this is a plain 403.
	if !authorizeTopic(w, r, topic, gwauth.ActionRead) {
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

	// Use internal auth context when interacting with client to avoid circular auth requirements
	ctx := client.WithInternalAuth(r.Context())
	// Apply namespace isolation
	ctx = pubsub.WithNamespace(ctx, ns)

	// The one path a message reaches this socket by is the node's pubsub
	// service, which delivers to this gateway's subscribers whether the message
	// was published here or on another node. The subscription exists before the
	// upgrade, so nothing published after the socket opens can be missed, and a
	// service that cannot subscribe is a refusal the client sees.
	msgs := make(chan []byte, 128)
	stopSubscription, err := p.client.PubSub().SubscribeHandle(ctx, topic, p.forwardToSocket(topic, msgs))
	if err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: subscribe failed",
			zap.String("topic", topic),
			zap.String("namespace", ns),
			zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "pubsub subscribe failed")
		return
	}
	defer func() {
		if err := stopSubscription(); err != nil {
			p.logger.ComponentWarn("gateway", "pubsub ws: unsubscribe failed",
				zap.String("topic", topic),
				zap.Error(err))
		}
	}()

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: upgrade failed")
		return
	}
	defer conn.Close()

	// The subscription is only as good as the token that opened it: the
	// gateway's sweeper closes the socket once that token expires or is
	// revoked.
	claims, _ := r.Context().Value(ctxkeys.JWT).(*gwauth.JWTClaims)
	sock := p.sessions.Register(claims, subscriberCloser(conn))
	defer sock.Unregister()

	topicKey := fmt.Sprintf("%s.%s", ns, topic)

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

	p.logger.ComponentInfo("gateway", "pubsub ws: subscriber registered",
		zap.String("topic", topic),
		zap.String("namespace", ns))

	// Unregister on close
	defer func() {
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

		p.logger.ComponentInfo("gateway", "pubsub ws: subscriber unregistered",
			zap.String("topic", topic))
	}()

	done := make(chan struct{})
	wsClient := newWSClient(conn, topic, p.logger)
	go p.writerLoop(ctx, wsClient, msgs, done)

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

// forwardToSocket is the handler that feeds one socket's writer from the
// node's pubsub service.
func (p *PubSubHandlers) forwardToSocket(topic string, msgs chan []byte) client.MessageHandler {
	return func(_ string, data []byte) error {
		select {
		case msgs <- data:
		default:
			// Drop if client is slow to avoid blocking network
			p.logger.ComponentWarn("gateway", "pubsub ws: client slow, dropping message",
				zap.String("topic", topic))
		}
		return nil
	}
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

// broadcastPresenceEvent publishes a presence join/leave event on the topic.
// It reaches every subscriber, on this gateway and on others, by the same path
// as any other message.
func (p *PubSubHandlers) broadcastPresenceEvent(ns, topic, eventType, memberID string, meta map[string]interface{}, timestamp int64) {
	event := map[string]interface{}{
		"type":      eventType,
		"member_id": memberID,
		"timestamp": timestamp,
	}
	if meta != nil {
		event["meta"] = meta
	}
	eventData, err := json.Marshal(event)
	if err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: presence event not encodable",
			zap.String("topic", topic), zap.String("event", eventType), zap.Error(err))
		return
	}

	broadcastCtx := pubsub.WithNamespace(client.WithInternalAuth(context.Background()), ns)
	if err := p.client.PubSub().Publish(broadcastCtx, topic, eventData); err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: presence event not published",
			zap.String("topic", topic), zap.String("event", eventType), zap.Error(err))
	}
}
