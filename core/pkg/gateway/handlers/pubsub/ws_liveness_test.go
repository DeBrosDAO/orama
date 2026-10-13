package pubsub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/gorilla/websocket"
)

// pump reads conn in the background, as a live client does (which is also what
// answers the server's pings), and returns the text frames it receives.
func pump(conn *websocket.Conn) <-chan string {
	frames := make(chan string, 64)
	go func() {
		defer close(frames)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frames <- string(data)
		}
	}()
	return frames
}

func awaitFrameWith(t *testing.T, frames <-chan string, needle string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("socket closed before a frame with %q", needle)
			}
			if strings.Contains(f, needle) || strings.Contains(frameData(t, f), needle) {
				return
			}
		case <-timeout:
			t.Fatalf("no frame with %q", needle)
		}
	}
}

// A peer that stops answering pings, without closing anything, is a dead
// connection: the read deadline ends it and its presence.leave is broadcast.
func TestWebsocketHandler_aSilentPeerIsDroppedAndLeaves(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	p.pingInterval, p.pongWait = 20*time.Millisecond, 200*time.Millisecond
	base := serveWS(t, p, "ns")
	watcher := pump(dialWS(t, base+"?topic=lobby"))
	waitFor(t, "the watcher to be subscribed", func() bool { return up.subscribers() == 1 })

	// Never read: the client library answers pings only while reading.
	_ = dialWS(t, base+"?topic=lobby&presence=true&member_id=ghost")
	waitFor(t, "ghost to be listed", func() bool { return len(presenceMemberIDs(t, p, "ns", "lobby")) == 1 })

	awaitFrameWith(t, watcher, "presence.leave")
	waitFor(t, "ghost to be unlisted", func() bool { return len(presenceMemberIDs(t, p, "ns", "lobby")) == 0 })
	waitFor(t, "ghost's subscription to end", func() bool { return up.subscribers() == 1 })
}

// Pongs keep a live peer connected well past the pong wait.
func TestWebsocketHandler_aPeerAnsweringPingsStaysConnected(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	p.pingInterval, p.pongWait = 20*time.Millisecond, 200*time.Millisecond
	frames := pump(dialWS(t, serveWS(t, p, "ns")+"?topic=lobby"))
	waitFor(t, "the socket to be subscribed", func() bool { return up.subscribers() == 1 })

	time.Sleep(5 * p.pongWait)

	if up.subscribers() != 1 {
		t.Fatal("a peer that answers pings was dropped")
	}
	up.feed("lobby", []byte("alive"))
	awaitFrameWith(t, frames, `"topic":"lobby"`)
}

// A frame written on the socket is published to the topic; a heartbeat is not.
func TestWebsocketHandler_aClientFrameIsPublishedAndAHeartbeatIsNot(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	conn := dialWS(t, serveWS(t, p, "ns")+"?topic=talk")
	waitFor(t, "the socket to be subscribed", func() bool { return up.subscribers() == 1 })

	for _, m := range []string{`{"type":"ping"}`, "spoken"} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the frame to be published", func() bool { return up.published.Load() == 1 })
	if data := frameData(t, readFrame(t, conn)); data != "spoken" {
		t.Errorf("the socket got %q, want its own message back", data)
	}
	if n := up.published.Load(); n != 1 {
		t.Errorf("%d publishes, want 1: the heartbeat must not be published", n)
	}
}

// A service that cannot subscribe is a refusal the client sees, not a socket
// that silently never receives.
func TestWebsocketHandler_aFailedSubscribeIsRefused(t *testing.T) {
	mock := &mockPubSubClient{SubscribeHandleFunc: func(context.Context, string, client.MessageHandler) (func() error, error) {
		return nil, errors.New("service down")
	}}
	p := newTestHandlers(&mockNetworkClient{pubsub: mock})
	_, resp, err := websocket.DefaultDialer.Dial(serveWS(t, p, "ns")+"?topic=t", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want a refused upgrade with 503, got %v %v", resp, err)
	}
}
