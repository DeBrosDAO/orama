package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// A `pubsub` grant used to be every topic in the namespace, so one leaked
// runtime key read every conversation in the application. A grant narrowed to
// `pubsub:topic=chat.*` is what lets a tenant isolate its own end users, and it
// only means anything if the publish and subscribe paths apply it.

func topicRequest(t *testing.T, topic, selector string) *http.Request {
	t.Helper()
	body, err := json.Marshal(PublishRequest{
		Topic:   topic,
		DataB64: base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
	if selector != "" {
		ctx = context.WithValue(ctx, ctxkeys.Permissions,
			gwauth.PermissionsFor(gwauth.RoleRuntime, selector))
	}
	return r.WithContext(ctx)
}

func TestAuthorizeTopic(t *testing.T) {
	tests := []struct {
		name     string
		topic    string
		selector string
		allowed  bool
	}{
		{"no selector at all", "billing.invoices", "", true},
		{"a topic the selector covers", "chat.general", "pubsub:topic=chat.*", true},
		{"a topic it does not", "billing.invoices", "pubsub:topic=chat.*", false},
		// `chat.*` is "chat." then anything, so a topic that shares the word
		// and not the separator is a different topic.
		{"a topic that only shares the prefix", "chatter.general", "pubsub:topic=chat.*", false},
		{"a selector for another domain", "chat.general", "fn:name=checkout", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := authorizeTopic(w, topicRequest(t, tc.topic, tc.selector), tc.topic, gwauth.ActionWrite)
			if got != tc.allowed {
				t.Errorf("authorizeTopic(%q) with %q = %v, want %v", tc.topic, tc.selector, got, tc.allowed)
			}
			if !got && w.Code != http.StatusForbidden {
				t.Errorf("a refusal answered %d, want 403", w.Code)
			}
		})
	}
}

// The check has to be in the handler, not merely available: a publish to a
// topic outside the grant must not be delivered.
func TestPublishHandler_refusesATopicOutsideTheGrant(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	w := httptest.NewRecorder()
	h.PublishHandler(w, topicRequest(t, "billing.invoices", "pubsub:topic=chat.*"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestPublishHandler_allowsATopicInsideTheGrant(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	w := httptest.NewRecorder()
	h.PublishHandler(w, topicRequest(t, "chat.general", "pubsub:topic=chat.*"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A batch is refused whole, before any of it is delivered: half a batch is
// worse than none.
func TestPublishBatchHandler_refusesTheWholeBatchForOneBadTopic(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})

	body, err := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{
		{Topic: "chat.general", DataB64: base64.StdEncoding.EncodeToString([]byte("ok"))},
		{Topic: "billing.invoices", DataB64: base64.StdEncoding.EncodeToString([]byte("not ok"))},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body))
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
	ctx = context.WithValue(ctx, ctxkeys.Permissions,
		gwauth.PermissionsFor(gwauth.RoleRuntime, "pubsub:topic=chat.*"))

	w := httptest.NewRecorder()
	h.PublishBatchHandler(w, r.WithContext(ctx))

	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", w.Code, w.Body.String())
	}
}
