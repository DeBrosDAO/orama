package triggers

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// fakePubSubManager implements dispatcherPubSub for unit tests. Tracks
// Subscribe/Unsubscribe calls in order so tests can assert exact behavior
// without standing up a real libp2p host.
type fakePubSubManager struct {
	mu               sync.Mutex
	subscribed       map[string]pubsub.MessageHandler // topic → handler
	subscribeErr     func(topic string) error
	subscribeCalls   []string
	unsubscribeCalls []string
}

func newFakePubSubManager() *fakePubSubManager {
	return &fakePubSubManager{subscribed: map[string]pubsub.MessageHandler{}}
}

func (f *fakePubSubManager) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribeErr != nil {
		if err := f.subscribeErr(topic); err != nil {
			return err
		}
	}
	f.subscribed[topic] = handler
	f.subscribeCalls = append(f.subscribeCalls, topic)
	return nil
}

func (f *fakePubSubManager) Unsubscribe(ctx context.Context, topic string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subscribed, topic)
	f.unsubscribeCalls = append(f.unsubscribeCalls, topic)
	return nil
}

func (f *fakePubSubManager) subscribedTopics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.subscribed))
	for t := range f.subscribed {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// fakeTopicLister implements the topicLister interface so Refresh's real
// code path can be exercised without standing up an rqlite client. The
// `subs` field is what ListDistinctTopicPatterns returns; tests mutate it
// between Refresh calls to drive add/remove diffs.
type fakeTopicLister struct {
	subs    []DistinctTopicSubscription
	listErr error
	calls   int
}

func (l *fakeTopicLister) ListDistinctTopicPatterns(ctx context.Context) ([]DistinctTopicSubscription, error) {
	l.calls++
	if l.listErr != nil {
		return nil, l.listErr
	}
	return append([]DistinctTopicSubscription(nil), l.subs...), nil
}

// newDispatcherForRefreshTest builds a PubSubDispatcher with the fake
// topic lister and fake pubsub manager swapped in. Returns the dispatcher
// plus both fakes so tests can mutate the trigger set and assert behavior.
func newDispatcherForRefreshTest(initialSubs []DistinctTopicSubscription) (*PubSubDispatcher, *fakeTopicLister, *fakePubSubManager) {
	ps := newFakePubSubManager()
	lister := &fakeTopicLister{subs: initialSubs}
	d := NewPubSubDispatcher(nil, nil, nil, ps, zap.NewNop())
	// Swap the topicLister with our fake — the constructor defaulted it to
	// the (nil) store. This is the seam that makes Refresh exercisable in
	// unit tests without an rqlite dependency.
	d.topicLister = lister
	return d, lister, ps
}

// TestRefresh_subscribesNewLiteralTopics — happy path. Triggers added to
// the store result in libp2p subscribes for their literal topics on the
// next Refresh. Regression guard for bugboard #282 — without the fix,
// dispatcher.Start never subscribed and every WASM publish silently
// dropped every trigger handler.
func TestRefresh_subscribesNewLiteralTopics(t *testing.T) {
	d, _, ps := newDispatcherForRefreshTest([]DistinctTopicSubscription{
		{Namespace: "anchat", TopicPattern: "messages:new", Wildcard: false},
		{Namespace: "anchat", TopicPattern: "conversations:updated", Wildcard: false},
		{Namespace: "anchat", TopicPattern: "messages:*", Wildcard: true},
	})

	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got := ps.subscribedTopics()
	want := []string{"conversations:updated", "messages:new"}
	if !equalStrings(got, want) {
		t.Errorf("subscribed topics = %v, want %v (wildcard 'messages:*' must be skipped)", got, want)
	}

	// subscribedKeys should track both namespaced keys.
	d.subMu.Lock()
	defer d.subMu.Unlock()
	if !d.subscribedKeys[subKey("anchat", "messages:new")] {
		t.Error("subscribedKeys missing messages:new")
	}
	if !d.subscribedKeys[subKey("anchat", "conversations:updated")] {
		t.Error("subscribedKeys missing conversations:updated")
	}
	if d.subscribedKeys[subKey("anchat", "messages:*")] {
		t.Error("subscribedKeys should NOT contain wildcard 'messages:*'")
	}
}

// TestRefresh_unsubscribesRemovedTopics — diff path. Triggers removed
// from the store (so their topic disappears from ListDistinct...) are
// unsubscribed on the next Refresh.
func TestRefresh_unsubscribesRemovedTopics(t *testing.T) {
	d, lister, ps := newDispatcherForRefreshTest([]DistinctTopicSubscription{
		{Namespace: "ns", TopicPattern: "old-topic"},
		{Namespace: "ns", TopicPattern: "still-here"},
	})

	// First Refresh — both subscribed.
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if got, want := ps.subscribedTopics(), []string{"old-topic", "still-here"}; !equalStrings(got, want) {
		t.Fatalf("after first refresh: subscribed = %v, want %v", got, want)
	}

	// Simulate trigger removal — only one remains.
	lister.subs = []DistinctTopicSubscription{
		{Namespace: "ns", TopicPattern: "still-here"},
	}

	// Second Refresh — old-topic should be unsubscribed.
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if len(ps.unsubscribeCalls) != 1 || ps.unsubscribeCalls[0] != "old-topic" {
		t.Errorf("unsubscribe calls = %v, want [old-topic]", ps.unsubscribeCalls)
	}
	if got, want := ps.subscribedTopics(), []string{"still-here"}; !equalStrings(got, want) {
		t.Errorf("after prune: subscribed = %v, want %v", got, want)
	}
}

// TestRefresh_skipsAlreadySubscribed — idempotency. Calling Refresh
// twice with the same trigger set must NOT re-subscribe.
func TestRefresh_skipsAlreadySubscribed(t *testing.T) {
	d, _, ps := newDispatcherForRefreshTest([]DistinctTopicSubscription{
		{Namespace: "ns", TopicPattern: "topic-a"},
	})

	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	if len(ps.subscribeCalls) != 1 {
		t.Errorf("expected 1 subscribe call total (idempotent), got %d: %v",
			len(ps.subscribeCalls), ps.subscribeCalls)
	}
}

// TestRefresh_subscribeErrorDoesNotBlockOtherTopics — a single Subscribe
// failure must not abort the refresh for other topics. One bad topic
// shouldn't take down every other handler.
func TestRefresh_subscribeErrorDoesNotBlockOtherTopics(t *testing.T) {
	d, _, ps := newDispatcherForRefreshTest([]DistinctTopicSubscription{
		{Namespace: "ns", TopicPattern: "ok-1"},
		{Namespace: "ns", TopicPattern: "broken-topic"},
		{Namespace: "ns", TopicPattern: "ok-2"},
	})
	ps.subscribeErr = func(topic string) error {
		if topic == "broken-topic" {
			return errors.New("simulated libp2p failure")
		}
		return nil
	}

	if err := d.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if got, want := ps.subscribedTopics(), []string{"ok-1", "ok-2"}; !equalStrings(got, want) {
		t.Errorf("subscribed = %v, want %v (broken-topic should fail-soft)", got, want)
	}

	// subscribedKeys must NOT contain the failed topic so the next Refresh
	// retries it. Verifies the rollback-on-error path.
	d.subMu.Lock()
	defer d.subMu.Unlock()
	if d.subscribedKeys[subKey("ns", "broken-topic")] {
		t.Error("subscribedKeys must NOT include broken-topic (so next Refresh retries)")
	}
}

// TestRefresh_listError_propagates verifies that a transport error from
// the trigger store (e.g. rqlite unreachable) returns an error from
// Refresh rather than silently doing nothing.
func TestRefresh_listError_propagates(t *testing.T) {
	d, lister, _ := newDispatcherForRefreshTest(nil)
	lister.listErr = errors.New("rqlite unavailable")

	err := d.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error from Refresh when store fails, got nil")
	}
	if !errors.Is(err, lister.listErr) && err.Error() != lister.listErr.Error() {
		t.Errorf("expected wrapped store error, got: %v", err)
	}
}

// TestNewPubSubDispatcher_nilPubsubIsAllowed — constructs cleanly when
// pubsub manager is nil. Subsequent Start/Refresh must be no-ops, and
// the store must NOT be queried (since there's no point subscribing).
func TestNewPubSubDispatcher_nilPubsubIsAllowed(t *testing.T) {
	d := NewPubSubDispatcher(nil, nil, nil, nil, zap.NewNop())
	if d == nil {
		t.Fatal("constructor returned nil")
	}
	// Swap in a fake lister so we can assert it isn't called.
	fakeLister := &fakeTopicLister{}
	d.topicLister = fakeLister

	if err := d.Start(context.Background()); err != nil {
		t.Errorf("Start with nil pubsub returned error: %v", err)
	}
	if err := d.Refresh(context.Background()); err != nil {
		t.Errorf("Refresh with nil pubsub returned error: %v", err)
	}
	if fakeLister.calls != 0 {
		t.Errorf("topic lister should NOT be called when pubsub is nil, got %d calls", fakeLister.calls)
	}
	// Stop is idempotent (two close on stopCh would panic; stopOnce guards it).
	d.Stop()
	d.Stop()
}

// TestSubKey verifies the (namespace, topic) tuple key format is stable —
// the Refresh diff logic depends on consistent key construction.
func TestSubKey(t *testing.T) {
	cases := []struct {
		ns, topic, want string
	}{
		{"anchat", "messages:new", "anchat|messages:new"},
		{"", "topic-only", "|topic-only"},
		{"ns", "", "ns|"},
	}
	for _, c := range cases {
		if got := subKey(c.ns, c.topic); got != c.want {
			t.Errorf("subKey(%q, %q) = %q, want %q", c.ns, c.topic, got, c.want)
		}
	}
}

// equalStrings is a tiny helper for slice-equality assertions (order-sensitive).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
