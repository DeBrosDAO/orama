package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/gorilla/websocket"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// serveWS serves p's subscribe handler in namespace ns and returns its URL.
func serveWS(t *testing.T, p *PubSubHandlers, ns string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, ns)
		p.WebsocketHandler(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/pubsub/ws"
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// upstream is a fake of the node's pubsub service that records who subscribed
// to it and who left, and can feed every subscriber a message.
type upstream struct {
	mu       sync.Mutex
	handlers map[int]client.MessageHandler
	next     int
	stopped  []int

	// published counts the messages published to the service; the service loops
	// each one back to every subscriber, as gossipsub does.
	published atomic.Int64
}

func newUpstream() *upstream { return &upstream{handlers: map[int]client.MessageHandler{}} }

func (u *upstream) client() *mockPubSubClient {
	return &mockPubSubClient{
		PublishFunc: func(_ context.Context, topic string, data []byte) error {
			u.feed(topic, data)
			u.published.Add(1)
			return nil
		},
		PublishBatchFunc: func(_ context.Context, msgs []client.TopicMessage, _ client.PublishBatchOptions) error {
			for _, m := range msgs {
				u.feed(m.Topic, m.Data)
				u.published.Add(1)
			}
			return nil
		},
		SubscribeHandleFunc: func(_ context.Context, _ string, h client.MessageHandler) (func() error, error) {
			u.mu.Lock()
			defer u.mu.Unlock()
			id := u.next
			u.next++
			u.handlers[id] = h
			return func() error {
				u.mu.Lock()
				defer u.mu.Unlock()
				if _, ok := u.handlers[id]; ok {
					delete(u.handlers, id)
					u.stopped = append(u.stopped, id)
				}
				return nil
			}, nil
		},
	}
}

func (u *upstream) subscribers() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.handlers)
}

// feed delivers data to every handler still subscribed, as the service does.
func (u *upstream) feed(topic string, data []byte) {
	u.mu.Lock()
	hs := make([]client.MessageHandler, 0, len(u.handlers))
	for _, h := range u.handlers {
		hs = append(hs, h)
	}
	u.mu.Unlock()
	for _, h := range hs {
		_ = h(topic, data)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return string(data)
}

// The bug: the second socket on a topic cancelled the first one's upstream
// subscription, so only the newest socket was ever fed.
func TestWebsocketHandler_twoSocketsOnATopicAreBothFed(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	url := serveWS(t, p, "ns") + "?topic=shared"

	a, b := dialWS(t, url), dialWS(t, url)
	waitFor(t, "both sockets to be subscribed upstream", func() bool { return up.subscribers() == 2 })

	up.feed("shared", []byte("hello"))
	for name, conn := range map[string]*websocket.Conn{"first": a, "second": b} {
		if got := readFrame(t, conn); !strings.Contains(got, `"topic":"shared"`) {
			t.Errorf("%s socket got %q", name, got)
		}
	}
}
