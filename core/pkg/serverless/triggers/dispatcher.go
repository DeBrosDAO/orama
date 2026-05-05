package triggers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/aggregator"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

const (
	// maxTriggerDepth prevents infinite loops when triggered functions publish
	// back to the same topic via the HTTP API.
	maxTriggerDepth = 5

	// dispatchTimeout is the timeout for each triggered function invocation.
	dispatchTimeout = 60 * time.Second
)

// PubSubEvent is the JSON payload sent to functions triggered by PubSub messages.
type PubSubEvent struct {
	Topic        string          `json:"topic"`
	Data         json.RawMessage `json:"data"`
	Namespace    string          `json:"namespace"`
	TriggerDepth int             `json:"trigger_depth"`
	Timestamp    int64           `json:"timestamp"`
}

// PubSubDispatcher looks up triggers for a topic+namespace and asynchronously
// invokes matching serverless functions.
type PubSubDispatcher struct {
	store       *PubSubTriggerStore
	invoker     *serverless.Invoker
	olricClient olriclib.Client // may be nil (cache disabled)
	aggregator  *aggregator.Aggregator
	logger      *zap.Logger
}

// NewPubSubDispatcher creates a new PubSub trigger dispatcher.
func NewPubSubDispatcher(
	store *PubSubTriggerStore,
	invoker *serverless.Invoker,
	olricClient olriclib.Client,
	logger *zap.Logger,
) *PubSubDispatcher {
	return &PubSubDispatcher{
		store:       store,
		invoker:     invoker,
		olricClient: olricClient,
		aggregator:  aggregator.New(logger, dispatchTimeout),
		logger:      logger,
	}
}

// Aggregator exposes the underlying aggregator so callers (gateway lifecycle)
// can flush pending buffers on shutdown.
func (d *PubSubDispatcher) Aggregator() *aggregator.Aggregator {
	return d.aggregator
}

// Dispatch looks up all triggers registered for the given topic+namespace and
// invokes matching functions asynchronously. Each invocation runs in its own
// goroutine and does not block the caller.
func (d *PubSubDispatcher) Dispatch(ctx context.Context, namespace, topic string, data []byte, depth int) {
	if depth >= maxTriggerDepth {
		d.logger.Warn("PubSub trigger depth limit reached, skipping dispatch",
			zap.String("namespace", namespace),
			zap.String("topic", topic),
			zap.Int("depth", depth),
		)
		return
	}

	matches, err := d.getMatches(ctx, namespace, topic)
	if err != nil {
		d.logger.Error("Failed to look up PubSub triggers",
			zap.String("namespace", namespace),
			zap.String("topic", topic),
			zap.Error(err),
		)
		return
	}

	if len(matches) == 0 {
		return
	}

	// Build the per-event payload once for non-aggregating dispatches.
	event := PubSubEvent{
		Topic:        topic,
		Data:         json.RawMessage(data),
		Namespace:    namespace,
		TriggerDepth: depth + 1,
		Timestamp:    time.Now().Unix(),
	}

	d.logger.Debug("Dispatching PubSub triggers",
		zap.String("namespace", namespace),
		zap.String("topic", topic),
		zap.Int("matches", len(matches)),
		zap.Int("depth", depth),
	)

	var (
		eventJSON []byte
		marshalErr error
	)

	for _, match := range matches {
		if match.AggregationWindowMs > 0 {
			d.bufferEvent(match, event)
			continue
		}
		// Lazily marshal — non-aggregating triggers need eventJSON.
		if eventJSON == nil && marshalErr == nil {
			eventJSON, marshalErr = json.Marshal(event)
			if marshalErr != nil {
				d.logger.Error("Failed to marshal PubSub event", zap.Error(marshalErr))
				continue
			}
		}
		if marshalErr != nil {
			continue
		}
		go d.invokeFunction(match, eventJSON)
	}
}

// bufferEvent routes an event through the aggregator. The flush callback
// invokes the function with the batched payload.
func (d *PubSubDispatcher) bufferEvent(match TriggerMatch, event PubSubEvent) {
	d.aggregator.Buffer(aggregator.BufferRequest{
		Namespace:    match.Namespace,
		FunctionID:   match.FunctionID,
		TriggerID:    match.TriggerID,
		WindowMs:     match.AggregationWindowMs,
		MaxBatchSize: match.AggregationMaxBatchSize,
		Event: aggregator.Event{
			Topic:        event.Topic,
			Data:         event.Data,
			Namespace:    event.Namespace,
			TriggerDepth: event.TriggerDepth,
			Timestamp:    event.Timestamp,
		},
		FlushFn: func(ctx context.Context, payload []byte) {
			req := &serverless.InvokeRequest{
				Namespace:    match.Namespace,
				FunctionName: match.FunctionName,
				Input:        payload,
				TriggerType:  serverless.TriggerTypePubSub,
			}
			if _, err := d.invoker.Invoke(ctx, req); err != nil {
				d.logger.Warn("Aggregated PubSub invocation failed",
					zap.String("function", match.FunctionName),
					zap.String("trigger_id", match.TriggerID),
					zap.Error(err),
				)
			}
		},
	})
}

// InvalidateCache is now a no-op — the dispatcher no longer caches lookups.
// Kept on the type so callers who used it still compile.
func (d *PubSubDispatcher) InvalidateCache(ctx context.Context, namespace, topic string) {}

// getMatches returns the trigger matches for a topic+namespace.
//
// Caching note: an earlier revision cached results in Olric keyed by
// (namespace, topic). With wildcard triggers the cache becomes
// inconsistent — a single trigger Add/Remove invalidates an unbounded
// number of resolved-topic keys. The cache was removed; re-introducing
// it requires a generation-counter (or TTL) scheme that handles
// wildcard pattern changes.
func (d *PubSubDispatcher) getMatches(ctx context.Context, namespace, topic string) ([]TriggerMatch, error) {
	return d.store.GetByTopicAndNamespace(ctx, topic, namespace)
}


// invokeFunction invokes a single function for a trigger match.
func (d *PubSubDispatcher) invokeFunction(match TriggerMatch, eventJSON []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
	defer cancel()

	req := &serverless.InvokeRequest{
		Namespace:    match.Namespace,
		FunctionName: match.FunctionName,
		Input:        eventJSON,
		TriggerType:  serverless.TriggerTypePubSub,
	}

	resp, err := d.invoker.Invoke(ctx, req)
	if err != nil {
		d.logger.Warn("PubSub trigger invocation failed",
			zap.String("function", match.FunctionName),
			zap.String("namespace", match.Namespace),
			zap.String("topic", match.Topic),
			zap.String("trigger_id", match.TriggerID),
			zap.Error(err),
		)
		return
	}

	d.logger.Debug("PubSub trigger invocation completed",
		zap.String("function", match.FunctionName),
		zap.String("topic", match.Topic),
		zap.String("status", string(resp.Status)),
		zap.Int64("duration_ms", resp.DurationMS),
	)
}

