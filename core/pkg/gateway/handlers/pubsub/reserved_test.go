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

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

func TestClassifyEnvelope(t *testing.T) {
	deep := `{"_orama":"x","a":` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`
	tests := []struct {
		name string
		data string
		want envelopeVerdict
	}{
		{"an object with the key", `{"_orama":"ephemeral.set","topic":"t"}`, envelopeReserved},
		{"leading whitespace", " \n\t{\"_orama\":1}", envelopeReserved},
		{"the key among others", `{"a":1,"b":{"c":2},"_orama":null}`, envelopeReserved},
		{"the key spelled with an escape", `{"\u005forama":"x"}`, envelopeReserved},
		{"the key in another case", `{"_ORAMA":"x"}`, envelopeReserved},
		{"a byte-order mark before the object", "\xEF\xBB\xBF{\"_orama\":\"x\"}", envelopeReserved},
		{"an ordinary object", `{"type":"message","body":"hi"}`, envelopeClear},
		{"an ordinary object after a byte-order mark", "\xEF\xBB\xBF{\"body\":\"hi\"}", envelopeClear},
		{"the key nested, not top-level", `{"body":{"_orama":"x"}}`, envelopeClear},
		{"the key only inside a string value", `{"body":"_orama"}`, envelopeClear},
		{"a longer key that contains it", `{"__orama":"auth.refresh"}`, envelopeClear},
		{"an array", `[{"_orama":"x"}]`, envelopeClear},
		{"plain text", `hello _orama`, envelopeClear},
		{"malformed JSON that starts as an object", `{"_orama":`, envelopeUnreadable},
		{"trailing comma a lenient parser accepts", `{"_orama":"x",}`, envelopeUnreadable},
		{"nesting past Go's limit", deep, envelopeUnreadable},
		{"empty", ``, envelopeClear},
		{"nil-like whitespace", "  \n", envelopeClear},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyEnvelope([]byte(tc.data)); got != tc.want {
				t.Errorf("classifyEnvelope(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

func publishRequestFor(t *testing.T, payload string) *http.Request {
	t.Helper()
	body, err := json.Marshal(PublishRequest{
		Topic:   "chat.general",
		DataB64: base64.StdEncoding.EncodeToString([]byte(payload)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body)), "anchat")
}

func countingPublisher(published *atomic.Int64) *mockPubSubClient {
	return &mockPubSubClient{
		PublishFunc: func(context.Context, string, []byte) error {
			published.Add(1)
			return nil
		},
		PublishBatchFunc: func(context.Context, []client.TopicMessage, client.PublishBatchOptions) error {
			published.Add(1)
			return nil
		},
	}
}

func TestPublishHandler_reserved_key_is_refused(t *testing.T) {
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})

	rr := httptest.NewRecorder()
	h.PublishHandler(rr, publishRequestFor(t, `{"_orama":"ephemeral.clear","key":"k"}`))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if got := decodeResponse(t, rr.Body)["code"]; got != CodeReservedEnvelope {
		t.Errorf("code = %v, want %s", got, CodeReservedEnvelope)
	}
	if published.Load() != 0 {
		t.Errorf("a refused message was published %d times", published.Load())
	}
}

func TestPublishHandler_reserved_key_is_refused_for_an_unrestricted_caller(t *testing.T) {
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})

	r := publishRequestFor(t, `{"_orama":"ephemeral.set"}`)
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.Permissions, gwauth.Everything()))
	rr := httptest.NewRecorder()
	h.PublishHandler(rr, r)

	if rr.Code != http.StatusBadRequest || published.Load() != 0 {
		t.Errorf("an admin's reserved publish: status %d, published %d; want 400 and none", rr.Code, published.Load())
	}
}

func TestPublishHandler_ordinary_payloads_are_published(t *testing.T) {
	for name, payload := range map[string]string{
		"json object":      `{"type":"message","body":"hi"}`,
		"binary":           "\x00\x01\x02",
		"array":            `[{"_orama":1}]`,
		"nested only":      `{"body":{"_orama":1}}`,
		"auth refresh key": `{"__orama":"auth.refresh"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var published atomic.Int64
			h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})

			rr := httptest.NewRecorder()
			h.PublishHandler(rr, publishRequestFor(t, payload))

			if rr.Code != http.StatusOK || published.Load() != 1 {
				t.Errorf("status %d, published %d; want 200 and 1: %s", rr.Code, published.Load(), rr.Body.String())
			}
		})
	}
}

func batchRequestFor(t *testing.T, payloads ...string) *http.Request {
	t.Helper()
	entries := make([]PublishBatchEntry, len(payloads))
	for i, p := range payloads {
		entries[i] = PublishBatchEntry{Topic: "chat.general", DataB64: base64.StdEncoding.EncodeToString([]byte(p))}
	}
	body, err := json.Marshal(PublishBatchRequest{Messages: entries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "anchat")
}

func TestPublishBatchHandler_one_reserved_item_refuses_the_batch(t *testing.T) {
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})

	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, batchRequestFor(t, `{"a":1}`, `{"_orama":"ephemeral.set"}`, `{"b":2}`))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rr.Code, rr.Body.String())
	}
	body := decodeResponse(t, rr.Body)
	if body["code"] != CodeReservedEnvelope || !strings.Contains(body["error"].(string), "index 1") {
		t.Errorf("body %v; want code %s naming index 1", body, CodeReservedEnvelope)
	}
	if published.Load() != 0 {
		t.Errorf("a refused batch published %d times; none of it may be delivered", published.Load())
	}
}

func TestPublishBatchHandler_ordinary_batch_is_published(t *testing.T) {
	var published atomic.Int64
	h := newTestHandlers(&mockNetworkClient{pubsub: countingPublisher(&published)})

	rr := httptest.NewRecorder()
	h.PublishBatchHandler(rr, batchRequestFor(t, `{"a":1}`, `plain`))

	if rr.Code != http.StatusOK || published.Load() != 1 {
		t.Errorf("status %d, published %d; want 200 and 1: %s", rr.Code, published.Load(), rr.Body.String())
	}
}

// A subscriber-only socket reads the topic and must not write to it by sending
// a frame; a socket that may publish still cannot send the reserved key.
func TestWebsocketHandler_frames_from_a_subscriber_only_socket_are_not_published(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	readOnly := gwauth.PermissionSet{{Domain: gwauth.DomainPubsub, Action: gwauth.ActionRead, Resource: gwauth.PermissionWildcard}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
		ctx = context.WithValue(ctx, ctxkeys.Permissions, readOnly)
		p.WebsocketHandler(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)

	conn := dialWS(t, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/pubsub/ws?topic=chat.general")
	if err := conn.WriteMessage(1, []byte(`{"body":"hi"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "publish_error" {
		t.Fatalf("read %q, %v; want publish_error", msg, err)
	}
	if up.published.Load() != 0 {
		t.Errorf("a subscriber-only socket published %d messages", up.published.Load())
	}
}

func TestWebsocketHandler_a_writer_socket_publishes_but_not_the_reserved_key(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	conn := dialWS(t, serveWS(t, p, "anchat")+"?topic=chat.general")

	if err := conn.WriteMessage(1, []byte(`{"_orama":"ephemeral.clear"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "publish_error" {
		t.Fatalf("read %q, %v; want publish_error for the reserved key", msg, err)
	}
	if err := conn.WriteMessage(1, []byte(`{"body":"hi"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, "the ordinary frame to be published", func() bool { return up.published.Load() == 1 })
}
