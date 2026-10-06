package pubsub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

const membershipTopic = "_orama/webrtc/r1"

func publishTo(t *testing.T, topic string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(PublishRequest{Topic: topic, DataB64: base64.StdEncoding.EncodeToString([]byte(`{"x":1}`))})
	return withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body)), "anchat")
}

func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return body["code"]
}

func TestIsReservedTopic(t *testing.T) {
	for topic, want := range map[string]bool{
		membershipTopic: true, "_orama/": true, "_ORAMA/webrtc/r1": true, "_Orama/x": true,
		"_orama": false, "_orama.x": false, "x/_orama/y": false, "chat.general": false, "": false, "_oram": false,
	} {
		if got := isReservedTopic(topic); got != want {
			t.Errorf("isReservedTopic(%q) = %v, want %v", topic, got, want)
		}
	}
}

// LOW-4: no caller of a publish route can publish to a reserved topic, however
// much it is granted: the platform publishes these in-process.
func TestPublishHandler_reservedTopicIsRefusedForEveryCaller(t *testing.T) {
	for name, perms := range map[string]gwauth.PermissionSet{
		"no permission set (a public route)": nil,
		"an admin":                           gwauth.Everything(),
		"a data-plane key":                   gwauth.DataPlanePermissions(),
	} {
		t.Run(name, func(t *testing.T) {
			var published atomic.Int64
			h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})
			r := publishTo(t, membershipTopic)
			if perms != nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxkeys.Permissions, perms))
			}
			rr := httptest.NewRecorder()
			h.PublishHandler(rr, r)
			if rr.Code != http.StatusForbidden || errorCode(t, rr) != CodeReservedTopic || published.Load() != 0 {
				t.Fatalf("status %d code %q published %d: %s", rr.Code, errorCode(t, rr), published.Load(), rr.Body)
			}
		})
	}
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})
	rr := httptest.NewRecorder()
	h.PublishHandler(rr, publishTo(t, "_ORAMA/webrtc/r1"))
	if rr.Code != http.StatusForbidden || published.Load() != 0 {
		t.Errorf("a differently cased spelling: status %d published %d", rr.Code, published.Load())
	}
}

func TestPublishBatchHandler_oneReservedTopicRefusesTheWholeBatch(t *testing.T) {
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})
	data := base64.StdEncoding.EncodeToString([]byte(`{"x":1}`))
	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{
		{Topic: "chat.general", DataB64: data}, {Topic: membershipTopic, DataB64: data},
	}})
	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "anchat"))
	if rr.Code != http.StatusForbidden || errorCode(t, rr) != CodeReservedTopic || published.Load() != 0 {
		t.Fatalf("status %d published %d: %s", rr.Code, published.Load(), rr.Body)
	}
}

// F4: a signed-in user with no grant cannot subscribe to the platform's topics,
// which carry who is in rooms they were not admitted to.
func TestWebsocketHandler_reservedTopicNeedsAGrant(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})
	w := httptest.NewRecorder()
	h.WebsocketHandler(w, subscribeRequest(membershipTopic, "", gwauth.NoGrantPermissions()))
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeReservedTopic {
		t.Fatalf("status %d %s, want 403 %s for a wallet with no grant", w.Code, w.Body, CodeReservedTopic)
	}

	w = httptest.NewRecorder()
	h.WebsocketHandler(w, subscribeRequest(membershipTopic, "", gwauth.DataPlanePermissions()))
	if w.Code == http.StatusForbidden {
		t.Fatalf("a caller holding a data-plane grant was refused: %s", w.Body)
	}
}

func TestWebsocketHandler_grantMustCoverTheReservedTopic(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}})
	narrowed := gwauth.PermissionSet{{Domain: gwauth.DomainPubsub, Action: gwauth.ActionAny, Resource: "topic=chat.*"}}
	w := httptest.NewRecorder()
	h.WebsocketHandler(w, subscribeRequest(membershipTopic, "", narrowed))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a grant narrowed to chat.* reached %s: status %d", membershipTopic, w.Code)
	}
}

// A grant holder reads the topic and still cannot write to it, by frame or by presence.
func TestWebsocketHandler_reservedTopicIsReadOnlyEvenForAGrantHolder(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	conn := dialWS(t, serveWS(t, p, "anchat")+"?topic="+membershipTopic)
	if err := conn.WriteMessage(1, []byte(`{"user_id":"0xmallory"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "publish_error" {
		t.Fatalf("read %q, %v; want publish_error", msg, err)
	}
	if up.published.Load() != 0 {
		t.Fatalf("a frame was published to a reserved topic")
	}

	w := httptest.NewRecorder()
	p.WebsocketHandler(w, subscribeRequest(membershipTopic, "&presence=true&member_id=m", gwauth.Everything()))
	if w.Code != http.StatusForbidden {
		t.Fatalf("presence on a reserved topic: status %d, want 403", w.Code)
	}
}

func TestTopicsHandler_hidesReservedTopicsFromACallerWithoutAGrant(t *testing.T) {
	h := newTestHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{
		ListTopicsFunc: func(context.Context) ([]string, error) {
			return []string{"chat", membershipTopic}, nil
		},
	}})
	list := func(perms gwauth.PermissionSet) string {
		r := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/pubsub/topics", nil), "anchat")
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.Permissions, perms))
		rr := httptest.NewRecorder()
		h.TopicsHandler(rr, r)
		return rr.Body.String()
	}
	if got := list(gwauth.NoGrantPermissions()); strings.Contains(got, "_orama") || !strings.Contains(got, "chat") {
		t.Errorf("no grant: %s", got)
	}
	if got := list(gwauth.DataPlanePermissions()); !strings.Contains(got, membershipTopic) {
		t.Errorf("grant holder: %s", got)
	}
}
