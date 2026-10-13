package webrtc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
)

// NEW-1: the mute carries the gateway's clock, which the SFU orders it against
// the tickets by.
func TestMute_requestCarriesTheGatewaysClock(t *testing.T) {
	sfu := newControlSFU(t, "a")
	h := controllerFor(t, sfu)
	at := time.Now().Truncate(time.Millisecond)
	h.now = func() time.Time { return at }

	if err := h.Mute(bg, "ns", "r1", "alice", true); err != nil {
		t.Fatal(err)
	}
	if len(sfu.mutes) != 1 || sfu.mutes[0].AtMs != at.UnixMilli() {
		t.Fatalf("SFU mutes = %+v, want AtMs %d", sfu.mutes, at.UnixMilli())
	}
}

// LOW-A: a report stamped for another gateway is refused here.
func TestEventsHandler_aStampForAnotherGatewayIsRefused(t *testing.T) {
	h, rec := eventHandlers(t)
	body := []byte(`{"type":"join","room":"r1","user_id":"0xalice","peer_id":"p1","at":"2027-01-01T00:00:00Z"}`)
	key, _ := ctrlauth.Key(testTURNSecret)
	for _, sink := range []string{"http://10.0.0.4:10004", "http://10.0.0.3:10005", ""} {
		req := httptest.NewRequest(http.MethodPost, ctrlauth.EventsPath, strings.NewReader(string(body)))
		req.Header.Set(ctrlauth.MACHeader, ctrlauth.Sign(key, sink, http.MethodPost, ctrlauth.EventsPath, body, time.Now()))
		w := httptest.NewRecorder()
		h.EventsHandler(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("a stamp for %q: status %d, want 401", sink, w.Code)
		}
	}
	if len(rec.got) != 0 {
		t.Fatalf("published %d events from stamps for another gateway", len(rec.got))
	}
}

// LOW-A: the SFU is signed to by its own address, so a stamp made for one SFU
// is not accepted by its sibling (the fake SFU verifies as the real one does).
func TestControl_stampNamesTheSFUItIsFor(t *testing.T) {
	a, b := newControlSFU(t, "a"), newControlSFU(t, "b")
	h := controllerFor(t, a)
	h.SetSFUDirectory(staticDirectory{nodes: []SFUNode{a.node, b.node}})

	if err := h.Kick(bg, "ns", "r1", "alice"); err != nil {
		t.Fatal(err)
	}
	if len(a.kicks) != 1 || len(b.kicks) != 1 {
		t.Fatalf("each SFU should accept the stamp made for it: a=%d b=%d", len(a.kicks), len(b.kicks))
	}
}
