package sfu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/gorilla/websocket"
)

// rawFrame is a server frame with its data left undecoded.
type rawFrame struct {
	Type MessageType     `json:"type"`
	Data json.RawMessage `json:"data"`
}

// testTicket is a valid ticket for room, as the namespace gateway would issue it.
func testTicket(t *testing.T, s *Server, room, user string) ctrlauth.Ticket {
	t.Helper()
	return ctrlauth.Ticket{
		Namespace:  s.config.Namespace,
		Room:       room,
		UserID:     user,
		IssuedAtMs: time.Now().UnixMilli(),
		Expires:    time.Now().Add(ctrlauth.TicketTTL).Unix(),
	}
}

// sealTicket signs tk with the server's own key.
func sealTicket(t *testing.T, s *Server, tk ctrlauth.Ticket) string {
	t.Helper()
	token, err := tk.Seal(s.controlKey)
	if err != nil {
		t.Fatalf("seal ticket: %v", err)
	}
	return token
}

// dialSignal opens a signalling socket to a real handleSignal behind httptest,
// as user u1 admitted to the room the query names (r1 when it names none).
func dialSignal(t *testing.T, s *Server, query string) *websocket.Conn {
	t.Helper()
	room, _ := url.ParseQuery(query)
	id := room.Get("room")
	if id == "" {
		id = "r1"
	}
	return dialSignalAs(t, s, query, testTicket(t, s, id, "u1"))
}

// dialSignalAs opens a signalling socket presenting tk.
func dialSignalAs(t *testing.T, s *Server, query string, tk ctrlauth.Ticket) *websocket.Conn {
	t.Helper()
	conn, resp, err := tryDial(t, s, query, http.Header{ctrlauth.TicketHeader: {sealTicket(t, s, tk)}})
	if err != nil {
		t.Fatalf("dial: %v (status %v)", err, resp)
	}
	return conn
}

// tryDial dials with exactly the headers given and returns what came back.
func tryDial(t *testing.T, s *Server, query string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handleSignal))
	t.Cleanup(srv.Close)
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/signal"
	if query != "" {
		u += "?" + query
	}
	conn, resp, err := websocket.DefaultDialer.Dial(u, header)
	if err == nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func sendJoin(t *testing.T, conn *websocket.Conn, room string) {
	t.Helper()
	data, _ := json.Marshal(JoinData{RoomID: room, UserID: "u1"})
	if err := conn.WriteJSON(ClientMessage{Type: MessageTypeJoin, Data: data}); err != nil {
		t.Fatalf("send join: %v", err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) rawFrame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m rawFrame
	if err := conn.ReadJSON(&m); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return m
}

// The 80%-of-TTL refresh is a refresh-credentials frame, distinct from the
// turn-credentials frame sent once on join.
func TestCredentialRefresh_sendsRefreshCredentialsFrame(t *testing.T) {
	cfg := testConfig()
	cfg.TURNCredentialTTL = 600
	s, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	var waited atomic.Int64
	s.refreshAfter = func(d time.Duration) <-chan time.Time {
		if waited.Add(1) > 1 {
			return make(chan time.Time) // one refresh is enough
		}
		if want := 480 * time.Second; d != want {
			t.Errorf("refresh waits %s, want 80%% of the TTL (%s)", d, want)
		}
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	conn := dialSignal(t, s, "room=r1")
	sendJoin(t, conn, "r1")

	initial := 0
	for i := 0; i < 6; i++ {
		m := readFrame(t, conn)
		switch m.Type {
		case MessageTypeTURNCredentials:
			initial++
		case MessageTypeRefreshCredentials:
			var got TURNCredentialsData
			if err := json.Unmarshal(m.Data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Username == "" || got.Password == "" || len(got.URIs) == 0 || got.TTL != 600 {
				t.Fatalf("refresh-credentials payload incomplete: %+v", got)
			}
			if initial != 1 {
				t.Fatalf("saw %d turn-credentials frames before the refresh, want exactly 1", initial)
			}
			return
		}
	}
	t.Fatal("no refresh-credentials frame after the join")
}

func TestSignal_joinForOtherRoomThanQueryIsRejected(t *testing.T) {
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	conn := dialSignal(t, s, url.Values{"room": {"owned-room"}}.Encode())
	sendJoin(t, conn, "another-room")

	m := readFrame(t, conn)
	if m.Type != MessageTypeError || !strings.Contains(string(m.Data), "room_mismatch") {
		t.Fatalf("frame = %s %s, want error room_mismatch", m.Type, m.Data)
	}
	if s.roomManager.RoomCount() != 0 {
		t.Errorf("a mismatched join created a room")
	}
}

func TestSignal_joinWithoutRoomQueryStillWorks(t *testing.T) {
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	conn := dialSignal(t, s, "")
	sendJoin(t, conn, "r1")
	if m := readFrame(t, conn); m.Type != MessageTypeWelcome {
		t.Fatalf("first frame = %s, want welcome", m.Type)
	}
}

// /health?room= tells a gateway whether this SFU already hosts the room.
func TestHealth_reportsWhetherRoomHasParticipants(t *testing.T) {
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	get := func(room string) string {
		w := httptest.NewRecorder()
		s.handleHealth(w, httptest.NewRequest("GET", "/health?room="+room, nil))
		return w.Body.String()
	}
	if got := get("r1"); got != `{"status":"ok","rooms":0,"hasRoom":false}` {
		t.Errorf("unknown room: %s", got)
	}

	conn := dialSignal(t, s, "room=r1")
	sendJoin(t, conn, "r1")
	if m := readFrame(t, conn); m.Type != MessageTypeWelcome {
		t.Fatalf("first frame = %s, want welcome", m.Type)
	}
	if got := get("r1"); got != `{"status":"ok","rooms":1,"hasRoom":true}` {
		t.Errorf("hosted room: %s", got)
	}
	if got := get("other"); got != `{"status":"ok","rooms":1,"hasRoom":false}` {
		t.Errorf("other room: %s", got)
	}
	if got := get(""); !strings.Contains(got, `"hasRoom":false`) {
		t.Errorf("no room param: %s", got)
	}
}

func TestRoomManagerHasParticipants_emptyRoomDoesNotCount(t *testing.T) {
	rm := NewRoomManager(testConfig(), testLogger())
	rm.GetOrCreateRoom("empty")
	if rm.HasParticipants("empty") {
		t.Error("an empty room reports participants")
	}
	if rm.HasParticipants("") || rm.HasParticipants("missing") {
		t.Error("an absent room reports participants")
	}
}

func TestSignal_joinWithInvalidRoomIdIsRefused(t *testing.T) {
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"has a space", strings.Repeat("a", 129)} {
		conn := dialSignal(t, s, "")
		sendJoin(t, conn, room)
		m := readFrame(t, conn)
		if m.Type != MessageTypeError || !strings.Contains(string(m.Data), "invalid_join") {
			t.Fatalf("room %q: frame = %s %s, want error invalid_join", room, m.Type, m.Data)
		}
	}
	if s.roomManager.RoomCount() != 0 {
		t.Error("an invalid room id created a room")
	}
}
