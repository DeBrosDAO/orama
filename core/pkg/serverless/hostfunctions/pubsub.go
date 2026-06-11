package hostfunctions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// maxPublishesPerInvocation caps how many pubsub messages a single function
// invocation (or persistent WS frame) may publish. This is a safety bound, not
// a normal-path limit: legitimate functions publish a handful (message-create
// does 3). It exists because the WASM runtime has no fuel metering and the
// request rate limiter gates invocation FREQUENCY, not per-invocation host-call
// volume — so without it a buggy/hostile `for { publish() }` could flood the
// shared gossipsub router, amplified to every peer by FloodPublish. 1000 is far
// above any real workload while bounding the blast radius ~1000x.
const maxPublishesPerInvocation = 1000

// PubSubPublish publishes a message to a topic.
//
// After a successful libp2p publish, also synchronously fires local
// wildcard triggers via the dispatcher (bugboard #93). Concrete-topic
// triggers are skipped here — they get delivered by the libp2p
// subscribe-loopback path and would double-invoke if fired locally too.
// See dispatcher.DispatchLocalPublish for the filter rationale.
//
// When no triggerDispatcher is wired (tests, or a future deployment
// without serverless triggers), this is just the plain libp2p publish
// — behavior unchanged from before #93.
func (h *HostFunctions) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if h.pubsub == nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish", Cause: fmt.Errorf("pubsub not available")}
	}

	if n := serverless.AddPublishCount(ctx, 1); n > maxPublishesPerInvocation {
		return &serverless.HostFunctionError{
			Function: "pubsub_publish",
			Cause:    fmt.Errorf("publish budget exceeded (max %d per invocation)", maxPublishesPerInvocation),
		}
	}

	// The pubsub adapter handles namespacing internally
	if err := h.pubsub.Publish(ctx, topic, data); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish", Cause: err}
	}

	h.dispatchLocalWildcards(ctx, topic, data)
	return nil
}

// dispatchLocalWildcards calls the trigger dispatcher's wildcard-only
// local-dispatch path for the given topic. Safe no-op when the
// dispatcher isn't wired or there's no namespace in the invocation
// context (e.g. when called from a non-serverless caller).
//
// Same-gateway publishes cover ~100% of namespace-gateway architecture
// (single gateway process per namespace). Cross-gateway wildcard
// delivery is plan-6/plan-10 territory and out of scope.
func (h *HostFunctions) dispatchLocalWildcards(ctx context.Context, topic string, data []byte) {
	h.triggerDispatcherLock.RLock()
	d := h.triggerDispatcher
	h.triggerDispatcherLock.RUnlock()
	if d == nil {
		return
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.Namespace == "" {
		// No namespace = nothing to look up; skip silently.
		return
	}
	// Pass the CURRENT invocation's depth so DispatchLocalPublish's
	// own check (`if depth >= maxTriggerDepth { return }`) eventually
	// trips after enough self-recursive WASM publishes (function on
	// "events:*" publishes "events:done" → loops). Without this thread,
	// every WASM publish reset depth to 0 and the local-recursion loop
	// was only bounded by dispatchTimeout + the rate limiter — much
	// weaker (security audit MEDIUM, bugboard #93 follow-up).
	d.DispatchLocalPublish(ctx, cur.Namespace, topic, data, cur.TriggerDepth)
}

// pubSubBatchEntry mirrors the JSON shape accepted by PubSubPublishBatch.
type pubSubBatchEntry struct {
	Topic   string `json:"topic"`
	DataB64 string `json:"data_base64"`
}

// PubSubPublishBatch publishes multiple messages in parallel.
//
// Input is JSON: [{"topic":"...","data_base64":"..."}, ...]
// Up to pubsub.MaxBatchSize entries per call.
//
// Default behavior is fail-fast (first publish error is returned). The
// host function does not currently expose a best-effort flag — WASM
// callers that need it should call this function multiple times in
// chunks they're willing to retry independently.
func (h *HostFunctions) PubSubPublishBatch(ctx context.Context, msgsJSON []byte) error {
	if h.pubsub == nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: fmt.Errorf("pubsub not available")}
	}

	var entries []pubSubBatchEntry
	if err := json.Unmarshal(msgsJSON, &entries); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: fmt.Errorf("invalid json: %w", err)}
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > pubsub.MaxBatchSize {
		return &serverless.HostFunctionError{
			Function: "pubsub_publish_batch",
			Cause:    fmt.Errorf("too many messages: max %d per batch", pubsub.MaxBatchSize),
		}
	}

	msgs := make([]pubsub.TopicMessage, 0, len(entries))
	for i, e := range entries {
		if e.Topic == "" {
			return &serverless.HostFunctionError{
				Function: "pubsub_publish_batch",
				Cause:    fmt.Errorf("entry %d: empty topic", i),
			}
		}
		data, err := base64.StdEncoding.DecodeString(e.DataB64)
		if err != nil {
			return &serverless.HostFunctionError{
				Function: "pubsub_publish_batch",
				Cause:    fmt.Errorf("entry %d (topic %q): bad base64: %w", i, e.Topic, err),
			}
		}
		msgs = append(msgs, pubsub.TopicMessage{Topic: e.Topic, Data: data})
	}

	if n := serverless.AddPublishCount(ctx, len(msgs)); n > maxPublishesPerInvocation {
		return &serverless.HostFunctionError{
			Function: "pubsub_publish_batch",
			Cause:    fmt.Errorf("publish budget exceeded (max %d per invocation)", maxPublishesPerInvocation),
		}
	}

	if err := h.pubsub.PublishBatch(ctx, msgs, pubsub.PublishBatchOptions{}); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: err}
	}

	// Fire local wildcard triggers per UNIQUE topic — same rationale as
	// PubSubPublish above. Done after the batch succeeds so we don't
	// fire phantom dispatches for messages that didn't actually publish.
	for _, e := range dedupBatchByTopic(msgs) {
		h.dispatchLocalWildcards(ctx, e.Topic, e.Data)
	}
	return nil
}

// dedupBatchByTopic collapses a batch to one entry per unique topic,
// keeping insertion order and most-recent-wins semantics on the data
// payload.
//
// A batch with 100 entries on the same topic should only run ONE
// trigger lookup + dispatch — otherwise the same wildcard-matching
// handler gets invoked 100 times for what is semantically one logical
// wakeup. Most-recent-wins matches what a downstream subscriber would
// see after libp2p coalescing in practice. Bounds the fan-out from
// len(batch) × N wildcard handlers to distinct-topics × N (security
// audit MEDIUM, bug #93 follow-up).
//
// Pure function so the batch-dedup logic pins exactly.
func dedupBatchByTopic(msgs []pubsub.TopicMessage) []pubsub.TopicMessage {
	if len(msgs) <= 1 {
		return msgs
	}
	lastByTopic := make(map[string][]byte, len(msgs))
	order := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if _, seen := lastByTopic[m.Topic]; !seen {
			order = append(order, m.Topic)
		}
		lastByTopic[m.Topic] = m.Data
	}
	out := make([]pubsub.TopicMessage, 0, len(order))
	for _, topic := range order {
		out = append(out, pubsub.TopicMessage{Topic: topic, Data: lastByTopic[topic]})
	}
	return out
}

// EphemeralStateSet records WS-subscribe-tracked ephemeral state for the
// current invocation's WS client and publishes a "set" event (bugboard #710).
// The owning client ID and namespace are derived from the invocation context —
// the function cannot spoof them. Auto-clears on the client's WS disconnect.
func (h *HostFunctions) EphemeralStateSet(ctx context.Context, topic, key string, payload []byte, ttlMs int64) error {
	if h.ephemeralStore == nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_set", Cause: fmt.Errorf("ephemeral state not available on this gateway")}
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_set", Cause: fmt.Errorf("no invocation context")}
	}
	if err := h.ephemeralStore.Set(ctx, cur.Namespace, cur.WSClientID, topic, key, payload, ttlMs); err != nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_set", Cause: err}
	}
	return nil
}

// EphemeralStateClear removes ephemeral state the current WS client owns and
// publishes a "clear" event (bugboard #710). Idempotent.
func (h *HostFunctions) EphemeralStateClear(ctx context.Context, topic, key string) error {
	if h.ephemeralStore == nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_clear", Cause: fmt.Errorf("ephemeral state not available on this gateway")}
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_clear", Cause: fmt.Errorf("no invocation context")}
	}
	if err := h.ephemeralStore.Clear(ctx, cur.Namespace, cur.WSClientID, topic, key); err != nil {
		return &serverless.HostFunctionError{Function: "ephemeral_state_clear", Cause: err}
	}
	return nil
}

// ephemeralListEnvelope is the JSON shape returned by EphemeralStateList —
// an object (not a bare array) so fields can be added without breaking
// existing WASM callers.
type ephemeralListEnvelope struct {
	Entries []serverless.EphemeralListEntry `json:"entries"`
}

// EphemeralStateList returns the live ephemeral entries on a topic in the
// invocation's namespace (bugboard #710 reconnect catch-up). Read-only: no
// WS client required, so HTTP-invoked functions can serve snapshots too.
func (h *HostFunctions) EphemeralStateList(ctx context.Context, topic string) ([]byte, error) {
	if h.ephemeralStore == nil {
		return nil, &serverless.HostFunctionError{Function: "ephemeral_state_list", Cause: fmt.Errorf("ephemeral state not available on this gateway")}
	}
	if topic == "" {
		return nil, &serverless.HostFunctionError{Function: "ephemeral_state_list", Cause: fmt.Errorf("topic is required")}
	}
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return nil, &serverless.HostFunctionError{Function: "ephemeral_state_list", Cause: fmt.Errorf("no invocation context")}
	}
	out, err := json.Marshal(ephemeralListEnvelope{Entries: h.ephemeralStore.List(cur.Namespace, topic)})
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "ephemeral_state_list", Cause: fmt.Errorf("marshal entries: %w", err)}
	}
	return out, nil
}

// WSSend sends data to a specific WebSocket client.
func (h *HostFunctions) WSSend(ctx context.Context, clientID string, data []byte) error {
	if h.wsManager == nil {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: serverless.ErrWSNotAvailable}
	}

	// If no clientID provided, use the current invocation's client.
	// Reads ctx-attached invCtx first (per-call, race-free for persistent
	// WS) then falls back to the singleton — see invocation_context.go.
	if clientID == "" {
		if cur := h.currentInvocationContext(ctx); cur != nil && cur.WSClientID != "" {
			clientID = cur.WSClientID
		}
	}

	if clientID == "" {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: serverless.ErrWSNotAvailable}
	}

	if err := h.wsManager.Send(clientID, data); err != nil {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: err}
	}

	return nil
}

// WSBroadcast sends data to all WebSocket clients subscribed to a topic.
func (h *HostFunctions) WSBroadcast(ctx context.Context, topic string, data []byte) error {
	if h.wsManager == nil {
		return &serverless.HostFunctionError{Function: "ws_broadcast", Cause: serverless.ErrWSNotAvailable}
	}

	if err := h.wsManager.Broadcast(topic, data); err != nil {
		return &serverless.HostFunctionError{Function: "ws_broadcast", Cause: err}
	}

	return nil
}
