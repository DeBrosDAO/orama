package serverless

import (
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Ensure WSManager implements WebSocketManager interface.
var _ WebSocketManager = (*WSManager)(nil)

// WSManager manages WebSocket connections for serverless functions.
// It handles connection registration, message routing, and topic subscriptions.
type WSManager struct {
	// connections maps client IDs to their WebSocket connections
	connections   map[string]*wsConnection
	connectionsMu sync.RWMutex

	// subscriptions maps topic names to sets of client IDs
	subscriptions   map[string]map[string]struct{}
	subscriptionsMu sync.RWMutex

	logger *zap.Logger
}

// wsConnection wraps a WebSocket connection with metadata.
type wsConnection struct {
	conn       WebSocketConn
	clientID   string
	topics     map[string]struct{} // Topics this client is subscribed to
	mu         sync.Mutex
}

// GorillaWSConn wraps a gorilla/websocket.Conn to implement WebSocketConn.
type GorillaWSConn struct {
	*websocket.Conn
}

// Ensure GorillaWSConn implements WebSocketConn.
var _ WebSocketConn = (*GorillaWSConn)(nil)

// WriteMessage writes a message to the WebSocket connection.
func (c *GorillaWSConn) WriteMessage(messageType int, data []byte) error {
	return c.Conn.WriteMessage(messageType, data)
}

// ReadMessage reads a message from the WebSocket connection.
func (c *GorillaWSConn) ReadMessage() (messageType int, p []byte, err error) {
	return c.Conn.ReadMessage()
}

// Close closes the WebSocket connection.
func (c *GorillaWSConn) Close() error {
	return c.Conn.Close()
}

// NewWSManager creates a new WebSocket manager.
func NewWSManager(logger *zap.Logger) *WSManager {
	return &WSManager{
		connections:   make(map[string]*wsConnection),
		subscriptions: make(map[string]map[string]struct{}),
		logger:        logger,
	}
}

// Register registers a new WebSocket connection.
func (m *WSManager) Register(clientID string, conn WebSocketConn) {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	// Close existing connection if any
	if existing, exists := m.connections[clientID]; exists {
		_ = existing.conn.Close()
		m.logger.Debug("Closed existing connection", zap.String("client_id", clientID))
	}

	m.connections[clientID] = &wsConnection{
		conn:     conn,
		clientID: clientID,
		topics:   make(map[string]struct{}),
	}

	m.logger.Debug("Registered WebSocket connection",
		zap.String("client_id", clientID),
		zap.Int("total_connections", len(m.connections)),
	)
}

// Unregister removes a WebSocket connection and its subscriptions.
func (m *WSManager) Unregister(clientID string) {
	m.connectionsMu.Lock()
	conn, exists := m.connections[clientID]
	if exists {
		delete(m.connections, clientID)
	}
	m.connectionsMu.Unlock()

	if !exists {
		return
	}

	// Remove from all subscriptions
	m.subscriptionsMu.Lock()
	for topic := range conn.topics {
		if clients, ok := m.subscriptions[topic]; ok {
			delete(clients, clientID)
			if len(clients) == 0 {
				delete(m.subscriptions, topic)
			}
		}
	}
	m.subscriptionsMu.Unlock()

	// Close the connection
	_ = conn.conn.Close()

	m.logger.Debug("Unregistered WebSocket connection",
		zap.String("client_id", clientID),
		zap.Int("remaining_connections", m.GetConnectionCount()),
	)
}

// Send sends data to a specific client.
func (m *WSManager) Send(clientID string, data []byte) error {
	m.connectionsMu.RLock()
	conn, exists := m.connections[clientID]
	m.connectionsMu.RUnlock()

	if !exists {
		return ErrWSClientNotFound
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if err := conn.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		m.logger.Warn("Failed to send WebSocket message",
			zap.String("client_id", clientID),
			zap.Error(err),
		)
		return err
	}

	return nil
}

// Broadcast sends data to all clients subscribed to a topic.
func (m *WSManager) Broadcast(topic string, data []byte) error {
	m.subscriptionsMu.RLock()
	clients, exists := m.subscriptions[topic]
	if !exists || len(clients) == 0 {
		m.subscriptionsMu.RUnlock()
		return nil // No subscribers, not an error
	}

	// Copy client IDs to avoid holding lock during send
	clientIDs := make([]string, 0, len(clients))
	for clientID := range clients {
		clientIDs = append(clientIDs, clientID)
	}
	m.subscriptionsMu.RUnlock()

	// Send to all subscribers
	var sendErrors int
	for _, clientID := range clientIDs {
		if err := m.Send(clientID, data); err != nil {
			sendErrors++
		}
	}

	m.logger.Debug("Broadcast message",
		zap.String("topic", topic),
		zap.Int("recipients", len(clientIDs)),
		zap.Int("errors", sendErrors),
	)

	return nil
}

// Subscribe adds a client to a topic.
func (m *WSManager) Subscribe(clientID, topic string) {
	// Add to connection's topic list
	m.connectionsMu.RLock()
	conn, exists := m.connections[clientID]
	m.connectionsMu.RUnlock()

	if !exists {
		return
	}

	conn.mu.Lock()
	conn.topics[topic] = struct{}{}
	conn.mu.Unlock()

	// Add to topic's client list
	m.subscriptionsMu.Lock()
	if m.subscriptions[topic] == nil {
		m.subscriptions[topic] = make(map[string]struct{})
	}
	m.subscriptions[topic][clientID] = struct{}{}
	m.subscriptionsMu.Unlock()

	m.logger.Debug("Client subscribed to topic",
		zap.String("client_id", clientID),
		zap.String("topic", topic),
	)
}

// Unsubscribe removes a client from a topic.
func (m *WSManager) Unsubscribe(clientID, topic string) {
	// Remove from connection's topic list
	m.connectionsMu.RLock()
	conn, exists := m.connections[clientID]
	m.connectionsMu.RUnlock()

	if exists {
		conn.mu.Lock()
		delete(conn.topics, topic)
		conn.mu.Unlock()
	}

	// Remove from topic's client list
	m.subscriptionsMu.Lock()
	if clients, ok := m.subscriptions[topic]; ok {
		delete(clients, clientID)
		if len(clients) == 0 {
			delete(m.subscriptions, topic)
		}
	}
	m.subscriptionsMu.Unlock()

	m.logger.Debug("Client unsubscribed from topic",
		zap.String("client_id", clientID),
		zap.String("topic", topic),
	)
}

// GetConnectionCount returns the number of active connections.
func (m *WSManager) GetConnectionCount() int {
	m.connectionsMu.RLock()
	defer m.connectionsMu.RUnlock()
	return len(m.connections)
}

// GetTopicSubscriberCount returns the number of subscribers for a topic.
func (m *WSManager) GetTopicSubscriberCount(topic string) int {
	m.subscriptionsMu.RLock()
	defer m.subscriptionsMu.RUnlock()
	if clients, exists := m.subscriptions[topic]; exists {
		return len(clients)
	}
	return 0
}

// GetClientTopics returns all topics a client is subscribed to.
func (m *WSManager) GetClientTopics(clientID string) []string {
	m.connectionsMu.RLock()
	conn, exists := m.connections[clientID]
	m.connectionsMu.RUnlock()

	if !exists {
		return nil
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	topics := make([]string, 0, len(conn.topics))
	for topic := range conn.topics {
		topics = append(topics, topic)
	}
	return topics
}

// IsConnected checks if a client is connected.
func (m *WSManager) IsConnected(clientID string) bool {
	m.connectionsMu.RLock()
	defer m.connectionsMu.RUnlock()
	_, exists := m.connections[clientID]
	return exists
}

// Close closes all connections and cleans up resources.
func (m *WSManager) Close() {
	m.connectionsMu.Lock()
	defer m.connectionsMu.Unlock()

	for clientID, conn := range m.connections {
		_ = conn.conn.Close()
		delete(m.connections, clientID)
	}

	m.subscriptionsMu.Lock()
	m.subscriptions = make(map[string]map[string]struct{})
	m.subscriptionsMu.Unlock()

	m.logger.Info("WebSocket manager closed")
}

// Stats returns statistics about the WebSocket manager.
type WSStats struct {
	ConnectionCount   int            `json:"connection_count"`
	TopicCount        int            `json:"topic_count"`
	SubscriptionCount int            `json:"subscription_count"`
	TopicStats        map[string]int `json:"topic_stats"` // topic -> subscriber count
}

// GetStats returns current statistics.
func (m *WSManager) GetStats() *WSStats {
	m.connectionsMu.RLock()
	connCount := len(m.connections)
	m.connectionsMu.RUnlock()

	m.subscriptionsMu.RLock()
	topicCount := len(m.subscriptions)
	topicStats := make(map[string]int, topicCount)
	totalSubs := 0
	for topic, clients := range m.subscriptions {
		topicStats[topic] = len(clients)
		totalSubs += len(clients)
	}
	m.subscriptionsMu.RUnlock()

	return &WSStats{
		ConnectionCount:   connCount,
		TopicCount:        topicCount,
		SubscriptionCount: totalSubs,
		TopicStats:        topicStats,
	}
}

