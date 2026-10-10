package pubsub

import (
	"context"
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Subscribe subscribes to a topic with a handler. Multiple handlers can
// subscribe to the same topic; Unsubscribe is ref-counted per topic.
func (m *Manager) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	_, err := m.SubscribeHandle(ctx, topic, handler)
	return err
}

// SubscribeHandle subscribes handler to a topic and returns the function that
// removes exactly that handler again. The underlying subscription ends when the
// last handler is removed. The returned function is idempotent.
func (m *Manager) SubscribeHandle(ctx context.Context, topic string, handler MessageHandler) (func() error, error) {
	if m.pubsub == nil {
		return nil, fmt.Errorf("pubsub not initialized")
	}

	// Determine namespace (allow per-call override via context)
	ns := m.namespace
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}
	namespacedTopic := fmt.Sprintf("%s.%s", ns, topic)

	// Fast path: we already have a subscription for this topic. The manager
	// lock is held while the handler is added so the last remover cannot end
	// the subscription in between.
	m.mu.RLock()
	if existing := m.subscriptions[namespacedTopic]; existing != nil {
		id := existing.add(handler)
		m.mu.RUnlock()
		return m.remover(namespacedTopic, existing, id), nil
	}
	m.mu.RUnlock()

	// Create the underlying libp2p subscription without holding the manager lock
	// to avoid re-entrant lock attempts
	libp2pTopic, err := m.getOrCreateTopic(namespacedTopic)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}

	// Subscribe to topic
	sub, err := libp2pTopic.Subscribe()
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to topic: %w", err)
	}

	// Create cancellable context for this subscription
	subCtx, cancel := context.WithCancel(context.Background())

	// Create topic subscription with initial handler
	newSub := &topicSubscription{
		sub:      sub,
		cancel:   cancel,
		handlers: make(map[HandlerID]MessageHandler),
	}
	handlerID := newSub.add(handler)

	// Install the subscription (or merge if another goroutine beat us)
	m.mu.Lock()
	if existing := m.subscriptions[namespacedTopic]; existing != nil {
		// Another goroutine already created a subscription while we were working
		// Clean up our resources and add to theirs
		cancel()
		sub.Cancel()
		id := existing.add(handler)
		m.mu.Unlock()
		return m.remover(namespacedTopic, existing, id), nil
	}
	m.subscriptions[namespacedTopic] = newSub
	m.mu.Unlock()

	// Announce topic interest to help with peer discovery
	go m.announceTopicInterest(namespacedTopic)

	// Start message handler goroutine (fan-out to all handlers)
	go func(ts *topicSubscription) {
		defer ts.sub.Cancel()

		for {
			select {
			case <-subCtx.Done():
				return
			default:
				msg, err := ts.sub.Next(subCtx)
				if err != nil {
					if subCtx.Err() != nil {
						return // Context cancelled
					}
					continue
				}

				// Filter out internal discovery messages
				if string(msg.Data) == "PEER_DISCOVERY_PING" {
					continue
				}

				// Broadcast to all handlers
				ts.mu.RLock()
				handlers := make([]MessageHandler, 0, len(ts.handlers))
				for _, h := range ts.handlers {
					handlers = append(handlers, h)
				}
				ts.mu.RUnlock()

				// Call each handler (don't block on individual handler errors)
				for _, h := range handlers {
					if err := h(topic, msg.Data); err != nil {
						// Log error but continue processing other handlers
						continue
					}
				}
			}
		}
	}(newSub)

	return m.remover(namespacedTopic, newSub, handlerID), nil
}

// add registers handler on the subscription and returns its id.
func (ts *topicSubscription) add(handler MessageHandler) HandlerID {
	id := generateHandlerID()
	ts.mu.Lock()
	ts.handlers[id] = handler
	ts.refCount++
	ts.mu.Unlock()
	return id
}

// remover returns the function that removes handler id from ts and ends the
// subscription when it was the last one.
func (m *Manager) remover(namespacedTopic string, ts *topicSubscription, id HandlerID) func() error {
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		ts.mu.Lock()
		_, held := ts.handlers[id]
		delete(ts.handlers, id)
		if held {
			ts.refCount--
		}
		last := ts.refCount <= 0
		ts.mu.Unlock()
		if held && last && m.subscriptions[namespacedTopic] == ts {
			ts.cancel()
			delete(m.subscriptions, namespacedTopic)
		}
		return nil
	}
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
