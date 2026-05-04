package wsbridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// fakePubSub records subscribe/unsubscribe calls and lets tests deliver
// synthetic messages.
type fakePubSub struct {
	mu       sync.Mutex
	subs     map[string]pubsub.MessageHandler
	subCalls int32
	unsubCalls int32
	failSubscribe bool
}

func newFakePubSub() *fakePubSub {
	return &fakePubSub{subs: make(map[string]pubsub.MessageHandler)}
}

func (f *fakePubSub) Subscribe(_ context.Context, topic string, handler pubsub.MessageHandler) error {
	atomic.AddInt32(&f.subCalls, 1)
	if f.failSubscribe {
		return errors.New("fakePubSub: subscribe failed")
	}
	f.mu.Lock()
	f.subs[topic] = handler
	f.mu.Unlock()
	return nil
}

func (f *fakePubSub) Unsubscribe(_ context.Context, topic string) error {
	atomic.AddInt32(&f.unsubCalls, 1)
	f.mu.Lock()
	delete(f.subs, topic)
	f.mu.Unlock()
	return nil
}

// deliver simulates a libp2p message arriving on `topic`.
func (f *fakePubSub) deliver(topic string, data []byte) {
	f.mu.Lock()
	h := f.subs[topic]
	f.mu.Unlock()
	if h != nil {
		_ = h(topic, data)
	}
}

// fakeWS records Send calls keyed by clientID.
type fakeWS struct {
	mu       sync.Mutex
	received map[string][][]byte
	failFor  map[string]bool
}

func newFakeWS() *fakeWS {
	return &fakeWS{
		received: make(map[string][][]byte),
		failFor:  make(map[string]bool),
	}
}

func (f *fakeWS) Send(clientID string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFor[clientID] {
		return errors.New("client closed")
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	f.received[clientID] = append(f.received[clientID], cp)
	return nil
}

func (f *fakeWS) sentTo(clientID string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.received[clientID]...)
}

func TestAdd_first_client_subscribes_libp2p(t *testing.T) {
	ps := newFakePubSub()
	ws := newFakeWS()
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("c1", "ns")

	if err := b.Add(context.Background(), "ns", "c1", "topic-A"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if atomic.LoadInt32(&ps.subCalls) != 1 {
		t.Errorf("expected 1 subscribe call, got %d", ps.subCalls)
	}
}

func TestAdd_second_client_no_extra_libp2p_subscribe(t *testing.T) {
	ps := newFakePubSub()
	b := New(ps, newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	b.SetClientNamespace("c2", "ns")

	_ = b.Add(context.Background(), "ns", "c1", "topic-A")
	_ = b.Add(context.Background(), "ns", "c2", "topic-A")

	if atomic.LoadInt32(&ps.subCalls) != 1 {
		t.Errorf("expected 1 subscribe (refcount=2), got %d", ps.subCalls)
	}
}

func TestAdd_idempotent(t *testing.T) {
	ps := newFakePubSub()
	b := New(ps, newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")

	for i := 0; i < 5; i++ {
		if err := b.Add(context.Background(), "ns", "c1", "topic-A"); err != nil {
			t.Fatalf("idempotent Add %d failed: %v", i, err)
		}
	}
	if atomic.LoadInt32(&ps.subCalls) != 1 {
		t.Errorf("expected 1 subscribe even after 5 adds, got %d", ps.subCalls)
	}
}

func TestAdd_subscribe_failure_rolls_back(t *testing.T) {
	ps := newFakePubSub()
	ps.failSubscribe = true
	b := New(ps, newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")

	err := b.Add(context.Background(), "ns", "c1", "topic-A")
	if err == nil {
		t.Fatal("expected error from failed subscribe")
	}
	stats := b.Stats()
	if stats.TotalBridges != 0 {
		t.Errorf("expected rollback to leave 0 bridges, got %d", stats.TotalBridges)
	}
}

func TestRemove_last_client_unsubscribes_libp2p(t *testing.T) {
	ps := newFakePubSub()
	b := New(ps, newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	b.SetClientNamespace("c2", "ns")

	_ = b.Add(context.Background(), "ns", "c1", "topic-A")
	_ = b.Add(context.Background(), "ns", "c2", "topic-A")

	_ = b.Remove(context.Background(), "ns", "c1", "topic-A")
	if atomic.LoadInt32(&ps.unsubCalls) != 0 {
		t.Errorf("expected no unsubscribe yet (c2 still bridged), got %d", ps.unsubCalls)
	}
	_ = b.Remove(context.Background(), "ns", "c2", "topic-A")
	if atomic.LoadInt32(&ps.unsubCalls) != 1 {
		t.Errorf("expected unsubscribe after last client, got %d", ps.unsubCalls)
	}
}

func TestRemoveClient_cleans_all_bridges(t *testing.T) {
	ps := newFakePubSub()
	b := New(ps, newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")

	_ = b.Add(context.Background(), "ns", "c1", "topic-A")
	_ = b.Add(context.Background(), "ns", "c1", "topic-B")
	_ = b.Add(context.Background(), "ns", "c1", "topic-C")

	b.RemoveClient(context.Background(), "c1")

	stats := b.Stats()
	if stats.ActiveClients != 0 || stats.TotalBridges != 0 || stats.ActiveTopics != 0 {
		t.Errorf("expected all-zero stats after RemoveClient, got %+v", stats)
	}
	if atomic.LoadInt32(&ps.unsubCalls) != 3 {
		t.Errorf("expected 3 unsubscribes (one per topic), got %d", ps.unsubCalls)
	}
}

func TestForwarding_delivers_to_correct_clients_only(t *testing.T) {
	ps := newFakePubSub()
	ws := newFakeWS()
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	b.SetClientNamespace("c2", "ns")
	b.SetClientNamespace("c3", "ns")

	_ = b.Add(context.Background(), "ns", "c1", "topic-A")
	_ = b.Add(context.Background(), "ns", "c2", "topic-A")
	// c3 NOT bridged — should not receive

	ps.deliver("topic-A", []byte("hello"))

	if got := len(ws.sentTo("c1")); got != 1 {
		t.Errorf("c1: expected 1 message, got %d", got)
	}
	if got := len(ws.sentTo("c2")); got != 1 {
		t.Errorf("c2: expected 1 message, got %d", got)
	}
	if got := len(ws.sentTo("c3")); got != 0 {
		t.Errorf("c3: expected 0 messages, got %d", got)
	}
}

func TestForwarding_namespace_isolation(t *testing.T) {
	ps := newFakePubSub()
	ws := newFakeWS()
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("a-client", "ns-A")
	b.SetClientNamespace("b-client", "ns-B")

	_ = b.Add(context.Background(), "ns-A", "a-client", "shared-topic")
	_ = b.Add(context.Background(), "ns-B", "b-client", "shared-topic")

	// Deliver only to ns-A's view; ns-B has its own (separate fake) sub.
	// In production they'd be distinct topics in libp2p too because of
	// namespacing — here our fake just keys by topic string. Verify the
	// per-namespace routing table delivers correctly.
	b.forward("ns-A", "shared-topic", []byte("a-only"))

	if got := len(ws.sentTo("a-client")); got != 1 {
		t.Errorf("a-client: expected 1 message, got %d", got)
	}
	if got := len(ws.sentTo("b-client")); got != 0 {
		t.Errorf("b-client: expected 0 messages (different namespace), got %d", got)
	}
}

func TestForwarding_slow_client_does_not_block_others(t *testing.T) {
	ps := newFakePubSub()
	ws := newFakeWS()
	ws.failFor["slow"] = true
	b := New(ps, ws, zap.NewNop())
	b.SetClientNamespace("slow", "ns")
	b.SetClientNamespace("fast", "ns")

	_ = b.Add(context.Background(), "ns", "slow", "topic-A")
	_ = b.Add(context.Background(), "ns", "fast", "topic-A")

	ps.deliver("topic-A", []byte("hi"))

	if got := len(ws.sentTo("fast")); got != 1 {
		t.Errorf("fast client should receive even when slow fails, got %d", got)
	}
}

func TestAdd_namespace_required(t *testing.T) {
	b := New(newFakePubSub(), newFakeWS(), zap.NewNop())
	if err := b.Add(context.Background(), "", "c1", "t"); err == nil {
		t.Error("expected error for empty namespace")
	}
	if err := b.Add(context.Background(), "ns", "", "t"); err == nil {
		t.Error("expected error for empty client_id")
	}
	if err := b.Add(context.Background(), "ns", "c", ""); err == nil {
		t.Error("expected error for empty topic")
	}
}

func TestAdd_per_client_topic_cap(t *testing.T) {
	b := New(newFakePubSub(), newFakeWS(), zap.NewNop())
	b.SetClientNamespace("c1", "ns")
	// Saturate the cap.
	for i := 0; i < MaxTopicsPerClient; i++ {
		topic := "t-" + string(rune(i))
		if err := b.Add(context.Background(), "ns", "c1", topic); err != nil {
			t.Fatalf("Add %d failed: %v", i, err)
		}
	}
	// Cap+1 should be rejected.
	err := b.Add(context.Background(), "ns", "c1", "one-too-many")
	if err == nil {
		t.Error("expected per-client topic cap rejection")
	}
}

func TestSetGetClientNamespace(t *testing.T) {
	b := New(nil, nil, zap.NewNop())
	b.SetClientNamespace("c1", "ns-A")

	ns, ok := b.GetClientNamespace("c1")
	if !ok || ns != "ns-A" {
		t.Errorf("expected ns-A, got %q (ok=%v)", ns, ok)
	}

	if _, ok := b.GetClientNamespace("unknown"); ok {
		t.Error("expected GetClientNamespace to return false for unknown client")
	}
}

func TestConcurrent_add_remove_no_race(t *testing.T) {
	// Run with -race
	b := New(newFakePubSub(), newFakeWS(), zap.NewNop())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			cid := "c-" + string(rune('A'+gid))
			b.SetClientNamespace(cid, "ns")
			for i := 0; i < 50; i++ {
				topic := "t-" + string(rune(i%10))
				_ = b.Add(context.Background(), "ns", cid, topic)
				_ = b.Remove(context.Background(), "ns", cid, topic)
			}
		}(g)
	}
	wg.Wait()
}
