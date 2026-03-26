package triggers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

const (
	// triggerCacheDMap is the Olric DMap name for caching trigger lookups.
	triggerCacheDMap = "pubsub_triggers"

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
		logger:      logger,
	}
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

	// Build the event payload once for all invocations
	event := PubSubEvent{
		Topic:        topic,
		Data:         json.RawMessage(data),
		Namespace:    namespace,
		TriggerDepth: depth + 1,
		Timestamp:    time.Now().Unix(),
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		d.logger.Error("Failed to marshal PubSub event", zap.Error(err))
		return
	}

	d.logger.Debug("Dispatching PubSub triggers",
		zap.String("namespace", namespace),
		zap.String("topic", topic),
		zap.Int("matches", len(matches)),
		zap.Int("depth", depth),
	)

	for _, match := range matches {
		go d.invokeFunction(match, eventJSON)
	}
}

// InvalidateCache removes the cached trigger lookup for a namespace+topic.
// Call this when triggers are added or removed.
func (d *PubSubDispatcher) InvalidateCache(ctx context.Context, namespace, topic string) {
	if d.olricClient == nil {
		return
	}

	dm, err := d.olricClient.NewDMap(triggerCacheDMap)
	if err != nil {
		d.logger.Debug("Failed to get trigger cache DMap for invalidation", zap.Error(err))
		return
	}

	key := cacheKey(namespace, topic)
	if _, err := dm.Delete(ctx, key); err != nil {
		d.logger.Debug("Failed to invalidate trigger cache", zap.String("key", key), zap.Error(err))
	}
}

// getMatches returns the trigger matches for a topic+namespace, using Olric cache when available.
func (d *PubSubDispatcher) getMatches(ctx context.Context, namespace, topic string) ([]TriggerMatch, error) {
	// Try cache first
	if d.olricClient != nil {
		if matches, ok := d.getCached(ctx, namespace, topic); ok {
			return matches, nil
		}
	}

	// Cache miss — query database
	matches, err := d.store.GetByTopicAndNamespace(ctx, topic, namespace)
	if err != nil {
		return nil, err
	}

	// Populate cache
	if d.olricClient != nil && matches != nil {
		d.setCache(ctx, namespace, topic, matches)
	}

	return matches, nil
}

// getCached attempts to retrieve trigger matches from Olric cache.
func (d *PubSubDispatcher) getCached(ctx context.Context, namespace, topic string) ([]TriggerMatch, bool) {
	dm, err := d.olricClient.NewDMap(triggerCacheDMap)
	if err != nil {
		return nil, false
	}

	key := cacheKey(namespace, topic)
	result, err := dm.Get(ctx, key)
	if err != nil {
		return nil, false
	}

	data, err := result.Byte()
	if err != nil {
		return nil, false
	}

	var matches []TriggerMatch
	if err := json.Unmarshal(data, &matches); err != nil {
		return nil, false
	}

	return matches, true
}

// setCache stores trigger matches in Olric cache.
func (d *PubSubDispatcher) setCache(ctx context.Context, namespace, topic string, matches []TriggerMatch) {
	dm, err := d.olricClient.NewDMap(triggerCacheDMap)
	if err != nil {
		return
	}

	data, err := json.Marshal(matches)
	if err != nil {
		return
	}

	key := cacheKey(namespace, topic)
	_ = dm.Put(ctx, key, data)
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

// cacheKey returns the Olric cache key for a namespace+topic pair.
func cacheKey(namespace, topic string) string {
	return "triggers:" + namespace + ":" + topic
}
