package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
)

type published struct {
	topic string
	data  []byte
}

type recorder struct {
	mu   sync.Mutex
	got  []published
	fail error
}

func (r *recorder) publish(_ context.Context, topic string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, published{topic, data})
	return nil
}

func eventHandlers(t *testing.T) (*WebRTCHandlers, *recorder) {
	t.Helper()
	h := controllerFor(t, newControlSFU(t, "a"))
	rec := &recorder{}
	h.SetEventSink(testSink, rec.publish)
	return h, rec
}

func postEvent(h *WebRTCHandlers, ev ctrlauth.MembershipEvent, secret string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(ev)
	return postEventRaw(h, body, secret)
}

func postEventRaw(h *WebRTCHandlers, body []byte, secret string) *httptest.ResponseRecorder {
	key, _ := ctrlauth.Key(secret)
	req := httptest.NewRequest(http.MethodPost, ctrlauth.EventsPath, bytes.NewReader(body))
	if key != nil {
		req.Header.Set(ctrlauth.MACHeader, ctrlauth.Sign(key, testSink, http.MethodPost, ctrlauth.EventsPath, body, time.Now()))
	}
	w := httptest.NewRecorder()
	h.EventsHandler(w, req)
	return w
}

func goodEvent(typ string) ctrlauth.MembershipEvent {
	return ctrlauth.MembershipEvent{Type: typ, Room: "r1", UserID: "0xalice", DeviceID: "phone", PeerID: "p1", At: time.Unix(1_800_000_000, 5).UTC()}
}

func TestEventsHandler_publishesJoinAndLeaveOnTheReservedTopic(t *testing.T) {
	h, rec := eventHandlers(t)

	leave := goodEvent(ctrlauth.EventLeave)
	leave.Reason = ctrlauth.KickedReason
	for _, ev := range []ctrlauth.MembershipEvent{goodEvent(ctrlauth.EventJoin), leave} {
		if w := postEvent(h, ev, testTURNSecret); w.Code != http.StatusOK {
			t.Fatalf("status %d %s", w.Code, w.Body)
		}
	}

	if len(rec.got) != 2 || rec.got[0].topic != "_orama/webrtc/r1" {
		t.Fatalf("published %+v", rec.got)
	}
	var join, left map[string]string
	_ = json.Unmarshal(rec.got[0].data, &join)
	_ = json.Unmarshal(rec.got[1].data, &left)
	if join["_orama"] != "webrtc.join" || join["user_id"] != "0xalice" || join["device_id"] != "phone" || join["room"] != "r1" || join["peer_id"] != "p1" {
		t.Fatalf("join message = %v", join)
	}
	if _, err := time.Parse(time.RFC3339Nano, join["at"]); err != nil {
		t.Errorf("at = %q: %v", join["at"], err)
	}
	if left["_orama"] != "webrtc.leave" || left["reason"] != "kicked" {
		t.Fatalf("leave message = %v", left)
	}
	if _, ok := join["reason"]; ok {
		t.Error("a join carries a reason")
	}
}

func TestEventsHandler_refusesWhatDoesNotComeFromThisNamespacesSFU(t *testing.T) {
	h, rec := eventHandlers(t)

	if w := postEvent(h, goodEvent(ctrlauth.EventJoin), "another-namespace"); w.Code != http.StatusUnauthorized {
		t.Errorf("another namespace's key: status %d", w.Code)
	}
	if w := postEvent(h, goodEvent(ctrlauth.EventJoin), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no MAC: status %d", w.Code)
	}
	if len(rec.got) != 0 {
		t.Fatalf("published %+v for unauthenticated callers", rec.got)
	}
}

func TestEventsHandler_rejectsMalformedEvents(t *testing.T) {
	h, rec := eventHandlers(t)
	bad := func(mut func(*ctrlauth.MembershipEvent)) ctrlauth.MembershipEvent {
		ev := goodEvent(ctrlauth.EventJoin)
		mut(&ev)
		return ev
	}
	for name, ev := range map[string]ctrlauth.MembershipEvent{
		"unknown type": bad(func(e *ctrlauth.MembershipEvent) { e.Type = "kick" }),
		"bad room":     bad(func(e *ctrlauth.MembershipEvent) { e.Room = "a b" }),
		"no user":      bad(func(e *ctrlauth.MembershipEvent) { e.UserID = "" }),
		"no peer":      bad(func(e *ctrlauth.MembershipEvent) { e.PeerID = "" }),
		"no time":      bad(func(e *ctrlauth.MembershipEvent) { e.At = time.Time{} }),
	} {
		if w := postEvent(h, ev, testTURNSecret); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}
	if w := postEventRaw(h, []byte("not json"), testTURNSecret); w.Code != http.StatusBadRequest {
		t.Errorf("not JSON: status %d", w.Code)
	}
	if len(rec.got) != 0 {
		t.Fatalf("published %+v for malformed events", rec.got)
	}
}

func TestEventsHandler_publishFailureIsAnErrorTheSFUSees(t *testing.T) {
	h, rec := eventHandlers(t)
	rec.fail = errors.New("pubsub down")
	w := postEvent(h, goodEvent(ctrlauth.EventJoin), testTURNSecret)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "pubsub down") {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
}

func TestEventsHandler_methodAndMissingConfiguration(t *testing.T) {
	h, _ := eventHandlers(t)
	w := httptest.NewRecorder()
	h.EventsHandler(w, httptest.NewRequest(http.MethodGet, ctrlauth.EventsPath, nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", w.Code)
	}
	h.publishEvent = nil
	if w := postEvent(h, goodEvent(ctrlauth.EventJoin), testTURNSecret); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no publisher: status %d", w.Code)
	}
}
