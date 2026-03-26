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