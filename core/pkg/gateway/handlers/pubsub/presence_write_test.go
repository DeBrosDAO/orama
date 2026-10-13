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

// subscribeRequest is a pubsub WebSocket upgrade request for topic by a caller
// holding perms.
func subscribeRequest(topic, query string, perms gwauth.PermissionSet) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws?topic="+topic+query, nil)
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
	ctx = context.WithValue(ctx, ctxkeys.Permissions, perms)
	return r.WithContext(ctx)
}

// Presence publishes presence.join and presence.leave under the caller's
// member_id and meta, so a subscriber-only caller (a wallet holding no grant)
// enabling it could announce anyone's presence on a topic it may only read.
func TestWebsocketHandler_presenceNeedsPublishPermission(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})
	w := httptest.NewRecorder()
	h.WebsocketHandler(w, subscribeRequest("chat.general", "&presence=true&member_id=alice", gwauth.NoGrantPermissions()))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for presence from a subscriber-only caller: %s", w.Code, w.Body.String())
	}
}

// The same caller may still subscribe without presence: only the write is refused.
func TestWebsocketHandler_aSubscriberOnlyCallerPassesTheChecksWithoutPresence(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})
	w := httptest.NewRecorder()
	h.WebsocketHandler(w, subscribeRequest("chat.general", "", gwauth.NoGrantPermissions()))
	if w.Code == http.StatusForbidden {
		t.Fatalf("a subscriber-only caller without presence was refused: %s", w.Body.String())
	}
}

// A payload that starts as an object but does not parse strictly may carry the
// reserved key to a lenient subscriber, so it is refused rather than published.
func TestPublishHandler_refusesAnUnreadableObject(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})
	body, err := json.Marshal(PublishRequest{
		Topic:   "chat.general",
		DataB64: base64.StdEncoding.EncodeToString([]byte(`{"_orama":"ephemeral.set",}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat"))
	w := httptest.NewRecorder()
	h.PublishHandler(w, r)
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte(CodeUnreadableObject)) {
		t.Fatalf("status %d body %s, want 400 %s", w.Code, w.Body.String(), CodeUnreadableObject)
	}
}
