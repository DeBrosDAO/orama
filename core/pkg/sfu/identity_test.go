package sfu

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/gorilla/websocket"
)

func newAuthServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.roomManager.CloseAll(); s.roomManager.reporter.close() })
	return s
}

func joinAs(t *testing.T, s *Server, tk ctrlauth.Ticket, frameUser string) (*websocket.Conn, WelcomeData) {
	t.Helper()
	conn := dialSignalAs(t, s, "room="+tk.Room, tk)
	data, _ := json.Marshal(JoinData{RoomID: tk.Room, UserID: frameUser})
	if err := conn.WriteJSON(ClientMessage{Type: MessageTypeJoin, Data: data}); err != nil {
		t.Fatal(err)
	}
	m := readFrame(t, conn)
	if m.Type != MessageTypeWelcome {
		t.Fatalf("first frame = %s %s, want welcome", m.Type, m.Data)
	}
	var w WelcomeData
	if err := json.Unmarshal(m.Data, &w); err != nil {
		t.Fatal(err)
	}
	return conn, w
}

func TestSignal_upgradeWithoutAValidTicketIsRefused(t *testing.T) {
	s := newAuthServer(t)
	other, err := ctrlauth.Key("another-namespaces-secret")
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := testTicket(t, s, "r1", "u").Seal(other)
	expired := testTicket(t, s, "r1", "u")
	expired.Expires = time.Now().Add(-time.Minute).Unix()
	elsewhere := testTicket(t, s, "r1", "u")
	elsewhere.Namespace = "someone-else"

	cases := []struct {
		name   string
		header http.Header
		status int
	}{
		{"no ticket", http.Header{}, http.StatusUnauthorized},
		{"signed with another namespace's key", http.Header{ctrlauth.TicketHeader: {forged}}, http.StatusUnauthorized},
		{"expired", http.Header{ctrlauth.TicketHeader: {sealTicket(t, s, expired)}}, http.StatusUnauthorized},
		{"for another namespace", http.Header{ctrlauth.TicketHeader: {sealTicket(t, s, elsewhere)}}, http.StatusForbidden},
		{"garbage", http.Header{ctrlauth.TicketHeader: {"not-a-ticket"}}, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, resp, err := tryDial(t, s, "room=r1", c.header)
			if err == nil || resp == nil || resp.StatusCode != c.status {
				t.Fatalf("dial = %v, resp %v; want a %d refusal", err, resp, c.status)
			}
		})
	}
	if s.roomManager.RoomCount() != 0 {
		t.Error("a refused upgrade created a room")
	}
}

func TestSignal_identityComesFromTheTicketNotTheJoinFrame(t *testing.T) {
	s := newAuthServer(t)
	tk := testTicket(t, s, "r1", "0xreal-user")
	tk.DeviceID = "device-thumbprint"

	_, welcome := joinAs(t, s, tk, "someone-the-client-claims-to-be")

	if len(welcome.Participants) != 1 {
		t.Fatalf("participants = %+v, want exactly the joiner", welcome.Participants)
	}
	got := welcome.Participants[0]
	if got.UserID != "0xreal-user" || got.DeviceID != "device-thumbprint" {
		t.Fatalf("participant = %+v, want the ticket's user and device", got)
	}
}

func TestSignal_joinFrameWithoutUserIDIsAccepted(t *testing.T) {
	s := newAuthServer(t)
	_, welcome := joinAs(t, s, testTicket(t, s, "r1", "u"), "")
	if welcome.Participants[0].UserID != "u" {
		t.Fatalf("participant = %+v", welcome.Participants[0])
	}
}

func TestSignal_ticketForOneRoomDoesNotAdmitToAnother(t *testing.T) {
	s := newAuthServer(t)
	conn := dialSignalAs(t, s, "", testTicket(t, s, "admitted-room", "u"))
	sendJoin(t, conn, "another-room")

	m := readFrame(t, conn)
	if m.Type != MessageTypeError || !strings.Contains(string(m.Data), "room_mismatch") {
		t.Fatalf("frame = %s %s, want error room_mismatch", m.Type, m.Data)
	}
	if s.roomManager.RoomCount() != 0 {
		t.Error("a join for a room the ticket did not name created it")
	}
}

func TestSignal_ticketRemembersItsEventSinkAndMuteFlag(t *testing.T) {
	s := newAuthServer(t)
	tk := testTicket(t, s, "r1", "u")
	tk.Muted = true
	tk.EventSink = "http://10.0.0.7:10004"

	joinAs(t, s, tk, "")

	if sinks := s.roomManager.reporter.snapshotSinks(); len(sinks) == 0 || sinks[0] != tk.EventSink {
		t.Fatalf("reporter sinks = %v, want the ticket's sink first", sinks)
	}
	peers := s.roomManager.GetRoom("r1").snapshotPeers("")
	if len(peers) != 1 || !peers[0].muted.Load() || peers[0].eventSink != tk.EventSink {
		t.Fatalf("peers = %+v, want one muted peer that knows its sink", peers)
	}
}
