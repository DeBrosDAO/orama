package pubsub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

const markerData = "marker"

// frameData decodes the base64 payload of a subscriber envelope.
func frameData(t *testing.T, frame string) string {
	t.Helper()
	var env struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(frame), &env); err != nil {
		t.Fatalf("envelope %q: %v", frame, err)
	}
	raw, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		t.Fatalf("envelope data %q: %v", env.Data, err)
	}
	return string(raw)
}

// received drains conn up to the marker and counts what arrived. A message
// enqueued before the marker was fed is ahead of it, so this sees everything
// delivered so far.
func received(t *testing.T, conn *websocket.Conn, up *upstream) map[string]int {
	t.Helper()
	up.feed("t", []byte(markerData))
	seen := map[string]int{}
	for {
		data := frameData(t, readFrame(t, conn))
		if data == markerData {
			return seen
		}
		seen[data]++
	}
}

func publishThroughHandler(t *testing.T, p *PubSubHandlers, ns, topic, data string) {
	t.Helper()
	body, _ := json.Marshal(PublishRequest{Topic: topic, DataB64: base64.StdEncoding.EncodeToString([]byte(data))})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish", bytes.NewReader(body)), ns)
	rr := httptest.NewRecorder()
	p.PublishHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body)
	}
}

// The bug: the publish handler delivered to the sockets on its own gateway
// itself and then published to the pubsub service, which looped the message
// back to the same sockets, so a subscriber on the publishing node got each
// message twice.
func TestPublishHandler_aSubscriberOnThePublishingNodeReceivesEachMessageOnce(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	conn := dialWS(t, serveWS(t, p, "ns")+"?topic=t")
	waitFor(t, "the socket to be subscribed", func() bool { return up.subscribers() == 1 })

	const count = 10
	for i := 0; i < count; i++ {
		publishThroughHandler(t, p, "ns", "t", "m"+string(rune('a'+i)))
	}
	waitFor(t, "every publish to reach the service", func() bool { return up.published.Load() == count })

	seen := received(t, conn, up)
	for i := 0; i < count; i++ {
		if k := "m" + string(rune('a'+i)); seen[k] != 1 {
			t.Errorf("%s delivered %d times, want once", k, seen[k])
		}
	}
}

func TestPublishBatchHandler_aSubscriberReceivesEachMessageOnce(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	conn := dialWS(t, serveWS(t, p, "ns")+"?topic=t")
	waitFor(t, "the socket to be subscribed", func() bool { return up.subscribers() == 1 })

	body, _ := json.Marshal(PublishBatchRequest{Messages: []PublishBatchEntry{
		{Topic: "t", DataB64: base64.StdEncoding.EncodeToString([]byte("one"))},
		{Topic: "t", DataB64: base64.StdEncoding.EncodeToString([]byte("two"))},
	}})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/pubsub/publish-batch", bytes.NewReader(body)), "ns")
	rr := httptest.NewRecorder()
	p.PublishBatchHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", rr.Code, rr.Body)
	}
	waitFor(t, "the batch to reach the service", func() bool { return up.published.Load() == 2 })

	seen := received(t, conn, up)
	if seen["one"] != 1 || seen["two"] != 1 {
		t.Errorf("batch delivered %v, want each once", seen)
	}
}

// Remote subscribers see one delivery too: the message reaches the service
// once per publish.
func TestPublishHandler_publishesOncePerRequest(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	publishThroughHandler(t, p, "ns", "t", "x")
	waitFor(t, "the publish to reach the service", func() bool { return up.published.Load() >= 1 })
	if n := up.published.Load(); n != 1 {
		t.Fatalf("service saw %d publishes, want 1", n)
	}
}

// A presence join is published, and reaches every subscriber once, the joining
// socket included, by the same path as a message.
func TestWebsocketHandler_presenceJoinReachesEachSubscriberOnce(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	base := serveWS(t, p, "ns")
	watcher := dialWS(t, base+"?topic=t")
	waitFor(t, "the watcher to be subscribed", func() bool { return up.subscribers() == 1 })
	alice := dialWS(t, base+"?topic=t&presence=true&member_id=alice")
	waitFor(t, "alice to be subscribed", func() bool { return up.subscribers() == 2 })
	waitFor(t, "the join to be published", func() bool { return up.published.Load() == 1 })

	for name, conn := range map[string]*websocket.Conn{"watcher": watcher, "alice": alice} {
		joins := 0
		for data, n := range received(t, conn, up) {
			if strings.Contains(data, `"presence.join"`) {
				joins += n
			}
		}
		if joins != 1 {
			t.Errorf("%s saw %d joins, want 1", name, joins)
		}
	}
}
