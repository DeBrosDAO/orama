package client

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// pubSubBridge bridges between our PubSubClient interface and the pubsub package
type pubSubBridge struct {
	client  *Client
	adapter *pubsub.ClientAdapter
}

func (p *pubSubBridge) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	if err := p.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}
	// Convert our MessageHandler to the pubsub package MessageHandler
	pubsubHandler := func(topic string, data []byte) error {
		return handler(topic, data)
	}
	return p.adapter.Subscribe(ctx, topic, pubsubHandler)
}

func (p *pubSubBridge) Publish(ctx context.Context, topic string, data []byte) error {
	if err := p.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}
	return p.adapter.Publish(ctx, topic, data)
}

func (p *pubSubBridge) Unsubscribe(ctx context.Context, topic string) error {
	if err := p.client.requireAccess(ctx); err != nil {
		return fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}
	return p.adapter.Unsubscribe(ctx, topic)
}

func (p *pubSubBridge) ListTopics(ctx context.Context) ([]string, error) {
	if err := p.client.requireAccess(ctx); err != nil {
		return nil, fmt.Errorf("authentication required: %w - run CLI commands to authenticate automatically", err)
	}
	return p.adapter.ListTopics(ctx)
}
