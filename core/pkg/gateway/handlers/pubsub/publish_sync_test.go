package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func publishRequest(ctx context.Context, topic, payload string) *http.Request {
	body, _ := json.Marshal(PublishRequest{Topic: topic, DataB64: base64.StdEncoding.EncodeToString([]byte(payload))})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body)).WithContext(ctx)
	return withNamespace(req, "test-ns")
}

// One client's sequential publishes to a topic reach a subscriber in the order
// they were sent, even when an earlier publish is slower than a later one.
func TestPublishHandler_sequential_publishes_arrive_in_order(t *testing.T) {
	const n = 20
	var mu sync.Mutex
	var delivered []string
	var calls int32
	mock := &mockPubSubClient{PublishFunc: func(_ context.Context, _ string, data []byte) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			time.Sleep(50 * time.Millisecond) // the first hand-off is the slow one
		}
		mu.Lock()
		delivered = append(delivered, string(data))
		mu.Unlock()
		return nil
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		h.PublishHandler(rr, publishRequest(context.Background(), "chat", fmt.Sprintf("m%02d", i)))
		if rr.Code != http.StatusOK {
			t.Fatalf("publish %d: status %d, body %s", i, rr.Code, rr.Body.String())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != n {
		t.Fatalf("service saw %d messages when the last answer returned, want %d", len(delivered), n)
	}
	for i, got := range delivered {
		if want := fmt.Sprintf("m%02d", i); got != want {
			t.Fatalf("message %d arrived as %q, want %q (order: %v)", i, got, want, delivered)
		}
	}
}

// A publish the service refuses is not a 200: the publisher is told, and the
// body says why.
func TestPublishHandler_service_failure_is_reported(t *testing.T) {
	mock := &mockPubSubClient{PublishFunc: func(context.Context, string, []byte) error {
		return errors.New("dial unix /run/orama/pubsub.sock: connection refused")
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})
	fired := make(chan struct{}, 1)
	h.SetOnPublish(func(context.Context, string, string, []byte) { fired <- struct{}{} })

	rr := httptest.NewRecorder()
	h.PublishHandler(rr, publishRequest(context.Background(), "chat", "hello"))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body %s)", rr.Code, rr.Body.String())
	}
	msg, _ := decodeResponse(t, rr.Body)["error"].(string)
	if !strings.Contains(msg, "chat") {
		t.Errorf("error %q does not name the topic", msg)
	}
	// The node's internals are the operator's, in the log, not the tenant's.
	for _, leak := range []string{"orama-namespace-pubsub@index", "connection refused", "/run/orama"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q exposes %q", msg, leak)
		}
	}
	select {
	case <-fired:
		t.Error("a message that was not handed to the pubsub service fired its triggers")
	case <-time.After(50 * time.Millisecond):
	}
}

// A service that does not answer within the publish timeout is a 504, not a
// 200 and not a hang.
func TestPublishHandler_service_timeout_is_504(t *testing.T) {
	mock := &mockPubSubClient{PublishFunc: func(ctx context.Context, _ string, _ []byte) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})
	h.publishTimeout = 30 * time.Millisecond

	rr := httptest.NewRecorder()
	h.PublishHandler(rr, publishRequest(context.Background(), "chat", "hello"))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504 (body %s)", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeResponse(t, rr.Body)["error"].(string); !strings.Contains(msg, "30ms") {
		t.Errorf("error %q does not name the timeout", msg)
	}
}

// A caller that goes away mid-publish cancels the hand-off; the handler
// returns promptly and does not report success.
func TestPublishHandler_cancelled_mid_publish(t *testing.T) {
	started := make(chan struct{})
	mock := &mockPubSubClient{PublishFunc: func(ctx context.Context, _ string, _ []byte) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.PublishHandler(rr, publishRequest(ctx, "chat", "hello"))
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request was cancelled")
	}
	if rr.Code == http.StatusOK {
		t.Fatalf("a cancelled publish answered 200 (body %s)", rr.Body.String())
	}
}

// The service call is made, and answered, inside the request, under a deadline.
func TestPublishHandler_success_answers_after_hand_off(t *testing.T) {
	var handedOff int32
	mock := &mockPubSubClient{PublishFunc: func(ctx context.Context, _ string, _ []byte) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("publish context has no deadline")
		}
		atomic.AddInt32(&handedOff, 1)
		return nil
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	rr := httptest.NewRecorder()
	h.PublishHandler(rr, publishRequest(context.Background(), "chat", "hello"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	if got := atomic.LoadInt32(&handedOff); got != 1 {
		t.Fatalf("service was called %d times before the answer, want 1", got)
	}
	if resp := decodeResponse(t, rr.Body); resp["status"] != "ok" || len(resp) != 1 {
		t.Errorf("response body %v, want {status: ok}", resp)
	}
}

func TestPublishBatchHandler_service_failure_is_reported(t *testing.T) {
	mock := &mockPubSubClient{PublishBatchFunc: func(context.Context, []client.TopicMessage, client.PublishBatchOptions) error {
		return errors.New("connection refused")
	}}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{{Topic: "a", DataB64: "AA=="}}})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 (body %s)", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeResponse(t, rr.Body)["error"].(string); !strings.Contains(msg, "not delivered") || strings.Contains(msg, "connection refused") {
		t.Errorf("error %q must say the batch was not delivered, without the node's internal cause", msg)
	}
}
