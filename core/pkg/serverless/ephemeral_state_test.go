package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// capturePublisher records every published event for assertions.
type capturePublisher struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	namespace string
	topic     string
	event     EphemeralEvent
}

func (c *capturePublisher) publish(_ context.Context, namespace, topic string, data []byte) error {
	var evt EphemeralEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}
	c.mu.Lock()
	c.events = append(c.events, capturedEvent{namespace: namespace, topic: topic, event: evt})
	c.mu.Unlock()
	return nil
}

func (c *capturePublisher) snapshot() []capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEvent, len(c.events))
	copy(out, c.events)
	return out
}

func (c *capturePublisher) countKind(eventType string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.event.Type == eventType {
			n++
		}
	}
	return n
}

func newTestStore(pub ephemeralPublisher) *EphemeralStore {
	s := NewEphemeralStore(pub)
	return s
}

func TestEphemeralStore_SetThenClear(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "client-A", "typing:room1", "k1", []byte(`{"typing":true}`), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if s.keyCountForTest() != 1 {
		t.Fatalf("expected 1 stored key, got %d", s.keyCountForTest())
	}

	if err := s.Clear(ctx, "ns1", "client-A", "typing:room1", "k1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if s.keyCountForTest() != 0 {
		t.Errorf("expected 0 stored keys after clear, got %d", s.keyCountForTest())
	}

	if got := pub.countKind(EphemeralEventSet); got != 1 {
		t.Errorf("set events = %d, want 1", got)
	}
	if got := pub.countKind(EphemeralEventClear); got != 1 {
		t.Errorf("clear events = %d, want 1", got)
	}
	// The set event must carry the payload verbatim.
	evts := pub.snapshot()
	if string(evts[0].event.Payload) != `{"typing":true}` {
		t.Errorf("set payload = %q, want the original JSON", evts[0].event.Payload)
	}
	if evts[1].event.Reason != "explicit" {
		t.Errorf("clear reason = %q, want explicit", evts[1].event.Reason)
	}
}

func TestEphemeralStore_SetThenDisconnect(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "client-A", "topicX", "kA", []byte("p1"), 0); err != nil {
		t.Fatalf("Set kA: %v", err)
	}
	if err := s.Set(ctx, "ns1", "client-A", "topicY", "kB", []byte("p2"), 0); err != nil {
		t.Fatalf("Set kB: %v", err)
	}

	s.ClearClient(ctx, "client-A")

	if s.keyCountForTest() != 0 {
		t.Errorf("expected all state dropped on disconnect, got %d", s.keyCountForTest())
	}
	// One synthetic clear per owned key, all reason=disconnect.
	if got := pub.countKind(EphemeralEventClear); got != 2 {
		t.Errorf("disconnect clear events = %d, want 2", got)
	}
	for _, e := range pub.snapshot() {
		if e.event.Type == EphemeralEventClear && e.event.Reason != "disconnect" {
			t.Errorf("clear reason = %q, want disconnect", e.event.Reason)
		}
	}
}

func TestEphemeralStore_TTLExpiry(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	// Freeze the clock so we control expiry deterministically.
	base := time.Now()
	s.now = func() time.Time { return base }

	if err := s.Set(ctx, "ns1", "client-A", "topicX", "kA", []byte("p"), 1000); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Before expiry: sweep is a no-op.
	s.sweepExpired(ctx)
	if s.keyCountForTest() != 1 {
		t.Fatalf("entry expired too early, count=%d", s.keyCountForTest())
	}

	// Advance past the 1s TTL and sweep.
	s.now = func() time.Time { return base.Add(2 * time.Second) }
	s.sweepExpired(ctx)
	if s.keyCountForTest() != 0 {
		t.Errorf("entry not swept after TTL, count=%d", s.keyCountForTest())
	}

	// A clear event with reason=expired must have been published.
	foundExpired := false
	for _, e := range pub.snapshot() {
		if e.event.Type == EphemeralEventClear && e.event.Reason == "expired" {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Error("expected a clear event with reason=expired")
	}
}

func TestEphemeralStore_TTLClampedToMax(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	base := time.Now()
	s.now = func() time.Time { return base }

	// Request a TTL far beyond the max; it must be clamped.
	huge := (ephemeralMaxTTL + time.Hour).Milliseconds()
	if err := s.Set(context.Background(), "ns1", "c", "t", "k", []byte("p"), huge); err != nil {
		t.Fatalf("Set: %v", err)
	}
	s.mu.Lock()
	entry := s.values[ephemeralStateKey{namespace: "ns1", topic: "t", key: "k"}]
	s.mu.Unlock()
	if entry == nil {
		t.Fatal("entry missing")
	}
	maxExpiry := base.Add(ephemeralMaxTTL)
	if entry.expiresAt.After(maxExpiry) {
		t.Errorf("TTL not clamped: expiresAt %v after max %v", entry.expiresAt, maxExpiry)
	}
}

func TestEphemeralStore_PerClientCapEnforced(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	for i := 0; i < ephemeralMaxKeysPerClient; i++ {
		if err := s.Set(ctx, "ns1", "client-A", "t", fmt.Sprintf("k%d", i), []byte("p"), 0); err != nil {
			t.Fatalf("Set #%d: %v", i, err)
		}
	}
	// The next NEW key must be rejected.
	err := s.Set(ctx, "ns1", "client-A", "t", "overflow", []byte("p"), 0)
	if err == nil {
		t.Fatal("expected per-client cap error")
	}
	if s.keyCountForTest() != ephemeralMaxKeysPerClient {
		t.Errorf("stored keys = %d, want %d (overflow must not be stored)", s.keyCountForTest(), ephemeralMaxKeysPerClient)
	}

	// Overwriting an EXISTING key must still succeed even at the cap.
	if err := s.Set(ctx, "ns1", "client-A", "t", "k0", []byte("updated"), 0); err != nil {
		t.Errorf("overwrite at cap rejected: %v", err)
	}
}

func TestEphemeralStore_ClientIsolation(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "client-A", "t", "kA", []byte("a"), 0); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := s.Set(ctx, "ns1", "client-B", "t", "kB", []byte("b"), 0); err != nil {
		t.Fatalf("Set B: %v", err)
	}

	// Disconnecting A must NOT touch B's state.
	s.ClearClient(ctx, "client-A")
	if s.keyCountForTest() != 1 {
		t.Fatalf("expected B's single key to survive A's disconnect, got %d", s.keyCountForTest())
	}
	s.mu.Lock()
	_, bSurvives := s.values[ephemeralStateKey{namespace: "ns1", topic: "t", key: "kB"}]
	s.mu.Unlock()
	if !bSurvives {
		t.Error("client-B's state was wrongly cleared by client-A's disconnect")
	}

	// A also cannot clear B's key (not the owner): idempotent no-op.
	if err := s.Clear(ctx, "ns1", "client-A", "t", "kB"); err != nil {
		t.Fatalf("cross-client Clear should be a no-op, got err: %v", err)
	}
	if s.keyCountForTest() != 1 {
		t.Error("client-A managed to clear client-B's key")
	}
}

func TestEphemeralStore_SetValidation(t *testing.T) {
	s := newTestStore(nil)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "", "t", "k", nil, 0); err == nil {
		t.Error("expected error for empty client ID")
	}
	if err := s.Set(ctx, "ns1", "c", "", "k", nil, 0); err == nil {
		t.Error("expected error for empty topic")
	}
	if err := s.Set(ctx, "ns1", "c", "t", "", nil, 0); err == nil {
		t.Error("expected error for empty key")
	}
	big := make([]byte, ephemeralMaxPayloadBytes+1)
	if err := s.Set(ctx, "ns1", "c", "t", "k", big, 0); err == nil {
		t.Error("expected error for oversized payload")
	}
}

func TestEphemeralStore_ClearClientUnknownIsNoOp(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	// No panic, no events for an unknown client.
	s.ClearClient(context.Background(), "nobody")
	if len(pub.snapshot()) != 0 {
		t.Error("ClearClient on unknown client should publish nothing")
	}
}

func TestEphemeralStore_OwnershipTransfer(t *testing.T) {
	pub := &capturePublisher{}
	s := newTestStore(pub.publish)
	ctx := context.Background()

	// client-A sets, then client-B overwrites the SAME (topic,key).
	if err := s.Set(ctx, "ns1", "client-A", "t", "shared", []byte("a"), 0); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := s.Set(ctx, "ns1", "client-B", "t", "shared", []byte("b"), 0); err != nil {
		t.Fatalf("Set B: %v", err)
	}

	// A's disconnect must NOT clear the key now owned by B.
	s.ClearClient(ctx, "client-A")
	if s.keyCountForTest() != 1 {
		t.Errorf("ownership transfer failed: key dropped on prior owner's disconnect, count=%d", s.keyCountForTest())
	}

	// B's disconnect clears it.
	s.ClearClient(ctx, "client-B")
	if s.keyCountForTest() != 0 {
		t.Errorf("new owner's disconnect did not clear, count=%d", s.keyCountForTest())
	}
}

// TestEphemeralStore_wireFormatContract pins the EXACT JSON wire shape of the
// synthetic events — the `_orama` control-frame contract agreed with app teams
// on bugboard #710 (#458/#505/#849/#901). Client sub-routers dispatch on the
// `_orama` discriminator; renaming any of these fields is a breaking protocol
// change and must fail this test.
func TestEphemeralStore_wireFormatContract(t *testing.T) {
	type raw struct {
		Orama    string `json:"_orama"`
		Topic    string `json:"topic"`
		Key      string `json:"key"`
		ClientID string `json:"client_id"`
		Payload  []byte `json:"payload"`
		Reason   string `json:"reason"`
	}
	var got []raw
	pub := func(_ context.Context, _, _ string, data []byte) error {
		var r raw
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		got = append(got, r)
		return nil
	}
	s := newTestStore(pub)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "client-A", "typing:room1", "user-7", []byte("blob"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	s.ClearClient(ctx, "client-A")

	if len(got) != 2 {
		t.Fatalf("expected 2 events (set + disconnect clear), got %d", len(got))
	}
	set, clear := got[0], got[1]
	if set.Orama != "ephemeral.set" {
		t.Errorf(`set _orama = %q, want "ephemeral.set"`, set.Orama)
	}
	if set.Topic != "typing:room1" || set.Key != "user-7" || set.ClientID != "client-A" {
		t.Errorf("set event fields wrong: %+v", set)
	}
	if string(set.Payload) != "blob" {
		t.Errorf("set payload = %q, want blob", set.Payload)
	}
	if clear.Orama != "ephemeral.clear" {
		t.Errorf(`clear _orama = %q, want "ephemeral.clear"`, clear.Orama)
	}
	if clear.Topic != "typing:room1" || clear.Key != "user-7" || clear.Reason != "disconnect" {
		t.Errorf("clear event fields wrong: %+v", clear)
	}
}

func TestEphemeralStoreList_returnsLiveEntriesSorted(t *testing.T) {
	s := newTestStore(nil)
	ctx := context.Background()

	if err := s.Set(ctx, "ns1", "client-B", "presence:room1", "zeta", []byte("z"), 0); err != nil {
		t.Fatalf("Set zeta: %v", err)
	}
	if err := s.Set(ctx, "ns1", "client-A", "presence:room1", "alpha", []byte("a"), 0); err != nil {
		t.Fatalf("Set alpha: %v", err)
	}

	entries := s.List("ns1", "presence:room1")
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	if entries[0].Key != "alpha" || entries[1].Key != "zeta" {
		t.Errorf("entries not sorted by key: %q, %q", entries[0].Key, entries[1].Key)
	}
	if entries[0].ClientID != "client-A" || string(entries[0].Payload) != "a" {
		t.Errorf("entry fields wrong: %+v", entries[0])
	}
	if entries[0].ExpiresInMs <= 0 {
		t.Errorf("ExpiresInMs must be positive for a live entry, got %d", entries[0].ExpiresInMs)
	}
}

func TestEphemeralStoreList_excludesExpiredAndOtherScopes(t *testing.T) {
	s := newTestStore(nil)
	ctx := context.Background()
	base := time.Now()
	s.now = func() time.Time { return base }

	if err := s.Set(ctx, "ns1", "c", "t", "live", []byte("p"), 60_000); err != nil {
		t.Fatalf("Set live: %v", err)
	}
	if err := s.Set(ctx, "ns1", "c", "t", "dying", []byte("p"), 1000); err != nil {
		t.Fatalf("Set dying: %v", err)
	}
	if err := s.Set(ctx, "ns2", "c", "t", "other-ns", []byte("p"), 60_000); err != nil {
		t.Fatalf("Set other-ns: %v", err)
	}
	if err := s.Set(ctx, "ns1", "c", "t2", "other-topic", []byte("p"), 60_000); err != nil {
		t.Fatalf("Set other-topic: %v", err)
	}

	// Advance past "dying"'s TTL but do NOT sweep — List must hide it anyway.
	s.now = func() time.Time { return base.Add(2 * time.Second) }

	entries := s.List("ns1", "t")
	if len(entries) != 1 || entries[0].Key != "live" {
		t.Fatalf("List = %+v, want exactly the single live ns1/t entry", entries)
	}
}

func TestEphemeralStoreList_emptyTopicReturnsEmpty(t *testing.T) {
	s := newTestStore(nil)
	if entries := s.List("ns1", "nothing-here"); len(entries) != 0 {
		t.Errorf("List on empty topic = %+v, want empty", entries)
	}
}

func TestEphemeralStoreList_snapshotIsDefensiveCopy(t *testing.T) {
	s := newTestStore(nil)
	ctx := context.Background()
	if err := s.Set(ctx, "ns1", "c", "t", "k", []byte("orig"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	entries := s.List("ns1", "t")
	entries[0].Payload[0] = 'X'
	fresh := s.List("ns1", "t")
	if string(fresh[0].Payload) != "orig" {
		t.Error("List payload is not a defensive copy; store was mutated")
	}
}
