package pubsub

import "context"

// Bus is the app pubsub surface: in-process GossipSub (the @index service)
// or a localhost HTTP client (gateways).
type Bus interface {
	Subscribe(ctx context.Context, topic string, handler MessageHandler) error
	// SubscribeHandle is Subscribe for a caller that must be able to leave on
	// its own: the returned function removes exactly this handler (idempotent)
	// and the underlying subscription ends with the last handler.
	SubscribeHandle(ctx context.Context, topic string, handler MessageHandler) (func() error, error)
	Publish(ctx context.Context, topic string, data []byte) error
	PublishBatch(ctx context.Context, msgs []TopicMessage, opts PublishBatchOptions) error
	PublishSame(ctx context.Context, topics []string, data []byte, opts PublishBatchOptions) error
	Unsubscribe(ctx context.Context, topic string) error
	ListTopics(ctx context.Context) ([]string, error)
	Close() error
}

var _ Bus = (*ClientAdapter)(nil)
