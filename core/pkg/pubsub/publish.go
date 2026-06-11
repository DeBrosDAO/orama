package pubsub

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// defaultBatchConcurrency is the default cap on in-flight publishes within a single batch.
const defaultBatchConcurrency = 32

// MaxBatchSize is the maximum number of messages allowed per PublishBatch call.
// The HTTP handler enforces this; the Manager itself does not, so internal callers
// (e.g. the SDK) can pass larger batches if they accept the responsibility.
const MaxBatchSize = 100

// TopicMessage is one entry in a batch publish.
type TopicMessage struct {
	Topic string
	Data  []byte
}

// PublishBatchOptions controls batch publish behavior.
type PublishBatchOptions struct {
	// BestEffort, if true, attempts every publish even when some fail and returns
	// a *BatchError summarizing per-topic failures. Default (false) is fail-fast:
	// the first failure cancels remaining in-flight publishes and returns that error.
	BestEffort bool

	// MaxConcurrency caps the number of in-flight publishes within this batch.
	// 0 means use defaultBatchConcurrency.
	MaxConcurrency int
}

// BatchError aggregates per-topic errors returned when PublishBatch is called
// with BestEffort=true and at least one publish failed.
type BatchError struct {
	Errors map[string]error // topic -> error
}

func (e *BatchError) Error() string {
	if len(e.Errors) == 0 {
		return "batch publish: no errors"
	}
	names := make([]string, 0, len(e.Errors))
	for t := range e.Errors {
		names = append(names, t)
	}
	return fmt.Sprintf("batch publish: %d topic(s) failed: %s", len(e.Errors), strings.Join(names, ", "))
}

// Publish publishes a message to a topic
func (m *Manager) Publish(ctx context.Context, topic string, data []byte) error {
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

	// Get or create topic
	libp2pTopic, err := m.getOrCreateTopic(namespacedTopic)
	if err != nil {
		return fmt.Errorf("failed to get topic for publishing: %w", err)
	}

	// Publish immediately — do NOT wait for gossipsub mesh formation.
	//
	// The router runs with FloodPublish enabled (pkg/node/libp2p.go and
	// pkg/client/client.go), so the message is sent directly to every
	// connected peer subscribed to the topic without needing a mesh, and a
	// same-gateway subscriber receives it via the local loopback regardless.
	//
	// A previous version polled ListPeers() for up to 2s here "to give the
	// mesh a chance to form." On the namespace-gateway topology most
	// application topics (per-conversation/wakeup) have no REMOTE mesh peers
	// — they're delivered to local WS clients — so the loop timed out the
	// full 2s on EVERY publish, making a 3-publish message-create cost ~6s
	// server-side (feat-6, the dominant realtime latency). FloodPublish makes
	// the wait redundant; removed.
	if err := libp2pTopic.Publish(ctx, data); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// PublishBatch publishes multiple messages in parallel, one per topic.
// Default behavior is fail-fast: the first publish error cancels remaining work
// and is returned. If opts.BestEffort is set, every publish is attempted and a
// *BatchError is returned if any failed.
//
// Concurrency is bounded by opts.MaxConcurrency (default 32).
// Empty msgs slice is a no-op and returns nil.
func (m *Manager) PublishBatch(ctx context.Context, msgs []TopicMessage, opts PublishBatchOptions) error {
	if len(msgs) == 0 {
		return nil
	}
	if m.pubsub == nil {
		return fmt.Errorf("pubsub not initialized")
	}

	maxConc := opts.MaxConcurrency
	if maxConc <= 0 {
		maxConc = defaultBatchConcurrency
	}
	if maxConc > len(msgs) {
		maxConc = len(msgs)
	}
	sem := make(chan struct{}, maxConc)

	if !opts.BestEffort {
		g, gctx := errgroup.WithContext(ctx)
		for _, msg := range msgs {
			msg := msg
			g.Go(func() error {
				select {
				case sem <- struct{}{}:
				case <-gctx.Done():
					return gctx.Err()
				}
				defer func() { <-sem }()
				return m.Publish(gctx, msg.Topic, msg.Data)
			})
		}
		return g.Wait()
	}

	// Best-effort path: attempt every publish, collect per-topic errors.
	var (
		wg     sync.WaitGroup
		errMu  sync.Mutex
		errMap = map[string]error{}
	)
	for _, msg := range msgs {
		msg := msg
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errMu.Lock()
				errMap[msg.Topic] = ctx.Err()
				errMu.Unlock()
				return
			}
			defer func() { <-sem }()
			if err := m.Publish(ctx, msg.Topic, msg.Data); err != nil {
				errMu.Lock()
				errMap[msg.Topic] = err
				errMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errMap) > 0 {
		return &BatchError{Errors: errMap}
	}
	return nil
}

// PublishSame is a convenience wrapper that sends the same payload to every topic.
func (m *Manager) PublishSame(ctx context.Context, topics []string, data []byte, opts PublishBatchOptions) error {
	if len(topics) == 0 {
		return nil
	}
	msgs := make([]TopicMessage, len(topics))
	for i, t := range topics {
		msgs[i] = TopicMessage{Topic: t, Data: data}
	}
	return m.PublishBatch(ctx, msgs, opts)
}
