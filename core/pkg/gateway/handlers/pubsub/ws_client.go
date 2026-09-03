package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkWSOrigin,
}

// checkWSOrigin validates WebSocket origins against the request's Host header.
// Non-browser clients (no Origin) are allowed. Browser clients must match the host.
//
// Bug #240/#249: when running on a NAMESPACE gateway, the request has been
// proxied via `handleNamespaceGatewayRequest` which rewrites r.Host to the
// backend target IP. The original public host is preserved in
// X-Forwarded-Host. Without this fix, RN-iOS / browser clients (which always
// send Origin) are rejected 403 because their Origin's public hostname will
// never match the proxied IP. Curl tests without Origin slip through,
// masking the bug. See namespace gateway log:
//
//	E routes WebSocket upgrade failed
//	  {"error": "websocket: request origin not allowed by Upgrader.CheckOrigin"}
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return false
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := parsed.Hostname()
	return originHost == host || strings.HasSuffix(originHost, "."+host)
}

// wsClient wraps a WebSocket connection with message handling
type wsClient struct {
	conn   *websocket.Conn
	topic  string
	logger *logging.ColoredLogger
}

// newWSClient creates a new WebSocket client wrapper
func newWSClient(conn *websocket.Conn, topic string, logger *logging.ColoredLogger) *wsClient {
	return &wsClient{
		conn:   conn,
		topic:  topic,
		logger: logger,
	}
}

// writeMessage sends a message to the WebSocket client with proper envelope formatting
func (c *wsClient) writeMessage(data []byte) error {
	c.logger.ComponentInfo("gateway", "pubsub ws: sending message to client",
		zap.String("topic", c.topic),
		zap.Int("data_len", len(data)))

	// Format message as JSON envelope with data (base64 encoded), timestamp, and topic
	// This matches the SDK's Message interface: {data: string, timestamp: number, topic: string}
	envelope := map[string]interface{}{
		"data":      base64.StdEncoding.EncodeToString(data),
		"timestamp": time.Now().UnixMilli(),
		"topic":     c.topic,
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		c.logger.ComponentWarn("gateway", "pubsub ws: failed to marshal envelope",
			zap.String("topic", c.topic),
			zap.Error(err))
		return err
	}

	c.logger.ComponentDebug("gateway", "pubsub ws: envelope created",
		zap.String("topic", c.topic),
		zap.Int("envelope_len", len(envelopeJSON)))

	c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := c.conn.WriteMessage(websocket.TextMessage, envelopeJSON); err != nil {
		c.logger.ComponentWarn("gateway", "pubsub ws: failed to write to websocket",
			zap.String("topic", c.topic),
			zap.Error(err))
		return err
	}

	c.logger.ComponentInfo("gateway", "pubsub ws: message sent successfully",
		zap.String("topic", c.topic))
	return nil
}

// writeControl sends a WebSocket control message
func (c *wsClient) writeControl(messageType int, data []byte, deadline time.Time) error {
	return c.conn.WriteControl(messageType, data, deadline)
}

// readMessage reads a message from the WebSocket client
func (c *wsClient) readMessage() (messageType int, data []byte, err error) {
	return c.conn.ReadMessage()
}

// close closes the WebSocket connection
func (c *wsClient) close() error {
	return c.conn.Close()
}
