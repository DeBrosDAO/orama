package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishBatch_empty_slice_returns_nil(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	if err := mgr.PublishBatch(context.Background(), nil, PublishBatchOptions{}); err != nil {
		t.Fatalf("expected nil error for empty slice, got: %v", err)
	}
	if err := mgr.PublishBatch(context.Background(), []TopicMessage{}, PublishBatchOptions{}); err != nil {
		t.Fatalf("expected nil error for empty slice, got: %v", err)
	}
}

func TestPublishBatch_happy_path(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	msgs := []TopicMessage{
		{Topic: "a", Data: []byte("data-a")},
		{Topic: "b", Data: []byte("data-b")},
		{Topic: "c", Data: []byte("data-c")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{}); err != nil {
		t.Fatalf("PublishBatch failed: %v", err)
	}
}

func TestPublishSame_uses_same_payload(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	topics := []string{"x", "y", "z"}
	data := []byte("shared")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.PublishSame(ctx, topics, data, PublishBatchOptions{}); err != nil {
		t.Fatalf("PublishSame failed: %v", err)
	}
}

func TestPublishSame_empty_returns_nil(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()
	if err := mgr.PublishSame(context.Background(), nil, []byte("x"), PublishBatchOptions{}); err != nil {
		t.Fatalf("expected nil for empty topics, got: %v", err)
	}
}

func TestPublishBatch_context_cancel_returns_error(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	msgs := make([]TopicMessage, 50)
	for i := range msgs {
		msgs[i] = TopicMessage{Topic: fmt.Sprintf("topic-%d", i), Data: []byte("d")}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{})
	if err == nil {
		t.Fatal("expected context.Canceled error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Logf("got error (acceptable as long as it's an error): %v", err)
	}
}

// TestPublish_does_not_block_on_empty_mesh is a regression guard for feat-6.
// Publish must NOT wait for gossipsub mesh formation: it previously polled
// ListPeers() for up to 2s, so every publish to a topic with no remote
// subscribers (the common namespace-gateway case, where wakeup topics are
// delivered to LOCAL WS clients) cost the full 2s — a 3-publish message-create
// paid ~6s server-side. FloodPublish delivers without the mesh, so a publish
// against an empty mesh must return promptly.
func TestPublish_does_not_block_on_empty_mesh(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	start := time.Now()
	if err := mgr.Publish(context.Background(), "no-subscribers", []byte("d")); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	// Old code: ~2000ms. New code: ~ms. 500ms is a generous ceiling that
	// avoids CI flakiness while still catching a re-introduced multi-second
	// mesh-wait.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Publish blocked %v on an empty mesh — the mesh-wait must stay removed (feat-6)", elapsed)
	}
}

func TestPublishBatch_concurrency_limit(t *testing.T) {
	// Verify PublishBatch with low MaxConcurrency completes without deadlocking.
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	msgs := make([]TopicMessage, 8)
	for i := range msgs {
		msgs[i] = TopicMessage{Topic: fmt.Sprintf("ct-%d", i), Data: []byte("d")}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{MaxConcurrency: 2}); err != nil {
		t.Fatalf("PublishBatch with low concurrency failed: %v", err)
	}
}

// TestPublishBatch_caps_concurrency_above_msg_count verifies that MaxConcurrency
// is clamped to len(msgs) — passing 100 with 3 messages should not panic on
// channel capacity.
func TestPublishBatch_caps_concurrency_above_msg_count(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	msgs := []TopicMessage{
		{Topic: "a", Data: []byte("1")},
		{Topic: "b", Data: []byte("2")},
		{Topic: "c", Data: []byte("3")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{MaxConcurrency: 100}); err != nil {
		t.Fatalf("PublishBatch failed: %v", err)
	}
}

func TestBatchError_Error_summarizes(t *testing.T) {
	be := &BatchError{Errors: map[string]error{
		"topic-a": errors.New("boom"),
		"topic-b": errors.New("kaboom"),
	}}
	s := be.Error()
	if s == "" {
		t.Fatal("expected non-empty error string")
	}
	// Should mention both topics.
	if !contains(s, "topic-a") || !contains(s, "topic-b") {
		t.Errorf("error string %q should mention both failing topics", s)
	}
}

func TestBatchError_Error_empty_map(t *testing.T) {
	be := &BatchError{}
	if s := be.Error(); s == "" {
		t.Fatal("expected non-empty string even for empty map")
	}
}

// contains is a tiny helper to avoid importing strings just for this.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestPublishBatch_concurrent_publishes_thread_safe ensures concurrent
// PublishBatch invocations don't race on internal state.
func TestPublishBatch_concurrent_publishes_thread_safe(t *testing.T) {
	mgr, cleanup := createTestManager(t, "test-ns")
	defer cleanup()

	const goroutines = 8
	const msgsPerGoroutine = 5

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var failures int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			msgs := make([]TopicMessage, msgsPerGoroutine)
			for i := range msgs {
				msgs[i] = TopicMessage{
					Topic: fmt.Sprintf("g%d-t%d", gid, i),
					Data:  []byte("d"),
				}
			}
			if err := mgr.PublishBatch(ctx, msgs, PublishBatchOptions{}); err != nil {
				atomic.AddInt64(&failures, 1)
				t.Logf("goroutine %d failed: %v", gid, err)
			}
		}(g)
	}
	wg.Wait()

	if failures > 0 {
		t.Errorf("%d concurrent batches failed", failures)
	}
}
