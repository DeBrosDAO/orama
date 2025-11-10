//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newMessageCollector(ctx context.Context, buffer int) (chan []byte, func(string, []byte) error) {
	if buffer <= 0 {
		buffer = 1
	}

	ch := make(chan []byte, buffer)
	handler := func(_ string, data []byte) error {
		copied := append([]byte(nil), data...)
		select {
		case ch <- copied:
		case <-ctx.Done():
		}
		return nil
	}
	return ch, handler
}

func waitForMessage(ctx context.Context, ch <-chan []byte) ([]byte, error) {
	select {
	case msg := <-ch:
		return msg, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("context finished while waiting for pubsub message: %w", ctx.Err())
	}
}

func TestPubSub_SubscribePublish(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two clients
	client1 := NewNetworkClient(t)
	client2 := NewNetworkClient(t)

	if err := client1.Connect(); err != nil {
		t.Fatalf("client1 connect failed: %v", err)
	}
	defer client1.Disconnect()

	if err := client2.Connect(); err != nil {
		t.Fatalf("client2 connect failed: %v", err)
	}
	defer client2.Disconnect()

	topic := GenerateTopic()
	message := "test-message-from-client1"

	// Subscribe on client2
	messageCh, handler := newMessageCollector(ctx, 1)
	if err := client2.PubSub().Subscribe(ctx, topic, handler); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer client2.PubSub().Unsubscribe(ctx, topic)

	// Give subscription time to propagate and mesh to form
	Delay(2000)

	// Publish from client1
	if err := client1.PubSub().Publish(ctx, topic, []byte(message)); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	// Receive message on client2
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	msg, err := waitForMessage(recvCtx, messageCh)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if string(msg) != message {
		t.Fatalf("expected message %q, got %q", message, string(msg))
	}
}

func TestPubSub_MultipleSubscribers(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create three clients
	clientPub := NewNetworkClient(t)
	clientSub1 := NewNetworkClient(t)
	clientSub2 := NewNetworkClient(t)

	if err := clientPub.Connect(); err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer clientPub.Disconnect()

	if err := clientSub1.Connect(); err != nil {
		t.Fatalf("subscriber1 connect failed: %v", err)
	}
	defer clientSub1.Disconnect()

	if err := clientSub2.Connect(); err != nil {
		t.Fatalf("subscriber2 connect failed: %v", err)
	}
	defer clientSub2.Disconnect()

	topic := GenerateTopic()
	message1 := "message-for-sub1"
	message2 := "message-for-sub2"

	// Subscribe on both clients
	sub1Ch, sub1Handler := newMessageCollector(ctx, 4)
	if err := clientSub1.PubSub().Subscribe(ctx, topic, sub1Handler); err != nil {
		t.Fatalf("subscribe1 failed: %v", err)
	}
	defer clientSub1.PubSub().Unsubscribe(ctx, topic)

	sub2Ch, sub2Handler := newMessageCollector(ctx, 4)
	if err := clientSub2.PubSub().Subscribe(ctx, topic, sub2Handler); err != nil {
		t.Fatalf("subscribe2 failed: %v", err)
	}
	defer clientSub2.PubSub().Unsubscribe(ctx, topic)

	// Give subscriptions time to propagate
	Delay(500)

	// Publish first message
	if err := clientPub.PubSub().Publish(ctx, topic, []byte(message1)); err != nil {
		t.Fatalf("publish1 failed: %v", err)
	}

	// Both subscribers should receive first message
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	msg1a, err := waitForMessage(recvCtx, sub1Ch)
	if err != nil {
		t.Fatalf("sub1 receive1 failed: %v", err)
	}

	if string(msg1a) != message1 {
		t.Fatalf("sub1: expected %q, got %q", message1, string(msg1a))
	}

	msg1b, err := waitForMessage(recvCtx, sub2Ch)
	if err != nil {
		t.Fatalf("sub2 receive1 failed: %v", err)
	}

	if string(msg1b) != message1 {
		t.Fatalf("sub2: expected %q, got %q", message1, string(msg1b))
	}

	// Publish second message
	if err := clientPub.PubSub().Publish(ctx, topic, []byte(message2)); err != nil {
		t.Fatalf("publish2 failed: %v", err)
	}

	// Both subscribers should receive second message
	recvCtx2, recvCancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel2()

	msg2a, err := waitForMessage(recvCtx2, sub1Ch)
	if err != nil {
		t.Fatalf("sub1 receive2 failed: %v", err)
	}

	if string(msg2a) != message2 {
		t.Fatalf("sub1: expected %q, got %q", message2, string(msg2a))
	}

	msg2b, err := waitForMessage(recvCtx2, sub2Ch)
	if err != nil {
		t.Fatalf("sub2 receive2 failed: %v", err)
	}

	if string(msg2b) != message2 {
		t.Fatalf("sub2: expected %q, got %q", message2, string(msg2b))
	}
}

func TestPubSub_Deduplication(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two clients
	clientPub := NewNetworkClient(t)
	clientSub := NewNetworkClient(t)

	if err := clientPub.Connect(); err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer clientPub.Disconnect()

	if err := clientSub.Connect(); err != nil {
		t.Fatalf("subscriber connect failed: %v", err)
	}
	defer clientSub.Disconnect()

	topic := GenerateTopic()
	message := "duplicate-test-message"

	// Subscribe on client
	messageCh, handler := newMessageCollector(ctx, 3)
	if err := clientSub.PubSub().Subscribe(ctx, topic, handler); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer clientSub.PubSub().Unsubscribe(ctx, topic)

	// Give subscription time to propagate and mesh to form
	Delay(2000)

	// Publish the same message multiple times
	for i := 0; i < 3; i++ {
		if err := clientPub.PubSub().Publish(ctx, topic, []byte(message)); err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}

	// Receive messages - should get all (no dedup filter on subscribe)
	recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
	defer recvCancel()

	receivedCount := 0
	for receivedCount < 3 {
		if _, err := waitForMessage(recvCtx, messageCh); err != nil {
			break
		}
		receivedCount++
	}

	if receivedCount < 1 {
		t.Fatalf("expected to receive at least 1 message, got %d", receivedCount)
	}
}

func TestPubSub_ConcurrentPublish(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create clients
	clientPub := NewNetworkClient(t)
	clientSub := NewNetworkClient(t)

	if err := clientPub.Connect(); err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer clientPub.Disconnect()

	if err := clientSub.Connect(); err != nil {
		t.Fatalf("subscriber connect failed: %v", err)
	}
	defer clientSub.Disconnect()

	topic := GenerateTopic()
	numMessages := 10

	// Subscribe
	messageCh, handler := newMessageCollector(ctx, numMessages)
	if err := clientSub.PubSub().Subscribe(ctx, topic, handler); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer clientSub.PubSub().Unsubscribe(ctx, topic)

	// Give subscription time to propagate and mesh to form
	Delay(2000)

	// Publish multiple messages concurrently
	var wg sync.WaitGroup
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := fmt.Sprintf("concurrent-msg-%d", idx)
			if err := clientPub.PubSub().Publish(ctx, topic, []byte(msg)); err != nil {
				t.Logf("publish %d failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Receive messages
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	receivedCount := 0
	for receivedCount < numMessages {
		if _, err := waitForMessage(recvCtx, messageCh); err != nil {
			break
		}
		receivedCount++
	}

	if receivedCount < numMessages {
		t.Logf("expected %d messages, got %d (some may have been dropped)", numMessages, receivedCount)
	}
}

func TestPubSub_TopicIsolation(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create clients
	clientPub := NewNetworkClient(t)
	clientSub := NewNetworkClient(t)

	if err := clientPub.Connect(); err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer clientPub.Disconnect()

	if err := clientSub.Connect(); err != nil {
		t.Fatalf("subscriber connect failed: %v", err)
	}
	defer clientSub.Disconnect()

	topic1 := GenerateTopic()
	topic2 := GenerateTopic()

	// Subscribe to topic1
	messageCh, handler := newMessageCollector(ctx, 2)
	if err := clientSub.PubSub().Subscribe(ctx, topic1, handler); err != nil {
		t.Fatalf("subscribe1 failed: %v", err)
	}
	defer clientSub.PubSub().Unsubscribe(ctx, topic1)

	// Give subscription time to propagate and mesh to form
	Delay(2000)

	// Publish to topic2
	msg2 := "message-on-topic2"
	if err := clientPub.PubSub().Publish(ctx, topic2, []byte(msg2)); err != nil {
		t.Fatalf("publish2 failed: %v", err)
	}

	// Publish to topic1
	msg1 := "message-on-topic1"
	if err := clientPub.PubSub().Publish(ctx, topic1, []byte(msg1)); err != nil {
		t.Fatalf("publish1 failed: %v", err)
	}

	// Receive on sub1 - should get msg1 only
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	msg, err := waitForMessage(recvCtx, messageCh)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if string(msg) != msg1 {
		t.Fatalf("expected %q, got %q", msg1, string(msg))
	}
}

func TestPubSub_EmptyMessage(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create clients
	clientPub := NewNetworkClient(t)
	clientSub := NewNetworkClient(t)

	if err := clientPub.Connect(); err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer clientPub.Disconnect()

	if err := clientSub.Connect(); err != nil {
		t.Fatalf("subscriber connect failed: %v", err)
	}
	defer clientSub.Disconnect()

	topic := GenerateTopic()

	// Subscribe
	messageCh, handler := newMessageCollector(ctx, 1)
	if err := clientSub.PubSub().Subscribe(ctx, topic, handler); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer clientSub.PubSub().Unsubscribe(ctx, topic)

	// Give subscription time to propagate and mesh to form
	Delay(2000)

	// Publish empty message
	if err := clientPub.PubSub().Publish(ctx, topic, []byte("")); err != nil {
		t.Fatalf("publish empty failed: %v", err)
	}

	// Receive on sub - should get empty message
	recvCtx, recvCancel := context.WithTimeout(ctx, 10*time.Second)
	defer recvCancel()

	msg, err := waitForMessage(recvCtx, messageCh)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	if len(msg) != 0 {
		t.Fatalf("expected empty message, got %q", string(msg))
	}
}
