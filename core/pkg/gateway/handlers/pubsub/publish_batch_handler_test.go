package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func TestPublishBatchHandler_invalid_method(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/pubsub/publish-batch", nil), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestPublishBatchHandler_missing_namespace(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{{Topic: "a", DataB64: "AA=="}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestPublishBatchHandler_empty_messages_rejected(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{}})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty messages, got %d", rr.Code)
	}
}

func TestPublishBatchHandler_oversize_batch_rejected(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	entries := make([]PublishBatchEntry, MaxPublishBatchSize+1)
	for i := range entries {
		entries[i] = PublishBatchEntry{Topic: "t", DataB64: "AA=="}
	}
	body, _ := json.Marshal(PublishBatchRequest{Messages: entries})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversize batch, got %d", rr.Code)
	}
}

func TestPublishBatchHandler_invalid_base64_rejected(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{
		{Topic: "good", DataB64: base64.StdEncoding.EncodeToString([]byte("ok"))},
		{Topic: "bad", DataB64: "!!!not-base64"},
	}})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid base64, got %d", rr.Code)
	}
}

func TestPublishBatchHandler_missing_topic_rejected(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{
		{Topic: "", DataB64: base64.StdEncoding.EncodeToString([]byte("x"))},
	}})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing topic, got %d", rr.Code)
	}
}

func TestPublishBatchHandler_happy_calls_PublishBatch(t *testing.T) {
	var (
		called      int32
		gotMessages []client.TopicMessage
		mu          sync.Mutex
	)
	mock := &mockPubSubClient{
		PublishBatchFunc: func(ctx context.Context, msgs []client.TopicMessage, opts client.PublishBatchOptions) error {
			atomic.AddInt32(&called, 1)
			mu.Lock()
			gotMessages = append(gotMessages, msgs...)
			mu.Unlock()
			return nil
		},
	}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	entries := []PublishBatchEntry{
		{Topic: "a", DataB64: base64.StdEncoding.EncodeToString([]byte("data-a"))},
		{Topic: "b", DataB64: base64.StdEncoding.EncodeToString([]byte("data-b"))},
	}
	body, _ := json.Marshal(PublishBatchRequest{Messages: entries})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "test-ns")
	rr := httptest.NewRecorder()

	h.PublishBatchHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	// PublishBatch is invoked from a goroutine; give it a moment to run.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&called) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("PublishBatch was not called within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotMessages) != 2 {
		t.Fatalf("expected 2 messages forwarded, got %d", len(gotMessages))
	}
	if gotMessages[0].Topic != "a" || string(gotMessages[0].Data) != "data-a" {
		t.Errorf("unexpected first message: %+v", gotMessages[0])
	}
	if gotMessages[1].Topic != "b" || string(gotMessages[1].Data) != "data-b" {
		t.Errorf("unexpected second message: %+v", gotMessages[1])
	}
}
