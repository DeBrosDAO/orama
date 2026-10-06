package sfu

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
)

// sink is a namespace gateway's event endpoint: it records the events whose MAC
// verifies under key and answers status.
type sink struct {
	*httptest.Server
	mu     sync.Mutex
	events []ctrlauth.MembershipEvent
	macs   int
}

func newSink(t *testing.T, key []byte, status int) *sink {
	t.Helper()
	s := &sink{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := ctrlauth.Verify(key, s.Server.URL, r.Header.Get(ctrlauth.MACHeader), r.Method, r.URL.Path, body, time.Now()); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var ev ctrlauth.MembershipEvent
		_ = json.Unmarshal(body, &ev)
		s.mu.Lock()
		s.macs++
		s.events = append(s.events, ev)
		s.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sink) got() []ctrlauth.MembershipEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ctrlauth.MembershipEvent(nil), s.events...)
}

func waitEvents(t *testing.T, s *sink, n int) []ctrlauth.MembershipEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ev := s.got(); len(ev) >= n {
			return ev
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("got %d events, want %d: %+v", len(s.got()), n, s.got())
	return nil
}

func testKeyFor(t *testing.T) []byte {
	t.Helper()
	key, err := ctrlauth.Key("reporter-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestReporter_deliversInOrderWithAValidMAC(t *testing.T) {
	key := testKeyFor(t)
	gw := newSink(t, key, http.StatusOK)
	r := newReporter(key, testLogger())
	r.observe(gw.URL)

	for i, typ := range []string{ctrlauth.EventJoin, ctrlauth.EventLeave, ctrlauth.EventJoin} {
		r.report(ctrlauth.MembershipEvent{Type: typ, Room: "r", UserID: "u", PeerID: string(rune('a' + i)), At: time.Now()})
	}
	r.close()

	got := gw.got()
	if len(got) != 3 || got[0].PeerID != "a" || got[1].PeerID != "b" || got[2].PeerID != "c" || got[1].Type != ctrlauth.EventLeave {
		t.Fatalf("events = %+v, want the three in the order reported", got)
	}
}

func TestReporter_movesToTheNextGatewayWhenOneDoesNotAnswer(t *testing.T) {
	key := testKeyFor(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // a gateway that restarted: connection refused
	live := newSink(t, key, http.StatusOK)
	r := newReporter(key, testLogger())
	r.observe(live.URL)
	r.observe(deadURL) // most recently seen, so tried first

	r.report(ctrlauth.MembershipEvent{Type: ctrlauth.EventLeave, Room: "r", UserID: "u", At: time.Now()})
	r.close()

	if got := live.got(); len(got) != 1 {
		t.Fatalf("live gateway got %d events, want the one the dead one could not take", len(got))
	}
}

func TestReporter_aGatewayThatRefusesIsNotRoutedAround(t *testing.T) {
	key := testKeyFor(t)
	refusing := newSink(t, []byte("a different key"), http.StatusOK) // every MAC fails: 401
	other := newSink(t, key, http.StatusOK)
	r := newReporter(key, testLogger())
	r.observe(other.URL)
	r.observe(refusing.URL)

	r.report(ctrlauth.MembershipEvent{Type: ctrlauth.EventJoin, Room: "r", UserID: "u", At: time.Now()})
	r.close()

	if got := other.got(); len(got) != 0 {
		t.Fatalf("an event refused for its MAC was sent on to another gateway: %+v", got)
	}
}

func TestReporter_noGatewayKnownDropsWithoutBlocking(t *testing.T) {
	r := newReporter(testKeyFor(t), testLogger())
	r.report(ctrlauth.MembershipEvent{Type: ctrlauth.EventJoin, Room: "r", UserID: "u", At: time.Now()})
	done := make(chan struct{})
	go func() { r.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close hung with undeliverable events")
	}
}

func TestReporter_reportAfterCloseIsIgnored(t *testing.T) {
	r := newReporter(testKeyFor(t), testLogger())
	r.close()
	r.report(ctrlauth.MembershipEvent{Type: ctrlauth.EventJoin, Room: "r", UserID: "u"}) // must not panic
	r.close()
}

func TestReporter_observeKeepsMostRecentFirstAndBounded(t *testing.T) {
	r := newReporter(testKeyFor(t), testLogger())
	defer r.close()
	for i := 0; i < maxEventSinks+4; i++ {
		r.observe("http://10.0.0." + string(rune('0'+i%10)) + ":1" + string(rune('0'+i/10)))
	}
	r.observe("http://10.0.0.99:1")
	sinks := r.snapshotSinks()
	if len(sinks) != maxEventSinks || sinks[0] != "http://10.0.0.99:1" {
		t.Fatalf("sinks = %v, want %d with the latest first", sinks, maxEventSinks)
	}
	r.observe("")
	if len(r.snapshotSinks()) != maxEventSinks {
		t.Error("an empty sink changed the list")
	}
}

func TestReporter_expiredSinksAreForgotten(t *testing.T) {
	r := newReporter(testKeyFor(t), testLogger())
	defer r.close()
	now := time.Now()
	r.now = func() time.Time { return now }
	r.observe("http://10.0.0.1:1")
	now = now.Add(eventSinkTTL + time.Second)
	if sinks := r.snapshotSinks(); len(sinks) != 0 {
		t.Fatalf("sinks = %v, want the stale one dropped", sinks)
	}
}

// A peer joining and leaving is reported with the authenticated identity, and a
// kick says so in the leave.
func TestRoom_joinLeaveAndKickAreReported(t *testing.T) {
	s := newAuthServer(t)
	gw := newSink(t, s.controlKey, http.StatusOK)
	s.roomManager.reporter.observe(gw.URL)

	tk := testTicket(t, s, "r1", "0xalice")
	tk.DeviceID = "phone"
	alice, _ := joinAs(t, s, tk, "forged-user")
	ev := waitEvents(t, gw, 1)
	if ev[0].Type != ctrlauth.EventJoin || ev[0].UserID != "0xalice" || ev[0].DeviceID != "phone" || ev[0].Room != "r1" || ev[0].PeerID == "" || ev[0].At.IsZero() {
		t.Fatalf("join event = %+v", ev[0])
	}

	_ = alice.WriteJSON(ClientMessage{Type: MessageTypeLeave})
	ev = waitEvents(t, gw, 2)
	if ev[1].Type != ctrlauth.EventLeave || ev[1].Reason != ctrlauth.LeftReason || ev[1].UserID != "0xalice" {
		t.Fatalf("leave event = %+v", ev[1])
	}

	joinAs(t, s, testTicket(t, s, "r1", "0xbob"), "")
	control(t, s, ctrlauth.KickPath, ctrlauth.KickRequest{Room: "r1", UserID: "0xbob", AtMs: time.Now().UnixMilli()})
	ev = waitEvents(t, gw, 4)
	if ev[3].Type != ctrlauth.EventLeave || ev[3].Reason != ctrlauth.KickedReason || ev[3].UserID != "0xbob" {
		t.Fatalf("kick leave event = %+v", ev[3])
	}
}

func TestRoom_closeReportsALeaveForEveryPeer(t *testing.T) {
	s := newAuthServer(t)
	gw := newSink(t, s.controlKey, http.StatusOK)
	s.roomManager.reporter.observe(gw.URL)
	joinAs(t, s, testTicket(t, s, "r1", "a"), "")
	joinAs(t, s, testTicket(t, s, "r1", "b"), "")
	waitEvents(t, gw, 2)

	s.roomManager.CloseAll()

	ev := waitEvents(t, gw, 4)
	closed := 0
	for _, e := range ev[2:] {
		if e.Type == ctrlauth.EventLeave && e.Reason == ctrlauth.ClosedReason {
			closed++
		}
	}
	if closed != 2 {
		t.Fatalf("closed leaves = %d in %+v, want 2", closed, ev)
	}
}
