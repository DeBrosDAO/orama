package pubsub

import (
	"context"
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Subscribe subscribes to a topic with a handler.
// Returns a HandlerID that can be used to unsubscribe this specific handler.
// Multiple handlers can subscribe to the same topic.
func (m *Manager) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	if m.pubsub == nil {
		return fmt.Errorf("pubsub not initialized")
	}

	// Determine namespace (allow per-call override via context)
	ns := m.namespace
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}
	namespacedTopic := fmt.Sprintf("%s.%s", ns, topic)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already have a subscription for this topic
	topicSub, exists := m.subscriptions[namespacedTopic]
	
	if exists {
		// Add handler to existing subscription
		handlerID := generateHandlerID()
		topicSub.mu.Lock()
		topicSub.handlers[handlerID] = handler
		topicSub.refCount++
		topicSub.mu.Unlock()
		return nil
	}

	// Create new subscription
	// Get or create topic
	libp2pTopic, err := m.getOrCreateTopic(namespacedTopic)
	if err != nil {
		return fmt.Errorf("failed to get topic: %w", err)
	}

	// Subscribe to topic
	sub, err := libp2pTopic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}

	// Create cancellable context for this subscription
	subCtx, cancel := context.WithCancel(context.Background())

	// Create topic subscription with initial handler
	handlerID := generateHandlerID()
	topicSub = &topicSubscription{
		sub:      sub,
		cancel:   cancel,
		handlers: map[HandlerID]MessageHandler{handlerID: handler},
		refCount: 1,
	}
	m.subscriptions[namespacedTopic] = topicSub

	// Start message handler goroutine (fan-out to all handlers)
	go func() {
		defer func() {
			sub.Cancel()
		}()

		for {
			select {
			case <-subCtx.Done():
				return
			default:
				msg, err := sub.Next(subCtx)
				if err != nil {
					if subCtx.Err() != nil {
						return // Context cancelled
					}
					continue
				}

				// Broadcast to all handlers
				topicSub.mu.RLock()
				handlers := make([]MessageHandler, 0, len(topicSub.handlers))
				for _, h := range topicSub.handlers {
					handlers = append(handlers, h)
				}
				topicSub.mu.RUnlock()

				// Call each handler (don't block on individual handler errors)
				for _, h := range handlers {
					if err := h(topic, msg.Data); err != nil {
						// Log error but continue processing other handlers
						continue
					}
				}
			}
		}
	}()

	return nil
}

// Unsubscribe decrements the subscription refcount for a topic.
// The subscription is only truly cancelled when refcount reaches zero.
// This allows multiple subscribers to the same topic.
func (m *Manager) Unsubscribe(ctx context.Context, topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Determine namespace (allow per-call override via context)
	ns := m.namespace
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}
	namespacedTopic := fmt.Sprintf("%s.%s", ns, topic)

	topicSub, exists := m.subscriptions[namespacedTopic]
	if !exists {
		return nil // Already unsubscribed
	}

	// Decrement ref count
	topicSub.mu.Lock()
	topicSub.refCount--
	shouldCancel := topicSub.refCount <= 0
	topicSub.mu.Unlock()

	// Only cancel and remove if no more subscribers
	if shouldCancel {
		topicSub.cancel()
		delete(m.subscriptions, namespacedTopic)
	}

	return nil
}

// ListTopics returns all subscribed topics
func (m *Manager) ListTopics(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var topics []string
	// Determine namespace (allow per-call override via context)
	ns := m.namespace
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}
	prefix := ns + "."

	for topic := range m.subscriptions {
		if len(topic) > len(prefix) && topic[:len(prefix)] == prefix {
			originalTopic := topic[len(prefix):]
			topics = append(topics, originalTopic)
		}
	}

	return topics, nil
}

// Close closes all subscriptions and topics
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Cancel all subscriptions
	for _, sub := range m.subscriptions {
		sub.cancel()
	}
	m.subscriptions = make(map[string]*topicSubscription)

	// Close all topics
	for _, topic := range m.topics {
		topic.Close()
	}
	m.topics = make(map[string]*pubsub.Topic)

	return nil
}
