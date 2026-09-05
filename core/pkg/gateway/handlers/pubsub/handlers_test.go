package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/libp2p/go-libp2p/core/host"
	"go.uber.org/zap"
)

// --- Mocks ---

// mockPubSubClient implements client.PubSubClient for testing
type mockPubSubClient struct {
	PublishFunc      func(ctx context.Context, topic string, data []byte) error
	PublishBatchFunc func(ctx context.Context, msgs []client.TopicMessage, opts client.PublishBatchOptions) error
	PublishSameFunc  func(ctx context.Context, topics []string, data []byte, opts client.PublishBatchOptions) error
	SubscribeFunc    func(ctx context.Context, topic string, handler client.MessageHandler) error
	UnsubscribeFunc  func(ctx context.Context, topic string) error
	ListTopicsFunc   func(ctx context.Context) ([]string, error)
}

func (m *mockPubSubClient) Publish(ctx context.Context, topic string, data []byte) error {
	if m.PublishFunc != nil {
		return m.PublishFunc(ctx, topic, data)
	}
	return nil
}

func (m *mockPubSubClient) PublishBatch(ctx context.Context, msgs []client.TopicMessage, opts client.PublishBatchOptions) error {
	if m.PublishBatchFunc != nil {
		return m.PublishBatchFunc(ctx, msgs, opts)
	}
	return nil
}

func (m *mockPubSubClient) PublishSame(ctx context.Context, topics []string, data []byte, opts client.PublishBatchOptions) error {
	if m.PublishSameFunc != nil {
		return m.PublishSameFunc(ctx, topics, data, opts)
	}
	return nil
}

func (m *mockPubSubClient) Subscribe(ctx context.Context, topic string, handler client.MessageHandler) error {
	if m.SubscribeFunc != nil {
		return m.SubscribeFunc(ctx, topic, handler)
	}
	return nil
}

func (m *mockPubSubClient) Unsubscribe(ctx context.Context, topic string) error {
	if m.UnsubscribeFunc != nil {
		return m.UnsubscribeFunc(ctx, topic)
	}
	return nil
}

func (m *mockPubSubClient) ListTopics(ctx context.Context) ([]string, error) {
	if m.ListTopicsFunc != nil {
		return m.ListTopicsFunc(ctx)
	}
	return nil, nil
}

// mockNetworkClient implements client.NetworkClient for testing
type mockNetworkClient struct {
	pubsub client.PubSubClient
}

func (m *mockNetworkClient) Database() client.DatabaseClient { return nil }
func (m *mockNetworkClient) PubSub() client.PubSubClient     { return m.pubsub }
func (m *mockNetworkClient) Network() client.NetworkInfo     { return nil }
func (m *mockNetworkClient) Storage() client.StorageClient   { return nil }
func (m *mockNetworkClient) Connect() error                  { return nil }
func (m *mockNetworkClient) Disconnect() error               { return nil }
func (m *mockNetworkClient) Health() (*client.HealthStatus, error) {
	return &client.HealthStatus{Status: "healthy"}, nil
}
func (m *mockNetworkClient) Config() *client.ClientConfig { return nil }
func (m *mockNetworkClient) Host() host.Host              { return nil }

// --- Helpers ---

// newTestHandlers creates a PubSubHandlers with the given mock client for testing.
func newTestHandlers(nc client.NetworkClient) *PubSubHandlers {
	logger := &logging.ColoredLogger{Logger: zap.NewNop()}
	return NewPubSubHandlers(nc, logger)
}

// withNamespace adds a namespace to the request context.
func withNamespace(r *http.Request, ns string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, ns)
	return r.WithContext(ctx)
}

// decodeResponse reads the response body into a map.
func decodeResponse(t *testing.T, body io.Reader) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return result
}

// --- PublishHandler Tests ---

func TestPublishHandler_InvalidMethod(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/publish", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "method not allowed" {
		t.Errorf("expected error 'method not allowed', got %q", resp["error"])
	}
}

func TestPublishHandler_MissingNamespace(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishRequest{Topic: "test", DataB64: "aGVsbG8="})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	// No namespace set in context
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "namespace not resolved" {
		t.Errorf("expected error 'namespace not resolved', got %q", resp["error"])
	}
}

func TestPublishHandler_InvalidJSON(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader([]byte("not json")))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "invalid body: expected {topic,data_base64}" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestPublishHandler_MissingTopic(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(map[string]string{"data_base64": "aGVsbG8="})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "invalid body: expected {topic,data_base64}" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestPublishHandler_MissingData(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(map[string]string{"topic": "test"})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	// The handler checks body.Topic == "" || body.DataB64 == "", so missing data returns 400
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "invalid body: expected {topic,data_base64}" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestPublishHandler_InvalidBase64(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, _ := json.Marshal(PublishRequest{Topic: "test", DataB64: "!!!invalid-base64!!!"})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "invalid base64 data" {
		t.Errorf("unexpected error message: %q", resp["error"])
	}
}

func TestPublishHandler_Success(t *testing.T) {
	published := make(chan struct{}, 1)
	mock := &mockPubSubClient{
		PublishFunc: func(ctx context.Context, topic string, data []byte) error {
			published <- struct{}{}
			return nil
		},
	}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	body, _ := json.Marshal(PublishRequest{Topic: "chat", DataB64: "aGVsbG8="})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}

	// The publish to libp2p happens asynchronously; wait briefly for it
	select {
	case <-published:
		// success
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for async publish call")
	}
}

func TestPublishHandler_NilClient(t *testing.T) {
	logger := &logging.ColoredLogger{Logger: zap.NewNop()}
	h := NewPubSubHandlers(nil, logger)

	body, _ := json.Marshal(PublishRequest{Topic: "chat", DataB64: "aGVsbG8="})
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}
}

func TestPublishHandler_LocalDelivery(t *testing.T) {
	mock := &mockPubSubClient{}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	// Register a local subscriber
	msgChan := make(chan []byte, 1)
	localSub := &localSubscriber{
		msgChan:   msgChan,
		namespace: "test-ns",
	}
	topicKey := "test-ns.chat"
	h.mu.Lock()
	h.localSubscribers[topicKey] = append(h.localSubscribers[topicKey], localSub)
	h.mu.Unlock()

	body, _ := json.Marshal(PublishRequest{Topic: "chat", DataB64: "aGVsbG8="}) // "hello"
	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PublishHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Verify local delivery
	select {
	case msg := <-msgChan:
		if string(msg) != "hello" {
			t.Errorf("expected 'hello', got %q", string(msg))
		}
	case <-time.After(1 * time.Second):
		t.Error("timed out waiting for local delivery")
	}
}

// --- TopicsHandler Tests ---

func TestTopicsHandler_InvalidMethod(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	// TopicsHandler does not explicitly check method, but let's verify it responds.
	// Looking at the code: TopicsHandler does NOT check method, it accepts any method.
	// So POST should also work. Let's test GET which is the expected method.
	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.TopicsHandler(rr, req)

	// Should succeed with empty topics
	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
}

func TestTopicsHandler_MissingNamespace(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil)
	// No namespace
	rr := httptest.NewRecorder()

	h.TopicsHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "namespace not resolved" {
		t.Errorf("expected error 'namespace not resolved', got %q", resp["error"])
	}
}

func TestTopicsHandler_NilClient(t *testing.T) {
	logger := &logging.ColoredLogger{Logger: zap.NewNop()}
	h := NewPubSubHandlers(nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.TopicsHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}
}

func TestTopicsHandler_ReturnsTopics(t *testing.T) {
	mock := &mockPubSubClient{
		ListTopicsFunc: func(ctx context.Context) ([]string, error) {
			return []string{"chat", "events", "notifications"}, nil
		},
	}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.TopicsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	resp := decodeResponse(t, rr.Body)
	topics, ok := resp["topics"].([]interface{})
	if !ok {
		t.Fatalf("expected topics to be an array, got %T", resp["topics"])
	}
	if len(topics) != 3 {
		t.Errorf("expected 3 topics, got %d", len(topics))
	}
	expected := []string{"chat", "events", "notifications"}
	for i, e := range expected {
		if topics[i] != e {
			t.Errorf("expected topic[%d] = %q, got %q", i, e, topics[i])
		}
	}
}

func TestTopicsHandler_EmptyTopics(t *testing.T) {
	mock := &mockPubSubClient{
		ListTopicsFunc: func(ctx context.Context) ([]string, error) {
			return []string{}, nil
		},
	}
	h := newTestHandlers(&mockNetworkClient{pubsub: mock})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.TopicsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	resp := decodeResponse(t, rr.Body)
	topics, ok := resp["topics"].([]interface{})
	if !ok {
		t.Fatalf("expected topics to be an array, got %T", resp["topics"])
	}
	if len(topics) != 0 {
		t.Errorf("expected 0 topics, got %d", len(topics))
	}
}

// --- PresenceHandler Tests ---

func TestPresenceHandler_InvalidMethod(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodPost, "/v1/pubsub/presence?topic=test", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "method not allowed" {
		t.Errorf("expected error 'method not allowed', got %q", resp["error"])
	}
}

func TestPresenceHandler_MissingNamespace(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence?topic=test", nil)
	// No namespace
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "namespace not resolved" {
		t.Errorf("expected error 'namespace not resolved', got %q", resp["error"])
	}
}

func TestPresenceHandler_MissingTopic(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
	resp := decodeResponse(t, rr.Body)
	if resp["error"] != "missing 'topic'" {
		t.Errorf("expected error \"missing 'topic'\", got %q", resp["error"])
	}
}

func TestPresenceHandler_NoMembers(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence?topic=chat", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	resp := decodeResponse(t, rr.Body)
	if resp["topic"] != "chat" {
		t.Errorf("expected topic 'chat', got %q", resp["topic"])
	}
	count, ok := resp["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
	members, ok := resp["members"].([]interface{})
	if !ok {
		t.Fatalf("expected members to be an array, got %T", resp["members"])
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d", len(members))
	}
}

func TestPresenceHandler_WithMembers(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	// Pre-populate presence members
	topicKey := "test-ns.chat"
	now := time.Now().Unix()
	h.presenceMu.Lock()
	h.presenceMembers[topicKey] = []PresenceMember{
		{MemberID: "user-1", JoinedAt: now, Meta: map[string]interface{}{"name": "Alice"}},
		{MemberID: "user-2", JoinedAt: now, Meta: map[string]interface{}{"name": "Bob"}},
	}
	h.presenceMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence?topic=chat", nil)
	req = withNamespace(req, "test-ns")
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	resp := decodeResponse(t, rr.Body)
	if resp["topic"] != "chat" {
		t.Errorf("expected topic 'chat', got %q", resp["topic"])
	}
	count, ok := resp["count"].(float64)
	if !ok || count != 2 {
		t.Errorf("expected count 2, got %v", resp["count"])
	}
	members, ok := resp["members"].([]interface{})
	if !ok {
		t.Fatalf("expected members to be an array, got %T", resp["members"])
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}
}

func TestPresenceHandler_NamespaceIsolation(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	// Add members under namespace "app-1"
	now := time.Now().Unix()
	h.presenceMu.Lock()
	h.presenceMembers["app-1.chat"] = []PresenceMember{
		{MemberID: "user-1", JoinedAt: now},
	}
	h.presenceMu.Unlock()

	// Query with a different namespace "app-2" - should see no members
	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence?topic=chat", nil)
	req = withNamespace(req, "app-2")
	rr := httptest.NewRecorder()

	h.PresenceHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	resp := decodeResponse(t, rr.Body)
	count, ok := resp["count"].(float64)
	if !ok || count != 0 {
		t.Errorf("expected count 0 for different namespace, got %v", resp["count"])
	}
}

// --- Helper function tests ---

func TestResolveNamespaceFromRequest(t *testing.T) {
	// Without namespace
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ns := resolveNamespaceFromRequest(req)
	if ns != "" {
		t.Errorf("expected empty namespace, got %q", ns)
	}

	// With namespace
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req = withNamespace(req, "my-app")
	ns = resolveNamespaceFromRequest(req)
	if ns != "my-app" {
		t.Errorf("expected 'my-app', got %q", ns)
	}
}

func TestNamespacedTopic(t *testing.T) {
	result := namespacedTopic("my-ns", "chat")
	expected := "ns::my-ns::chat"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestNamespacePrefix(t *testing.T) {
	result := namespacePrefix("my-ns")
	expected := "ns::my-ns::"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestGetLocalSubscribers(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	// No subscribers
	subs := h.getLocalSubscribers("chat", "test-ns")
	if subs != nil {
		t.Errorf("expected nil for no subscribers, got %v", subs)
	}

	// Add a subscriber
	sub := &localSubscriber{
		msgChan:   make(chan []byte, 1),
		namespace: "test-ns",
	}
	h.mu.Lock()
	h.localSubscribers["test-ns.chat"] = []*localSubscriber{sub}
	h.mu.Unlock()

	subs = h.getLocalSubscribers("chat", "test-ns")
	if len(subs) != 1 {
		t.Errorf("expected 1 subscriber, got %d", len(subs))
	}
	if subs[0] != sub {
		t.Error("returned subscriber does not match registered subscriber")
	}
}
