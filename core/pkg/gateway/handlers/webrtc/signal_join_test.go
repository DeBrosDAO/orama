package webrtc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/gorilla/websocket"
)

// gatewayServer serves h's SignalHandler as a namespace gateway would, with the
// namespace already resolved.
func gatewayServer(t *testing.T, h *WebRTCHandlers) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "ns"))
		h.SignalHandler(w, asCaller(r, testUser, ""))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// joinGateway builds a gateway (with no proxy: the join-frame path must not need it).
func joinGateway(t *testing.T, dir SFUDirectory) *WebRTCHandlers {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "10.0.0.9", 30000, "", testTURNSecret, nil)
	h.SetSFUDirectory(dir)
	withAdmissions(t, h)
	return h
}

func dialGateway(t *testing.T, srv *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/webrtc/signal"
	if query != "" {
		u += "?" + query
	}
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", u, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readText(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

func joinJSON(room string) string {
	return fmt.Sprintf(`{"type":"join","data":{"roomId":%q,"userId":"u1"}}`, room)
}

// nextJoin returns the first frame an SFU received, or "" if none within the wait.
func nextJoin(f *fakeSFU) string {
	select {
	case j := <-f.joins:
		return j
	case <-time.After(300 * time.Millisecond):
		return ""
	}
}

// A client that puts no ?room= on the URL (AnChat) still reaches the room's
// SFU through any gateway, and its frames flow both ways.
func TestSignalJoinFrame_noRoomQueryTwoGatewaysReachOwnerSFU(t *testing.T) {
	sfus, dir := threeSFUs(t)
	srvA, srvB := gatewayServer(t, joinGateway(t, dir)), gatewayServer(t, joinGateway(t, dir))

	for i := 0; i < 10; i++ {
		room := fmt.Sprintf("anchat-%d", i)
		var hit *fakeSFU
		for _, srv := range []*httptest.Server{srvA, srvB} {
			c := dialGateway(t, srv, "")
			if err := c.WriteMessage(websocket.TextMessage, []byte(joinJSON(room))); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, c); got != `{"type":"welcome"}` {
				t.Fatalf("SFU reply did not come back through the gateway: %s", got)
			}
			if err := c.WriteMessage(websocket.TextMessage, []byte("ping-"+room)); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, c); got != "ping-"+room {
				t.Fatalf("frame after the join not piped both ways: %s", got)
			}
			var landed *fakeSFU
			for _, s := range sfus {
				if j := nextJoin(s); j != "" {
					if want := room + "|" + joinJSON(room); j != want {
						t.Fatalf("SFU saw %q, want the room in the URL and the join replayed verbatim: %q", j, want)
					}
					landed = s
				}
			}
			if landed == nil {
				t.Fatalf("no SFU received the join for %s", room)
			}
			if hit != nil && hit != landed {
				t.Fatalf("room %s split across SFUs %s and %s", room, hit.node.NodeID, landed.node.NodeID)
			}
			hit = landed
			c.Close()
		}
	}
}

func TestSignalJoinFrame_ownerDownRehomes(t *testing.T) {
	sfus, dir := threeSFUs(t)
	srv := gatewayServer(t, joinGateway(t, dir))

	c := dialGateway(t, srv, "")
	_ = c.WriteMessage(websocket.TextMessage, []byte(joinJSON("r1")))
	readText(t, c)
	var owner *fakeSFU
	for _, s := range sfus {
		if nextJoin(s) != "" {
			owner = s
		}
	}
	owner.srv.Close()

	c2 := dialGateway(t, srv, "")
	_ = c2.WriteMessage(websocket.TextMessage, []byte(joinJSON("r1")))
	if got := readText(t, c2); got != `{"type":"welcome"}` {
		t.Fatalf("no welcome after re-homing: %s", got)
	}
	for _, s := range sfus {
		if s != owner && nextJoin(s) != "" {
			return
		}
	}
	t.Fatal("the join did not reach a surviving SFU")
}

// A bad first frame is refused with an error frame naming why, and no SFU sees it.
func TestSignalJoinFrame_refusesBadFirstFrames(t *testing.T) {
	sfus, dir := threeSFUs(t)
	srv := gatewayServer(t, joinGateway(t, dir))

	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{"not JSON", `not json`, "must be JSON"},
		{"not a join", `{"type":"offer","data":{}}`, "must be a join"},
		{"no room", `{"type":"join","data":{"userId":"u"}}`, "room id must not be empty"},
		{"room with a space", joinJSON("my room"), "printable ASCII"},
		{"room too long", joinJSON(strings.Repeat("a", 129)), "the limit is 128"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := dialGateway(t, srv, "")
			_ = c.WriteMessage(websocket.TextMessage, []byte(tc.frame))
			if got := readText(t, c); !strings.Contains(got, `"error"`) || !strings.Contains(got, tc.want) {
				t.Fatalf("reply = %s, want an error frame containing %q", got, tc.want)
			}
		})
	}
	for _, s := range sfus {
		if j := nextJoin(s); j != "" {
			t.Fatalf("a refused frame reached an SFU: %s", j)
		}
	}
}

// An oversized first frame is cut off at the read limit (close 1009) and never replayed.
func TestSignalJoinFrame_oversizedFirstFrameIsCutOff(t *testing.T) {
	sfus, dir := threeSFUs(t)
	c := dialGateway(t, gatewayServer(t, joinGateway(t, dir)), "")
	big := `{"type":"join","data":{"roomId":"r1","userId":"` + strings.Repeat("u", joinFrameMaxBytes) + `"}}`
	_ = c.WriteMessage(websocket.TextMessage, []byte(big))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := c.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("read error = %v, want close 1009 (message too big)", err)
	}
	for _, s := range sfus {
		if j := nextJoin(s); j != "" {
			t.Fatalf("an oversized frame reached an SFU: %s", j)
		}
	}
}

func TestSignalJoinFrame_binaryFirstFrameRefused(t *testing.T) {
	_, dir := threeSFUs(t)
	c := dialGateway(t, gatewayServer(t, joinGateway(t, dir)), "")
	_ = c.WriteMessage(websocket.BinaryMessage, []byte(joinJSON("r1")))
	if got := readText(t, c); !strings.Contains(got, "text frame") {
		t.Fatalf("reply = %s", got)
	}
}

// A socket that never says which room it wants is dropped, not held open.
func TestSignalJoinFrame_slowClientIsRefused(t *testing.T) {
	_, dir := threeSFUs(t)
	h := joinGateway(t, dir)
	h.joinTimeout = 150 * time.Millisecond
	c := dialGateway(t, gatewayServer(t, h), "")

	start := time.Now()
	got := readText(t, c) // send nothing
	if !strings.Contains(got, "no join frame arrived within") {
		t.Fatalf("reply = %s", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the gateway held the silent socket for %s", time.Since(start))
	}
}

// No SFU is never answered by a local fallback: the client is told, and closed.
func TestSignalJoinFrame_noHealthySFUTellsClient(t *testing.T) {
	sfus, dir := threeSFUs(t)
	for _, s := range sfus {
		s.setDraining(true)
	}
	c := dialGateway(t, gatewayServer(t, joinGateway(t, dir)), "")
	_ = c.WriteMessage(websocket.TextMessage, []byte(joinJSON("r1")))
	if got := readText(t, c); !strings.Contains(got, "no_sfu") {
		t.Fatalf("reply = %s, want no_sfu", got)
	}
}

// --- rate limit ---

func TestSignalHandler_rateLimitedJoinIs429(t *testing.T) {
	_, dir := threeSFUs(t)
	var target string
	h := gatewayFor(t, dir, &target)
	allowed := 2
	h.SetJoinLimiter(func(*http.Request) bool { allowed--; return allowed >= 0 })

	for i := 0; i < 2; i++ {
		if w := signalTo(h, "ns", "r1"); w.Code != http.StatusOK {
			t.Fatalf("join %d: status %d", i, w.Code)
		}
	}
	w := signalTo(h, "ns", "r1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d Retry-After = %q, want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestSignalJoinFrame_rateLimitedBeforeUpgrade(t *testing.T) {
	_, dir := threeSFUs(t)
	h := joinGateway(t, dir)
	h.SetJoinLimiter(func(*http.Request) bool { return false })
	srv := gatewayServer(t, h)

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/webrtc/signal"
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("dial err = %v resp = %v, want a 429 handshake refusal", err, resp)
	}
}
