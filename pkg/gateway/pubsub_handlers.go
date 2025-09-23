package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"

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
	// Use internal auth context when interacting with client to avoid circular auth requirements
	ctx := client.WithInternalAuth(r.Context())
	// Subscribe to the topic; push data into msgs with simple per-connection de-dup
	recent := make(map[string]time.Time)
	h := func(_ string, data []byte) error {
		// Drop duplicates seen in the last 2 seconds
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:])
		if t, ok := recent[key]; ok && time.Since(t) < 2*time.Second {
			return nil
		}
		recent[key] = time.Now()
		select {
		case msgs <- data:
			return nil
		default:
			// Drop if client is slow to avoid blocking network
			return nil
		}
	}
	if err := g.client.PubSub().Subscribe(ctx, topic, h); err != nil {
		g.logger.ComponentWarn("gateway", "pubsub ws: subscribe failed")
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = g.client.PubSub().Unsubscribe(ctx, topic) }()

	// no extra fan-out; rely on libp2p subscription

	// Writer loop
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case b, ok := <-msgs:
				if !ok {
					_ = conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(5*time.Second))
					close(done)
					return
				}
				conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
					close(done)
					return
				}
			case <-ticker.C:
				// Ping keepalive
				_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			case <-ctx.Done():
				close(done)
				return
			}
		}
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
	if err := g.client.PubSub().Publish(client.WithInternalAuth(r.Context()), body.Topic, data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// rely on libp2p to deliver to WS subscribers
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
	all, err := g.client.PubSub().ListTopics(client.WithInternalAuth(r.Context()))
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
