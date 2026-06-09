package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// WS-subscribe-tracked ephemeral state primitive (bugboard #710).
//
// A serverless function can publish short-lived per-subscriber state (typing
// indicators, "online" flags, cursor positions, …) keyed by (topic, key) and
// have the gateway AUTO-CLEAR that state the moment the owning WebSocket
// client disconnects — publishing a synthetic clear event so every subscriber
// sees the state vanish with zero cron lag. State also expires on a TTL as a
// backstop.
//
// Ownership model: each set is tagged with the CURRENT invocation's WS client
// ID (the same source GetWSClientID reads). On disconnect the store iterates
// that client's owned (topic,key) entries, publishes a clear event for each,
// and drops them. A client's disconnect never touches another client's state.

const (
	// ephemeralMaxKeysPerClient caps how many distinct (topic,key) entries a
	// single WS client may own at once. Bounds the per-client memory + the
	// fan-out of synthetic clears on disconnect.
	ephemeralMaxKeysPerClient = 256

	// ephemeralMaxPayloadBytes caps a single ephemeral payload. Generous for
	// presence/typing/cursor metadata while bounding gateway memory.
	ephemeralMaxPayloadBytes = 16 << 10 // 16 KiB

	// ephemeralMaxTTL caps the requested TTL. Ephemeral state is meant to be
	// short-lived; the disconnect hook is the primary cleanup path and the TTL
	// is only a backstop, so a long TTL is never useful.
	ephemeralMaxTTL = 30 * time.Minute

	// ephemeralDefaultTTL is applied when a caller passes ttlMs <= 0.
	ephemeralDefaultTTL = 60 * time.Second

	// ephemeralSweepInterval is how often the backstop sweeper scans for
	// expired entries. The disconnect hook handles the common case; the
	// sweeper only catches entries whose owner is still connected but whose
	// TTL elapsed.
	ephemeralSweepInterval = 10 * time.Second
)

// EphemeralEventKind discriminates the synthetic events published on a topic.
type EphemeralEventKind string

const (
	EphemeralEventSet   EphemeralEventKind = "set"
	EphemeralEventClear EphemeralEventKind = "clear"
)

// EphemeralEvent is the wire shape published on the topic when ephemeral state
// is set, cleared, or auto-cleared on disconnect/expiry. Subscribers key off
// Kind + Key to update their local view. Payload is only populated for "set".
type EphemeralEvent struct {
	Type     string             `json:"__ephemeral"` // always "state"
	Kind     EphemeralEventKind `json:"kind"`        // set | clear
	Key      string             `json:"key"`         // app-chosen key
	ClientID string             `json:"client_id"`   // owning WS client
	// Payload is the opaque app-chosen blob (may be JSON, protobuf, or
	// arbitrary bytes), present only for "set". encoding/json base64-encodes
	// a []byte on the wire, so subscribers base64-decode "payload" to recover
	// the original bytes — mirroring how pubsub_publish_batch carries data.
	Payload []byte `json:"payload,omitempty"`
	Reason  string `json:"reason,omitempty"` // clear only: explicit|disconnect|expired
}

// ephemeralPublisher publishes data on a (namespace, topic). Abstracted so the
// store can publish synthetic clears without depending on the concrete pubsub
// adapter type — and so tests can capture published events. Namespace handling
// matches the host pubsub path: the adapter namespaces internally, so this
// publisher receives the already-namespaced caller's topic verbatim.
type ephemeralPublisher func(ctx context.Context, namespace, topic string, data []byte) error

// ephemeralEntry is one stored value plus its expiry and the metadata needed
// to publish a clear event for it.
type ephemeralEntry struct {
	namespace string
	topic     string
	key       string
	clientID  string
	payload   []byte
	expiresAt time.Time
}

// ephemeralStateKey identifies a stored value across namespaces/topics.
type ephemeralStateKey struct {
	namespace string
	topic     string
	key       string
}

// EphemeralStore holds WS-subscribe-tracked ephemeral state with auto-clear on
// disconnect (bugboard #710). Safe for concurrent use.
type EphemeralStore struct {
	publish ephemeralPublisher

	mu sync.Mutex
	// values keyed by (ns, topic, key).
	values map[ephemeralStateKey]*ephemeralEntry
	// owned maps a clientID to the set of state keys it owns, for O(1)
	// disconnect cleanup.
	owned map[string]map[ephemeralStateKey]struct{}

	// sweeper lifecycle.
	stopOnce sync.Once
	stopCh   chan struct{}
	now      func() time.Time // injectable clock for tests
}

// NewEphemeralStore constructs a store with the given publisher. The publisher
// may be nil (set/clear then skip publishing) — useful in tests, but in
// production the host wires the pubsub adapter so subscribers see events.
func NewEphemeralStore(publish ephemeralPublisher) *EphemeralStore {
	return &EphemeralStore{
		publish: publish,
		values:  make(map[ephemeralStateKey]*ephemeralEntry),
		owned:   make(map[string]map[ephemeralStateKey]struct{}),
		stopCh:  make(chan struct{}),
		now:     time.Now,
	}
}

// Set records an ephemeral value owned by clientID and publishes a "set" event
// on the topic so subscribers observe it. Returns an error on validation
// failure (empty client/topic/key, oversized payload, per-client cap reached).
func (s *EphemeralStore) Set(ctx context.Context, namespace, clientID, topic, key string, payload []byte, ttlMs int64) error {
	if clientID == "" {
		return fmt.Errorf("ephemeral_state_set: requires a WebSocket client (no ws_client_id in invocation context)")
	}
	if topic == "" || key == "" {
		return fmt.Errorf("ephemeral_state_set: topic and key are required")
	}
	if len(payload) > ephemeralMaxPayloadBytes {
		return fmt.Errorf("ephemeral_state_set: payload too large (%d > %d bytes)", len(payload), ephemeralMaxPayloadBytes)
	}

	ttl := time.Duration(ttlMs) * time.Millisecond
	if ttl <= 0 {
		ttl = ephemeralDefaultTTL
	}
	if ttl > ephemeralMaxTTL {
		ttl = ephemeralMaxTTL
	}

	sk := ephemeralStateKey{namespace: namespace, topic: topic, key: key}
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	s.mu.Lock()
	ownedSet := s.owned[clientID]
	// Enforce the per-client cap only for NEW keys this client doesn't already
	// own — overwriting an existing key must always be allowed.
	if _, alreadyOwned := s.values[sk]; !alreadyOwned || s.values[sk].clientID != clientID {
		if len(ownedSet) >= ephemeralMaxKeysPerClient {
			s.mu.Unlock()
			return fmt.Errorf("ephemeral_state_set: client %s exceeded max %d ephemeral keys", clientID, ephemeralMaxKeysPerClient)
		}
	}

	// If a different client owned this exact (ns,topic,key), transfer ownership
	// — drop it from the previous owner's set so its disconnect won't clear
	// state it no longer owns.
	if prev, ok := s.values[sk]; ok && prev.clientID != clientID {
		if prevSet := s.owned[prev.clientID]; prevSet != nil {
			delete(prevSet, sk)
			if len(prevSet) == 0 {
				delete(s.owned, prev.clientID)
			}
		}
	}

	s.values[sk] = &ephemeralEntry{
		namespace: namespace,
		topic:     topic,
		key:       key,
		clientID:  clientID,
		payload:   payloadCopy,
		expiresAt: s.now().Add(ttl),
	}
	if ownedSet == nil {
		ownedSet = make(map[ephemeralStateKey]struct{})
		s.owned[clientID] = ownedSet
	}
	ownedSet[sk] = struct{}{}
	s.mu.Unlock()

	evt := EphemeralEvent{
		Type:     "state",
		Kind:     EphemeralEventSet,
		Key:      key,
		ClientID: clientID,
		Payload:  payloadCopy,
	}
	return s.publishEvent(ctx, namespace, topic, evt)
}

// Clear removes an ephemeral value the client owns and publishes a "clear"
// event with reason "explicit". Clearing a key the client does not own (or a
// missing key) is a no-op that still returns nil — clears are idempotent.
func (s *EphemeralStore) Clear(ctx context.Context, namespace, clientID, topic, key string) error {
	if clientID == "" {
		return fmt.Errorf("ephemeral_state_clear: requires a WebSocket client (no ws_client_id in invocation context)")
	}
	if topic == "" || key == "" {
		return fmt.Errorf("ephemeral_state_clear: topic and key are required")
	}

	sk := ephemeralStateKey{namespace: namespace, topic: topic, key: key}

	s.mu.Lock()
	entry, ok := s.values[sk]
	if !ok || entry.clientID != clientID {
		// Not present, or owned by someone else — idempotent no-op.
		s.mu.Unlock()
		return nil
	}
	s.removeLocked(sk, entry)
	s.mu.Unlock()

	return s.publishEvent(ctx, namespace, topic, EphemeralEvent{
		Type:     "state",
		Kind:     EphemeralEventClear,
		Key:      key,
		ClientID: clientID,
		Reason:   "explicit",
	})
}

// ClearClient removes every entry owned by clientID and publishes a clear
// event for each (reason "disconnect"). Called from the WS disconnect hook —
// the primary, zero-lag cleanup path. Safe to call for an unknown client.
func (s *EphemeralStore) ClearClient(ctx context.Context, clientID string) {
	s.clearClientWithReason(ctx, clientID, "disconnect")
}

func (s *EphemeralStore) clearClientWithReason(ctx context.Context, clientID, reason string) {
	s.mu.Lock()
	ownedSet := s.owned[clientID]
	if len(ownedSet) == 0 {
		delete(s.owned, clientID)
		s.mu.Unlock()
		return
	}
	// Snapshot entries to publish after releasing the lock.
	toClear := make([]*ephemeralEntry, 0, len(ownedSet))
	for sk := range ownedSet {
		if entry, ok := s.values[sk]; ok {
			toClear = append(toClear, entry)
			delete(s.values, sk)
		}
	}
	delete(s.owned, clientID)
	s.mu.Unlock()

	for _, entry := range toClear {
		_ = s.publishEvent(ctx, entry.namespace, entry.topic, EphemeralEvent{
			Type:     "state",
			Kind:     EphemeralEventClear,
			Key:      entry.key,
			ClientID: clientID,
			Reason:   reason,
		})
	}
}

// removeLocked drops one entry from both maps. Caller holds s.mu.
func (s *EphemeralStore) removeLocked(sk ephemeralStateKey, entry *ephemeralEntry) {
	delete(s.values, sk)
	if set := s.owned[entry.clientID]; set != nil {
		delete(set, sk)
		if len(set) == 0 {
			delete(s.owned, entry.clientID)
		}
	}
}

// publishEvent marshals and publishes a synthetic event. No-op (nil) when no
// publisher is wired.
func (s *EphemeralStore) publishEvent(ctx context.Context, namespace, topic string, evt EphemeralEvent) error {
	if s.publish == nil {
		return nil
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("ephemeral state: marshal event: %w", err)
	}
	if err := s.publish(ctx, namespace, topic, data); err != nil {
		return fmt.Errorf("ephemeral state: publish %s event: %w", evt.Kind, err)
	}
	return nil
}

// StartSweeper launches the TTL backstop sweeper. Idempotent guards aren't
// provided — call exactly once. Stop with StopSweeper.
func (s *EphemeralStore) StartSweeper() {
	go func() {
		ticker := time.NewTicker(ephemeralSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.sweepExpired(context.Background())
			}
		}
	}()
}

// StopSweeper stops the backstop sweeper. Safe to call multiple times.
func (s *EphemeralStore) StopSweeper() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// sweepExpired removes and publishes clears for every entry whose TTL elapsed.
func (s *EphemeralStore) sweepExpired(ctx context.Context) {
	now := s.now()

	s.mu.Lock()
	var expired []*ephemeralEntry
	for sk, entry := range s.values {
		if now.After(entry.expiresAt) {
			expired = append(expired, entry)
			s.removeLocked(sk, entry)
		}
	}
	s.mu.Unlock()

	for _, entry := range expired {
		_ = s.publishEvent(ctx, entry.namespace, entry.topic, EphemeralEvent{
			Type:     "state",
			Kind:     EphemeralEventClear,
			Key:      entry.key,
			ClientID: entry.clientID,
			Reason:   "expired",
		})
	}
}

// keyCountForTest returns the number of stored values (test-only accessor).
func (s *EphemeralStore) keyCountForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.values)
}
