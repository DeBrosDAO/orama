package pubsub

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

func createTestManager(t *testing.T, ns string) (*Manager, func()) {
	ctx, cancel := context.WithCancel(context.Background())

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("failed to create libp2p host: %v", err)
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		h.Close()
		t.Fatalf("failed to create gossipsub: %v", err)
	}

	mgr := NewManager(ps, ns)

	cleanup := func() {
		mgr.Close()
		h.Close()
		cancel()
	}

	return mgr, cleanup
}

func TestManager_Namespacing(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	ctx := context.Background()
	topic := "my-topic"
	expectedNamespacedTopic := "test-ns.my-topic"

	// Subscribe
	err := mgr.Subscribe(ctx, topic, func(t string, d []byte) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	mgr.mu.RLock()
	_, exists := mgr.subscriptions[expectedNamespacedTopic]
	mgr.mu.RUnlock()

	if !exists {
		t.Errorf("expected subscription for %s to exist", expectedNamespacedTopic)
	}

	// Test override
	overrideNS := "other-ns"
	overrideCtx := context.WithValue(ctx, CtxKeyNamespaceOverride, overrideNS)
	expectedOverrideTopic := "other-ns.my-topic"

	err = mgr.Subscribe(overrideCtx, topic, func(t string, d []byte) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe with override failed: %v", err)
	}

	mgr.mu.RLock()
	_, exists = mgr.subscriptions[expectedOverrideTopic]
	mgr.mu.RUnlock()

	if !exists {
		t.Errorf("expected subscription for %s to exist", expectedOverrideTopic)
	}

	// Test ListTopics
	topics, err := mgr.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 1 || topics[0] != "my-topic" {
		t.Errorf("expected 1 topic [my-topic], got %v", topics)
	}

	topicsOverride, err := mgr.ListTopics(overrideCtx)
	if err != nil {
		t.Fatalf("ListTopics with override failed: %v", err)
	}
	if len(topicsOverride) != 1 || topicsOverride[0] != "my-topic" {
		t.Errorf("expected 1 topic [my-topic] with override, got %v", topicsOverride)
	}
}

func TestManager_RefCount(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	ctx := context.Background()
	topic := "ref-topic"
	namespacedTopic := "test-ns.ref-topic"

	h1 := func(t string, d []byte) error { return nil }
	h2 := func(t string, d []byte) error { return nil }

	// First subscription
	err := mgr.Subscribe(ctx, topic, h1)
	if err != nil {
		t.Fatalf("first subscribe failed: %v", err)
	}

	mgr.mu.RLock()
	ts := mgr.subscriptions[namespacedTopic]
	mgr.mu.RUnlock()

	if ts.refCount != 1 {
		t.Errorf("expected refCount 1, got %d", ts.refCount)
	}

	// Second subscription
	err = mgr.Subscribe(ctx, topic, h2)
	if err != nil {
		t.Fatalf("second subscribe failed: %v", err)
	}

	if ts.refCount != 2 {
		t.Errorf("expected refCount 2, got %d", ts.refCount)
	}

	// Unsubscribe one
	err = mgr.Unsubscribe(ctx, topic)
	if err != nil {
		t.Fatalf("unsubscribe 1 failed: %v", err)
	}

	if ts.refCount != 1 {
		t.Errorf("expected refCount 1 after one unsubscribe, got %d", ts.refCount)
	}

	mgr.mu.RLock()
	_, exists := mgr.subscriptions[namespacedTopic]
	mgr.mu.RUnlock()
	if !exists {
		t.Error("expected subscription to still exist")
	}

	// Unsubscribe second
	err = mgr.Unsubscribe(ctx, topic)
	if err != nil {
		t.Fatalf("unsubscribe 2 failed: %v", err)
	}

	mgr.mu.RLock()
	_, exists = mgr.subscriptions[namespacedTopic]
	mgr.mu.RUnlock()
	if exists {
		t.Error("expected subscription to be removed")
	}
}

func TestManager_PubSub(t *testing.T) {
	// For a real pubsub test between two managers, we need them to be connected
	ctx := context.Background()

	h1, _ := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	ps1, _ := pubsub.NewGossipSub(ctx, h1)
	mgr1 := NewManager(ps1, "test")
	defer h1.Close()
	defer mgr1.Close()

	h2, _ := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	ps2, _ := pubsub.NewGossipSub(ctx, h2)
	mgr2 := NewManager(ps2, "test")
	defer h2.Close()
	defer mgr2.Close()

	// Connect hosts
	h1.Peerstore().AddAddrs(h2.ID(), h2.Addrs(), time.Hour)
	err := h1.Connect(ctx, peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
	if err != nil {
		t.Fatalf("failed to connect hosts: %v", err)
	}

	topic := "chat"
	msgData := []byte("hello world")
	received := make(chan []byte, 1)

	err = mgr2.Subscribe(ctx, topic, func(t string, d []byte) error {
		received <- d
		return nil
	})
	if err != nil {
		t.Fatalf("mgr2 subscribe failed: %v", err)
	}

	// Wait for mesh to form (mgr1 needs to know about mgr2's subscription)
	// In a real network this happens via gossip. We'll just retry publish.
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

Loop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for message")
		case <-ticker.C:
			_ = mgr1.Publish(ctx, topic, msgData)
		case data := <-received:
			if string(data) != string(msgData) {
				t.Errorf("expected %s, got %s", string(msgData), string(data))
			}
			break Loop
		}
	}
}
