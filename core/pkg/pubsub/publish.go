package pubsub

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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

	// Wait briefly for mesh formation if no peers are in the mesh yet
	// GossipSub needs time to discover peers and form a mesh
	// With FloodPublish enabled, messages will be flooded to all connected peers
	// but we still want to give the mesh a chance to form for better delivery
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()

	// Check if we have peers in the mesh, wait up to 2 seconds for mesh formation
	meshFormed := false
	for i := 0; i < 20 && !meshFormed; i++ {
		peers := libp2pTopic.ListPeers()
		if len(peers) > 0 {
			meshFormed = true
			break // Mesh has formed, proceed with publish
		}
		select {
		case <-waitCtx.Done():
			meshFormed = true // Timeout, proceed anyway (FloodPublish will handle it)
		case <-time.After(100 * time.Millisecond):
			// Continue waiting
		}
	}

	// Publish message
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
