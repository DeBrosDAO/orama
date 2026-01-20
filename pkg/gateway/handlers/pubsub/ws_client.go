package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// For early development we accept any origin; tighten later.
	CheckOrigin: func(r *http.Request) bool { return true },
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
