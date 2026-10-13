package pubsub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// presenceMemberIDs lists the members the presence endpoint reports for topic.
func presenceMemberIDs(t *testing.T, p *PubSubHandlers, ns, topic string) []string {
	t.Helper()
	req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/pubsub/presence?topic="+topic, nil), ns)
	rr := httptest.NewRecorder()
	p.PresenceHandler(rr, req)
	var out struct {
		Members []struct {
			MemberID string `json:"member_id"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("presence reply %q: %v", rr.Body, err)
	}
	ids := make([]string, 0, len(out.Members))
	for _, m := range out.Members {
		ids = append(ids, m.MemberID)
	}
	return ids
}

// awaitEvent reads frames until one carries eventType, and returns it.
func awaitEvent(t *testing.T, conn *websocket.Conn, eventType string) string {
	t.Helper()
	for {
		data := frameData(t, readFrame(t, conn))
		if strings.Contains(data, `"`+eventType+`"`) {
			return data
		}
	}
}

// The socket that leaves takes only its own subscription with it, and does so
// even though its client never sent a frame.
func TestWebsocketHandler_aSocketLeavingKeepsTheOthersSubscribed(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	url := serveWS(t, p, "ns") + "?topic=shared"

	stays, leaves := dialWS(t, url), dialWS(t, url)
	waitFor(t, "both sockets to be subscribed upstream", func() bool { return up.subscribers() == 2 })

	_ = leaves.Close()
	waitFor(t, "the closed socket's subscription to end", func() bool { return up.subscribers() == 1 })

	up.feed("shared", []byte("still-here"))
	if got := readFrame(t, stays); !strings.Contains(got, `"topic":"shared"`) {
		t.Errorf("the staying socket got %q", got)
	}
}

// The bug: when a client that had never sent anything disconnected, the reader
// loop ended and then waited for the writer, which only stops on a failed
// write, so presence.leave was never broadcast and the member stayed listed.
func TestWebsocketHandler_presenceLeaveFiresWhenAnIdleClientDisconnects(t *testing.T) {
	up := newUpstream()
	p := newTestHandlers(&mockNetworkClient{pubsub: up.client()})
	base := serveWS(t, p, "ns")
	watcher := dialWS(t, base+"?topic=lobby")
	waitFor(t, "the watcher to be subscribed", func() bool { return up.subscribers() == 1 })
	alice := dialWS(t, base+"?topic=lobby&presence=true&member_id=alice")
	waitFor(t, "alice to be listed", func() bool { return len(presenceMemberIDs(t, p, "ns", "lobby")) == 1 })

	_ = alice.Close()

	if leave := awaitEvent(t, watcher, "presence.leave"); !strings.Contains(leave, `"alice"`) {
		t.Errorf("leave event %s, want alice", leave)
	}
	waitFor(t, "alice to be unlisted", func() bool { return len(presenceMemberIDs(t, p, "ns", "lobby")) == 0 })
	waitFor(t, "alice's subscription to end", func() bool { return up.subscribers() == 1 })
}
