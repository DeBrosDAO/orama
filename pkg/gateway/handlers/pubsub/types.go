package pubsub

import (
	"net/http"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// PubSubHandlers handles all pubsub-related HTTP and WebSocket endpoints
type PubSubHandlers struct {
	client client.NetworkClient
	logger *logging.ColoredLogger

	// Local pub/sub bypass for same-gateway subscribers
	localSubscribers map[string][]*localSubscriber // topic+namespace -> subscribers
	presenceMembers  map[string][]PresenceMember   // topicKey -> members
	mu               sync.RWMutex
	presenceMu       sync.RWMutex
}

// NewPubSubHandlers creates a new PubSubHandlers instance
func NewPubSubHandlers(client client.NetworkClient, logger *logging.ColoredLogger) *PubSubHandlers {
	return &PubSubHandlers{
		client:           client,
		logger:           logger,
		localSubscribers: make(map[string][]*localSubscriber),
		presenceMembers:  make(map[string][]PresenceMember),
	}
}

// localSubscriber represents a local websocket subscriber on this gateway node
type localSubscriber struct {
	msgChan   chan []byte
	namespace string
}

// PresenceMember represents a member in a topic's presence list
type PresenceMember struct {
	MemberID string                 `json:"member_id"`
	JoinedAt int64                  `json:"joined_at"` // Unix timestamp
	Meta     map[string]interface{} `json:"meta,omitempty"`
	ConnID   string                 `json:"-"` // Internal: for tracking which connection
}

// PublishRequest represents the request body for publishing a message
type PublishRequest struct {
	Topic   string `json:"topic"`
	DataB64 string `json:"data_base64"`
}

// getLocalSubscribers returns local subscribers for a given topic and namespace
func (p *PubSubHandlers) getLocalSubscribers(topic, namespace string) []*localSubscriber {
	topicKey := namespace + "." + topic
	if subs, ok := p.localSubscribers[topicKey]; ok {
		return subs
	}
	return nil
}

// resolveNamespaceFromRequest gets namespace from context set by auth middleware
func resolveNamespaceFromRequest(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// namespacePrefix returns the namespace prefix for a given namespace
func namespacePrefix(ns string) string {
	return "ns::" + ns + "::"
}

// namespacedTopic returns the fully namespaced topic string
func namespacedTopic(ns, topic string) string {
	return namespacePrefix(ns) + topic
}
