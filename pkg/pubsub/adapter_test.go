package pubsub

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p"
	ps "github.com/libp2p/go-libp2p-pubsub"
	"go.uber.org/zap"
)

// createTestAdapter creates a ClientAdapter backed by a real libp2p host for testing.
func createTestAdapter(t *testing.T, ns string) (*ClientAdapter, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("failed to create libp2p host: %v", err)
	}

	gossip, err := ps.NewGossipSub(ctx, h)
	if err != nil {
		h.Close()
		cancel()
		t.Fatalf("failed to create gossipsub: %v", err)
	}

	adapter := NewClientAdapter(gossip, ns, zap.NewNop())

	cleanup := func() {
		adapter.Close()
		h.Close()
		cancel()
	}

	return adapter, cleanup
}

func TestNewClientAdapter(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
	if adapter.manager == nil {
		t.Fatal("expected non-nil manager inside adapter")
	}
	if adapter.manager.namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", adapter.manager.namespace)
	}
}

func TestClientAdapter_ListTopics_Empty(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	topics, err := adapter.ListTopics(context.Background())
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics, got %d: %v", len(topics), topics)
	}
}

func TestClientAdapter_ListTopics(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	ctx := context.Background()

	// Subscribe to a topic
	err := adapter.Subscribe(ctx, "chat", func(topic string, data []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// List topics - should contain "chat"
	topics, err := adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("expected 1 topic, got %d: %v", len(topics), topics)
	}
	if topics[0] != "chat" {
		t.Errorf("expected topic 'chat', got %q", topics[0])
	}
}

func TestClientAdapter_ListTopics_Multiple(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	ctx := context.Background()
	handler := func(topic string, data []byte) error { return nil }

	// Subscribe to multiple topics
	for _, topic := range []string{"chat", "events", "notifications"} {
		if err := adapter.Subscribe(ctx, topic, handler); err != nil {
			t.Fatalf("Subscribe(%q) failed: %v", topic, err)
		}
	}

	topics, err := adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 3 {
		t.Fatalf("expected 3 topics, got %d: %v", len(topics), topics)
	}

	// Check all expected topics are present (order may vary)
	found := map[string]bool{}
	for _, topic := range topics {
		found[topic] = true
	}
	for _, expected := range []string{"chat", "events", "notifications"} {
		if !found[expected] {
			t.Errorf("expected topic %q not found in %v", expected, topics)
		}
	}
}

func TestClientAdapter_SubscribeAndUnsubscribe(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	ctx := context.Background()
	topic := "my-topic"

	// Subscribe
	err := adapter.Subscribe(ctx, topic, func(t string, d []byte) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Verify subscription exists
	topics, err := adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 1 || topics[0] != topic {
		t.Fatalf("expected [%s], got %v", topic, topics)
	}

	// Unsubscribe
	err = adapter.Unsubscribe(ctx, topic)
	if err != nil {
		t.Fatalf("Unsubscribe failed: %v", err)
	}

	// Verify subscription is removed
	topics, err = adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics after unsubscribe failed: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics after unsubscribe, got %d: %v", len(topics), topics)
	}
}

func TestClientAdapter_UnsubscribeNonexistent(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	// Unsubscribe from a topic that was never subscribed - should not error
	err := adapter.Unsubscribe(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("Unsubscribe on nonexistent topic returned error: %v", err)
	}
}

func TestClientAdapter_Publish(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	ctx := context.Background()

	// Publishing to a topic should not error even without subscribers
	err := adapter.Publish(ctx, "chat", []byte("hello"))
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
}

func TestClientAdapter_Close(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "test-ns")
	defer cleanup()

	ctx := context.Background()
	handler := func(topic string, data []byte) error { return nil }

	// Subscribe to some topics
	_ = adapter.Subscribe(ctx, "topic-a", handler)
	_ = adapter.Subscribe(ctx, "topic-b", handler)

	// Close should clean up all subscriptions
	err := adapter.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After close, listing topics should return empty
	topics, err := adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics after Close failed: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics after Close, got %d: %v", len(topics), topics)
	}
}

func TestClientAdapter_NamespaceOverrideViaContext(t *testing.T) {
	adapter, cleanup := createTestAdapter(t, "default-ns")
	defer cleanup()

	ctx := context.Background()
	overrideCtx := WithNamespace(ctx, "custom-ns")
	handler := func(topic string, data []byte) error { return nil }

	// Subscribe with override namespace
	err := adapter.Subscribe(overrideCtx, "chat", handler)
	if err != nil {
		t.Fatalf("Subscribe with namespace override failed: %v", err)
	}

	// List with default namespace - should be empty since we subscribed under "custom-ns"
	topics, err := adapter.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics with default namespace failed: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics for default namespace, got %d: %v", len(topics), topics)
	}

	// List with override namespace - should see the topic
	topics, err = adapter.ListTopics(overrideCtx)
	if err != nil {
		t.Fatalf("ListTopics with override namespace failed: %v", err)
	}
	if len(topics) != 1 || topics[0] != "chat" {
		t.Errorf("expected [chat] for override namespace, got %v", topics)
	}
}
