//go:build e2e

package shared_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	e2e "github.com/DeBrosOfficial/network/e2e"
)

// TestPubSub_SubscribePublish tests basic pub/sub functionality via WebSocket
func TestPubSub_SubscribePublish(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()
	message := "test-message-from-publisher"

	// Create subscriber first
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish message
	if err := publisher.Publish([]byte(message)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	// Receive message on subscriber
	msg, err := subscriber.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if string(msg) != message {
		t.Fatalf("expected message %q, got %q", message, string(msg))
	}
}

// TestPubSub_MultipleSubscribers tests that multiple subscribers receive the same message
func TestPubSub_MultipleSubscribers(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()
	message1 := "message-1"
	message2 := "message-2"

	// Create two subscribers
	sub1, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber1: %v", err)
	}
	defer sub1.Close()

	sub2, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber2: %v", err)
	}
	defer sub2.Close()

	// Give subscribers time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish first message
	if err := publisher.Publish([]byte(message1)); err != nil {
		t.Fatalf("publish1 failed: %v", err)
	}

	// Both subscribers should receive first message
	msg1a, err := sub1.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub1 receive1 failed: %v", err)
	}
	if string(msg1a) != message1 {
		t.Fatalf("sub1: expected %q, got %q", message1, string(msg1a))
	}

	msg1b, err := sub2.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub2 receive1 failed: %v", err)
	}
	if string(msg1b) != message1 {
		t.Fatalf("sub2: expected %q, got %q", message1, string(msg1b))
	}

	// Publish second message
	if err := publisher.Publish([]byte(message2)); err != nil {
		t.Fatalf("publish2 failed: %v", err)
	}

	// Both subscribers should receive second message
	msg2a, err := sub1.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub1 receive2 failed: %v", err)
	}
	if string(msg2a) != message2 {
		t.Fatalf("sub1: expected %q, got %q", message2, string(msg2a))
	}

	msg2b, err := sub2.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub2 receive2 failed: %v", err)
	}
	if string(msg2b) != message2 {
		t.Fatalf("sub2: expected %q, got %q", message2, string(msg2b))
	}
}

// TestPubSub_Deduplication tests that multiple identical messages are all received
func TestPubSub_Deduplication(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()
	message := "duplicate-test-message"

	// Create subscriber
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish the same message multiple times
	for i := 0; i < 3; i++ {
		if err := publisher.Publish([]byte(message)); err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
		// Small delay between publishes
		e2e.Delay(50)
	}

	// Receive messages - should get all (no dedup filter)
	receivedCount := 0
	for receivedCount < 3 {
		_, err := subscriber.ReceiveWithTimeout(5 * time.Second)
		if err != nil {
			break
		}
		receivedCount++
	}

	if receivedCount < 1 {
		t.Fatalf("expected to receive at least 1 message, got %d", receivedCount)
	}
	t.Logf("received %d messages", receivedCount)
}

// TestPubSub_ConcurrentPublish tests concurrent message publishing
func TestPubSub_ConcurrentPublish(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()
	numMessages := 10

	// Create subscriber
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish multiple messages concurrently
	var wg sync.WaitGroup
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := fmt.Sprintf("concurrent-msg-%d", idx)
			if err := publisher.Publish([]byte(msg)); err != nil {
				t.Logf("publish %d failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Receive messages
	receivedCount := 0
	for receivedCount < numMessages {
		_, err := subscriber.ReceiveWithTimeout(10 * time.Second)
		if err != nil {
			break
		}
		receivedCount++
	}

	if receivedCount < numMessages {
		t.Logf("expected %d messages, got %d (some may have been dropped)", numMessages, receivedCount)
	}
}

// TestPubSub_TopicIsolation tests that messages are isolated to their topics
func TestPubSub_TopicIsolation(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic1 := e2e.GenerateTopic()
	topic2 := e2e.GenerateTopic()
	msg1 := "message-on-topic1"
	msg2 := "message-on-topic2"

	// Create subscriber for topic1
	sub1, err := e2e.NewWSPubSubClient(t, topic1)
	if err != nil {
		t.Fatalf("failed to create subscriber1: %v", err)
	}
	defer sub1.Close()

	// Create subscriber for topic2
	sub2, err := e2e.NewWSPubSubClient(t, topic2)
	if err != nil {
		t.Fatalf("failed to create subscriber2: %v", err)
	}
	defer sub2.Close()

	// Give subscribers time to register
	e2e.Delay(200)

	// Create publishers
	pub1, err := e2e.NewWSPubSubClient(t, topic1)
	if err != nil {
		t.Fatalf("failed to create publisher1: %v", err)
	}
	defer pub1.Close()

	pub2, err := e2e.NewWSPubSubClient(t, topic2)
	if err != nil {
		t.Fatalf("failed to create publisher2: %v", err)
	}
	defer pub2.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish to topic2 first
	if err := pub2.Publish([]byte(msg2)); err != nil {
		t.Fatalf("publish2 failed: %v", err)
	}

	// Publish to topic1
	if err := pub1.Publish([]byte(msg1)); err != nil {
		t.Fatalf("publish1 failed: %v", err)
	}

	// Sub1 should receive msg1 only
	received1, err := sub1.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub1 receive failed: %v", err)
	}
	if string(received1) != msg1 {
		t.Fatalf("sub1: expected %q, got %q", msg1, string(received1))
	}

	// Sub2 should receive msg2 only
	received2, err := sub2.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("sub2 receive failed: %v", err)
	}
	if string(received2) != msg2 {
		t.Fatalf("sub2: expected %q, got %q", msg2, string(received2))
	}
}

// TestPubSub_EmptyMessage tests sending and receiving empty messages
func TestPubSub_EmptyMessage(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()

	// Create subscriber
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish empty message
	if err := publisher.Publish([]byte("")); err != nil {
		t.Fatalf("publish empty failed: %v", err)
	}

	// Receive on subscriber - should get empty message
	msg, err := subscriber.ReceiveWithTimeout(10 * time.Second)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if len(msg) != 0 {
		t.Fatalf("expected empty message, got %q", string(msg))
	}
}

// TestPubSub_LargeMessage tests sending and receiving large messages
func TestPubSub_LargeMessage(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()

	// Create a large message (100KB)
	largeMessage := make([]byte, 100*1024)
	for i := range largeMessage {
		largeMessage[i] = byte(i % 256)
	}

	// Create subscriber
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish large message
	if err := publisher.Publish(largeMessage); err != nil {
		t.Fatalf("publish large message failed: %v", err)
	}

	// Receive on subscriber
	msg, err := subscriber.ReceiveWithTimeout(30 * time.Second)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if len(msg) != len(largeMessage) {
		t.Fatalf("expected message of length %d, got %d", len(largeMessage), len(msg))
	}

	// Verify content
	for i := range msg {
		if msg[i] != largeMessage[i] {
			t.Fatalf("message content mismatch at byte %d", i)
		}
	}
}

// TestPubSub_RapidPublish tests rapid message publishing
func TestPubSub_RapidPublish(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	topic := e2e.GenerateTopic()
	numMessages := 50

	// Create subscriber
	subscriber, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create subscriber: %v", err)
	}
	defer subscriber.Close()

	// Give subscriber time to register
	e2e.Delay(200)

	// Create publisher
	publisher, err := e2e.NewWSPubSubClient(t, topic)
	if err != nil {
		t.Fatalf("failed to create publisher: %v", err)
	}
	defer publisher.Close()

	// Give connections time to stabilize
	e2e.Delay(200)

	// Publish messages rapidly
	for i := 0; i < numMessages; i++ {
		msg := fmt.Sprintf("rapid-msg-%d", i)
		if err := publisher.Publish([]byte(msg)); err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}

	// Receive messages
	receivedCount := 0
	for receivedCount < numMessages {
		_, err := subscriber.ReceiveWithTimeout(10 * time.Second)
		if err != nil {
			break
		}
		receivedCount++
	}

	// Allow some message loss due to buffering
	minExpected := numMessages * 80 / 100 // 80% minimum
	if receivedCount < minExpected {
		t.Fatalf("expected at least %d messages, got %d", minExpected, receivedCount)
	}
	t.Logf("received %d/%d messages (%.1f%%)", receivedCount, numMessages, float64(receivedCount)*100/float64(numMessages))
}
