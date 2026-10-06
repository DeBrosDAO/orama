package pubsub

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/wssession"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const (
	// defaultWSPingInterval is how often a subscriber socket is pinged.
	defaultWSPingInterval = 30 * time.Second
	// defaultWSPongWait is how long a subscriber socket may be silent, pongs
	// and client frames both counting, before it is dead: two missed pings and
	// slack. It must exceed the ping interval.
	defaultWSPongWait = 75 * time.Second
	// wsWriteTimeout bounds one data-frame write to a subscriber socket.
	wsWriteTimeout = 30 * time.Second
	// wsControlWriteTimeout bounds one ping or close frame.
	wsControlWriteTimeout = 5 * time.Second
	// defaultPublishTimeout bounds the hand-off of one publish, or one batch, to
	// the node's pubsub service.
	defaultPublishTimeout = 10 * time.Second
)

// PubSubHandlers handles all pubsub-related HTTP and WebSocket endpoints
type PubSubHandlers struct {
	client client.NetworkClient
	logger *logging.ColoredLogger

	presenceMembers map[string][]PresenceMember // topicKey -> members
	presenceMu      sync.RWMutex

	// pingInterval and pongWait drive subscriber-socket liveness (see the
	// defaults); tests shorten them.
	pingInterval time.Duration
	pongWait     time.Duration

	// publishTimeout bounds one publish's hand-off to the pubsub service; tests
	// shorten it.
	publishTimeout time.Duration

	// onPublish is called when a message is published, to dispatch PubSub triggers.
	// Set via SetOnPublish. May be nil if serverless triggers are not configured.
	onPublish func(ctx context.Context, namespace, topic string, data []byte)

	// sessions holds every token-authorized subscriber socket to its token's
	// expiry and revocation. It is the gateway's one registry, the one its
	// sweeper runs over.
	sessions *wssession.Registry
}

// SetOnPublish sets the callback invoked when messages are published.
// Used to wire PubSub trigger dispatch from the serverless engine.
func (p *PubSubHandlers) SetOnPublish(fn func(ctx context.Context, namespace, topic string, data []byte)) {
	p.onPublish = fn
}

// NewPubSubHandlers creates a new PubSubHandlers instance. sessions is the
// gateway's WebSocket session registry, which subscriber sockets register in so
// its sweeper can close the ones whose token expires or is revoked.
func NewPubSubHandlers(client client.NetworkClient, sessions *wssession.Registry, logger *logging.ColoredLogger) *PubSubHandlers {
	return &PubSubHandlers{
		client:          client,
		logger:          logger,
		presenceMembers: make(map[string][]PresenceMember),
		pingInterval:    defaultWSPingInterval,
		pongWait:        defaultWSPongWait,
		publishTimeout:  defaultPublishTimeout,
		sessions:        sessions,
	}
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
