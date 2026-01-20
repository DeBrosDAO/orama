package contracts

import (
	"context"
)

// PubSubService defines the interface for publish-subscribe messaging.
// Provides topic-based message broadcasting with support for multiple handlers.
type PubSubService interface {
	// Publish sends a message to all subscribers of a topic.
	// The message is delivered asynchronously to all registered handlers.
	Publish(ctx context.Context, topic string, data []byte) error

	// Subscribe registers a handler for messages on a topic.
	// Multiple handlers can be registered for the same topic.
	// Returns a HandlerID that can be used to unsubscribe.
	Subscribe(ctx context.Context, topic string, handler MessageHandler) (HandlerID, error)

	// Unsubscribe removes a specific handler from a topic.
	// The subscription is reference-counted per topic.
	Unsubscribe(ctx context.Context, topic string, handlerID HandlerID) error

	// Close gracefully shuts down the pubsub service and releases resources.
	Close(ctx context.Context) error
}

// MessageHandler processes messages received from a subscribed topic.
// Each handler receives the topic name and message data.
// Multiple handlers for the same topic each receive a copy of the message.
// Handlers should return an error only for critical failures.
type MessageHandler func(topic string, data []byte) error

// HandlerID uniquely identifies a subscription handler.
// Each Subscribe call generates a new HandlerID, allowing multiple
// independent subscriptions to the same topic.
type HandlerID string
