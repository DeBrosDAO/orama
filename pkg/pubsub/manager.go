package pubsub

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Manager handles pub/sub operations
type Manager struct {
    pubsub        *pubsub.PubSub
    topics        map[string]*pubsub.Topic
    subscriptions map[string]*topicSubscription
    namespace     string
    mu            sync.RWMutex
}

// topicSubscription holds multiple handlers for a single topic
type topicSubscription struct {
    sub       *pubsub.Subscription
    cancel    func()
    handlers  map[HandlerID]MessageHandler
    refCount  int  // Number of active subscriptions
    mu        sync.RWMutex
}

// NewManager creates a new pubsub manager
func NewManager(ps *pubsub.PubSub, namespace string) *Manager {
    return &Manager {
        pubsub:        ps,
        topics:        make(map[string]*pubsub.Topic),
        subscriptions: make(map[string]*topicSubscription),
        namespace:     namespace,
    }
}

// generateHandlerID creates a unique handler ID
func generateHandlerID() HandlerID {
    b := make([]byte, 8)
    rand.Read(b)
    return HandlerID(hex.EncodeToString(b))
}
