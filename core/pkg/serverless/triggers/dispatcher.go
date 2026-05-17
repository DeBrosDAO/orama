package triggers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
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

	// dispatcherRefreshInterval is the safety-net cadence for re-syncing
	// libp2p subscriptions against the trigger store. Trigger add/remove
	// calls Refresh synchronously; this catches anything missed (e.g. an
	// add that happened on a different gateway node, or a deploy-time
	// auto-register where the Refresh hook wasn't wired).
	dispatcherRefreshInterval = 60 * time.Second
)

// PubSubEvent is the JSON payload sent to functions triggered by PubSub messages.
type PubSubEvent struct {
	Topic        string          `json:"topic"`
	Data         json.RawMessage `json:"data"`
	Namespace    string          `json:"namespace"`
	TriggerDepth int             `json:"trigger_depth"`
	Timestamp    int64           `json:"timestamp"`
}

// dispatcherPubSub is the subset of *pubsub.ClientAdapter the dispatcher
// needs for libp2p subscribe/unsubscribe. Defined as an interface so the
// dispatcher's Start/Refresh logic is unit-testable without standing up
// a real libp2p host.
type dispatcherPubSub interface {
	Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler) error
	Unsubscribe(ctx context.Context, topic string) error
}

// topicLister is the subset of *PubSubTriggerStore the dispatcher's
// Refresh path needs. Defined as an interface so tests can inject a
// canned trigger set and exercise the real Refresh code path (rather
// than re-simulating it inline, which would let regressions slip).
type topicLister interface {
	ListDistinctTopicPatterns(ctx context.Context) ([]DistinctTopicSubscription, error)
}

// PubSubDispatcher looks up triggers for a topic+namespace and asynchronously
// invokes matching serverless functions. Subscribes to libp2p pubsub for
// every literal trigger pattern so WASM `oh.PubSubPublish` calls reach
// trigger handlers (bugboard #282 — before this, the dispatcher only fired
// when the HTTP `/v1/pubsub/publish` endpoint was hit, so every internal
// WASM publish silently dropped every subscriber).
//
// KNOWN LIMITATIONS (tracked as follow-ups, NOT in scope for #282):
//
//  1. Cross-namespace publish surface: any peer in the cluster's libp2p
//     mesh can publish to a tenant's namespaced topic (`<ns>.<topic>`)
//     and drive a trigger invocation. The libp2p mesh has no per-topic
//     ACL, so a compromised namespace gateway gains the ability to fire
//     other tenants' handlers. Pre-fix this attack failed because the
//     dispatcher never subscribed at all. Mitigation requires either
//     signed-envelope verification at dispatch time or a per-namespace
//     swarm key (PSK) separating each tenant's pubsub mesh. Documented
//     in the security audit on bugboard #282; track as a separate ticket.
//
//  2. Trigger-depth loops via libp2p round-trip: maxTriggerDepth=5 is
//     embedded in the PubSubEvent payload, but a triggered function that
//     publishes back through `oh.PubSubPublish` re-enters this dispatcher
//     via libp2p Subscribe with depth=0 (the depth field lives in the
//     OUR envelope, not in the libp2p wire format). Loops are bounded
//     only by the per-invocation timeout. WASM functions MUST self-limit
//     by reading `event.trigger_depth` from their input. A future fix
//     would encode depth in a libp2p header the dispatcher reads back.
//
//  3. Wildcard patterns are not subscribed via libp2p (libp2p has no
//     wildcard subscribe). Wildcard triggers only fire from HTTP-publish
//     events via the legacy Dispatch hook, NOT from WASM publishes.
//     Documented in Refresh below.
type PubSubDispatcher struct {
	store       *PubSubTriggerStore
	invoker     *serverless.Invoker
	olricClient olriclib.Client // may be nil (cache disabled)
	aggregator  *aggregator.Aggregator
	logger      *zap.Logger

	// topicLister is the interface Refresh uses to enumerate desired
	// subscriptions. Defaults to the concrete store but is overridable
	// in tests so the real Refresh code path can be exercised against
	// a canned trigger set. Set in NewPubSubDispatcher; only swapped
	// by tests via the helper in dispatcher_refresh_test.go.
	topicLister topicLister

	// pubsub is the libp2p-pubsub layer the dispatcher subscribes to so
	// it can react to events published from WASM `oh.PubSubPublish` calls
	// (which bypass the HTTP publish handler). nil disables the
	// auto-subscribe behavior — kept nullable for tests that exercise
	// only the Dispatch path.
	pubsub dispatcherPubSub

	// subMu guards subscribedKeys against concurrent Refresh + Stop calls.
	subMu sync.Mutex
	// subscribedKeys is the set of (namespace, topic) tuples currently
	// libp2p-subscribed by this dispatcher. Used by Refresh to compute the
	// add/remove diff against the live trigger store.
	subscribedKeys map[string]bool

	// stopCh signals the periodic Refresh goroutine to exit.
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewPubSubDispatcher creates a new PubSub trigger dispatcher.
//
// The `ps` argument may be nil (e.g. in tests, or namespaces with pubsub
// disabled) — in that case Start/Refresh are no-ops and the dispatcher
// only fires for explicit Dispatch calls (the legacy HTTP-publish hook).
func NewPubSubDispatcher(
	store *PubSubTriggerStore,
	invoker *serverless.Invoker,
	olricClient olriclib.Client,
	ps dispatcherPubSub,
	logger *zap.Logger,
) *PubSubDispatcher {
	return &PubSubDispatcher{
		store:          store,
		topicLister:    store, // defaults to the real store; tests override
		invoker:        invoker,
		olricClient:    olricClient,
		pubsub:         ps,
		aggregator:     aggregator.New(logger, dispatchTimeout),
		logger:         logger,
		subscribedKeys: make(map[string]bool),
		stopCh:         make(chan struct{}),
	}
}

// subKey produces the map key used to track libp2p subscriptions per
// (namespace, topic) tuple. Keeping it in one place avoids drift.
func subKey(namespace, topic string) string {
	return namespace + "|" + topic
}

// Start subscribes to libp2p pubsub for every literal trigger pattern in
// the store and spawns the periodic refresh goroutine. Returns the first
// Subscribe error if any — but a partial-failure scenario (some topics
// subscribed, others failed) is logged and continues, since one bad topic
// shouldn't break dispatch for every other handler.
//
// Wildcard patterns (e.g. "messages:*") are skipped with a warning. libp2p
// has no native wildcard subscribe, so handling those cross-node properly
// needs a separate mechanism (per-namespace fan-out topic, or hooking
// HostFunctions.PubSubPublish to call Dispatch directly). For now, wildcard
// triggers only fire when the publish originates from the HTTP endpoint
// (which goes through the legacy Dispatch hook).
func (d *PubSubDispatcher) Start(ctx context.Context) error {
	if d.pubsub == nil {
		d.logger.Info("PubSubDispatcher.Start: pubsub disabled, skipping libp2p subscribe")
		return nil
	}
	if err := d.Refresh(ctx); err != nil {
		return err
	}
	go d.refreshLoop()
	d.logger.Info("PubSubDispatcher started",
		zap.Duration("refresh_interval", dispatcherRefreshInterval),
	)
	return nil
}

// Stop signals the periodic refresh goroutine to exit. Safe to call
// multiple times. Does NOT unsubscribe — the dispatcher's libp2p
// subscriptions die with the pubsub manager during gateway shutdown.
func (d *PubSubDispatcher) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
	})
}

// refreshLoop is the periodic-Refresh goroutine spawned by Start. Catches
// trigger add/remove events that didn't go through the Refresh hook (e.g.
// a different gateway node ran the trigger add, or the deploy-time
// auto-register path).
func (d *PubSubDispatcher) refreshLoop() {
	ticker := time.NewTicker(dispatcherRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := d.Refresh(ctx); err != nil {
				d.logger.Warn("PubSubDispatcher periodic refresh failed",
					zap.Error(err))
			}
			cancel()
		}
	}
}

// Refresh re-syncs libp2p subscriptions against the live trigger store:
// subscribes to any new literal patterns, unsubscribes from patterns
// whose triggers were all removed. Idempotent — safe to call from
// multiple paths (Start, trigger-add hook, periodic loop).
//
// Wildcards are skipped (see Start). Errors on individual Subscribe calls
// are logged but do not abort the refresh — one bad topic shouldn't take
// down every other handler.
func (d *PubSubDispatcher) Refresh(ctx context.Context) error {
	if d.pubsub == nil {
		return nil
	}
	subs, err := d.topicLister.ListDistinctTopicPatterns(ctx)
	if err != nil {
		return err
	}

	// Compute the desired set, skipping wildcards.
	desired := make(map[string]DistinctTopicSubscription, len(subs))
	for _, s := range subs {
		if s.Wildcard {
			// Log once-per-refresh would be cleaner but the volume is
			// bounded by the trigger count and this is a known limitation.
			d.logger.Debug("PubSubDispatcher.Refresh: skipping wildcard pattern (libp2p has no wildcard subscribe)",
				zap.String("namespace", s.Namespace),
				zap.String("topic_pattern", s.TopicPattern),
			)
			continue
		}
		desired[subKey(s.Namespace, s.TopicPattern)] = s
	}

	d.subMu.Lock()
	defer d.subMu.Unlock()

	// Subscribe to newly-added topics.
	for key, s := range desired {
		if d.subscribedKeys[key] {
			continue
		}
		ns, topic := s.Namespace, s.TopicPattern
		handler := func(msgTopic string, data []byte) error {
			// PEER_DISCOVERY_PING is filtered upstream in the Manager.
			// data already excludes those.
			d.Dispatch(context.Background(), ns, topic, data, 0)
			return nil
		}
		if err := d.pubsub.Subscribe(ctx, topic, handler); err != nil {
			d.logger.Warn("PubSubDispatcher.Refresh: libp2p Subscribe failed",
				zap.String("namespace", ns),
				zap.String("topic", topic),
				zap.Error(err))
			continue
		}
		d.subscribedKeys[key] = true
		d.logger.Info("PubSubDispatcher subscribed to trigger topic",
			zap.String("namespace", ns),
			zap.String("topic", topic))
	}

	// Unsubscribe from topics whose triggers were all removed.
	for key := range d.subscribedKeys {
		if _, stillDesired := desired[key]; stillDesired {
			continue
		}
		// key format is "namespace|topic"; split safely.
		topic := key
		if i := indexByteFromStart(key, '|'); i >= 0 {
			topic = key[i+1:]
		}
		if err := d.pubsub.Unsubscribe(ctx, topic); err != nil {
			d.logger.Debug("PubSubDispatcher.Refresh: libp2p Unsubscribe ignored",
				zap.String("key", key),
				zap.Error(err))
		}
		delete(d.subscribedKeys, key)
		d.logger.Info("PubSubDispatcher unsubscribed from trigger topic (no live triggers)",
			zap.String("key", key))
	}
	return nil
}

// indexByteFromStart is a tiny local helper to avoid importing `strings`
// for one call. Returns the index of the first occurrence of c in s, or -1.
func indexByteFromStart(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
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

