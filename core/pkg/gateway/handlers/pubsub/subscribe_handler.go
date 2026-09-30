package pubsub

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
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

	presence := p.joinPresence(ns, topic, enablePresence, memberID, memberMeta)
	defer p.leavePresence(presence)

	p.logger.ComponentInfo("gateway", "pubsub ws: subscriber registered",
		zap.String("topic", topic),
		zap.String("namespace", ns))
	defer p.logger.ComponentInfo("gateway", "pubsub ws: subscriber unregistered",
		zap.String("topic", topic))

	// The connection lives exactly as long as its reader: a read error, a
	// close from the client, a read deadline missed by a peer that stopped
	// answering pings, or the sweeper closing the socket all end it. Then the
	// writer is stopped and waited for, and the deferred leave and unsubscribe
	// run whichever way it ended.
	wsCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	wsClient := newWSClient(conn, topic, p.logger)
	go p.writerLoop(wsCtx, wsClient, msgs, done)

	// Reader loop: treat any client message as publish to the same topic
	p.readerLoop(wsCtx, wsClient, topic)
	cancel()
	<-done
}

// writerLoop handles writing messages from the msgs channel to the WebSocket client
// It closes the connection when it stops, which ends the reader, and closes
// done.
func (p *PubSubHandlers) writerLoop(ctx context.Context, wsClient *wsClient, msgs chan []byte, done chan struct{}) {
	p.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine started",
		zap.String("topic", wsClient.topic))
	defer close(done)
	defer func() { _ = wsClient.close() }()
	defer p.logger.ComponentInfo("gateway", "pubsub ws: writer goroutine exiting",
		zap.String("topic", wsClient.topic))

	ticker := time.NewTicker(p.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case b, ok := <-msgs:
			if !ok {
				p.logger.ComponentWarn("gateway", "pubsub ws: message channel closed",
					zap.String("topic", wsClient.topic))
				_ = wsClient.writeControl(websocket.CloseMessage, []byte{}, time.Now().Add(5*time.Second))
				return
			}

			if err := wsClient.writeMessage(b); err != nil {
				return
			}

		case <-ticker.C:
			// Ping keepalive: the peer's pong is what keeps its read deadline
			// from expiring. A ping that cannot be written is a dead connection.
			if err := wsClient.writeControl(websocket.PingMessage, []byte("ping"), time.Now().Add(wsControlWriteTimeout)); err != nil {
				p.logger.ComponentWarn("gateway", "pubsub ws: ping failed, closing connection",
					zap.String("topic", wsClient.topic),
					zap.Error(err))
				return
			}

		case <-ctx.Done():
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

// readerLoop reads messages from the WebSocket client and publishes them. It
// returns when the connection ends, for whatever reason: a close or read error,
// or no frame, pong included, within the pong wait.
func (p *PubSubHandlers) readerLoop(ctx context.Context, wsClient *wsClient, topic string) {
	conn := wsClient.conn
	refreshDeadline := func() error { return conn.SetReadDeadline(time.Now().Add(p.pongWait)) }
	if err := refreshDeadline(); err != nil {
		p.logger.ComponentWarn("gateway", "pubsub ws: cannot set the read deadline, closing connection",
			zap.String("topic", topic), zap.Error(err))
		return
	}
	conn.SetPongHandler(func(string) error { return refreshDeadline() })

	for {
		mt, data, err := wsClient.readMessage()
		if err != nil {
			p.logger.ComponentInfo("gateway", "pubsub ws: read ended, closing connection",
				zap.String("topic", topic), zap.Error(err))
			return
		}
		if err := refreshDeadline(); err != nil {
			p.logger.ComponentWarn("gateway", "pubsub ws: cannot set the read deadline, closing connection",
				zap.String("topic", topic), zap.Error(err))
			return
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
			p.logger.ComponentWarn("gateway", "pubsub ws: publish from socket failed",
				zap.String("topic", topic), zap.Error(err))
			// Best-effort notify client
			_ = wsClient.writeText([]byte("publish_error"))
		}
	}
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
