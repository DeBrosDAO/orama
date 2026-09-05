package pubsub

import (
	"context"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"go.uber.org/zap"
)

// ClientAdapter adapts the pubsub Manager to work with the existing client interface
type ClientAdapter struct {
	manager *Manager
}

// NewClientAdapter creates a new adapter for the pubsub manager
func NewClientAdapter(ps *pubsub.PubSub, namespace string, logger *zap.Logger) *ClientAdapter {
	return &ClientAdapter{
		manager: NewManager(ps, namespace, logger),
	}
}

// Subscribe subscribes to a topic
func (a *ClientAdapter) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	return a.manager.Subscribe(ctx, topic, handler)
}

// Publish publishes a message to a topic
func (a *ClientAdapter) Publish(ctx context.Context, topic string, data []byte) error {
	return a.manager.Publish(ctx, topic, data)
}

// PublishBatch publishes multiple messages in parallel.
// See Manager.PublishBatch for semantics.
func (a *ClientAdapter) PublishBatch(ctx context.Context, msgs []TopicMessage, opts PublishBatchOptions) error {
	return a.manager.PublishBatch(ctx, msgs, opts)
}

// PublishSame sends the same payload to every topic in parallel.
// See Manager.PublishSame for semantics.
func (a *ClientAdapter) PublishSame(ctx context.Context, topics []string, data []byte, opts PublishBatchOptions) error {
	return a.manager.PublishSame(ctx, topics, data, opts)
}

// Unsubscribe unsubscribes from a topic
func (a *ClientAdapter) Unsubscribe(ctx context.Context, topic string) error {
	return a.manager.Unsubscribe(ctx, topic)
}

// ListTopics returns all subscribed topics
func (a *ClientAdapter) ListTopics(ctx context.Context) ([]string, error) {
	return a.manager.ListTopics(ctx)
}

// Close closes all subscriptions and topics
func (a *ClientAdapter) Close() error {
	return a.manager.Close()
}
