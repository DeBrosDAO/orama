package sfu

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func limiterAt(now *time.Time) *signalLimiter {
	l := newSignalLimiter()
	l.now = func() time.Time { return *now }
	return l
}

func TestSignalLimiter_burstThenRefill(t *testing.T) {
	now := time.Unix(1000, 0)
	l := limiterAt(&now)
	for i := 0; i < signalBurst; i++ {
		if !l.allowSignal() {
			t.Fatalf("message %d of the burst refused", i+1)
		}
	}
	if l.allowSignal() {
		t.Fatal("a message beyond the burst was allowed")
	}
	now = now.Add(time.Second)
	for i := 0; i < signalRefillPerSecond; i++ {
		if !l.allowSignal() {
			t.Fatalf("refilled message %d refused", i+1)
		}
	}
	if l.allowSignal() {
		t.Fatal("more than one second's refill was allowed")
	}
}

func TestSignalLimiter_idleTimeDoesNotBankPastTheBurst(t *testing.T) {
	now := time.Unix(1000, 0)
	l := limiterAt(&now)
	l.allowSignal()
	now = now.Add(time.Hour)
	allowed := 0
	for l.allowSignal() {
		allowed++
	}
	if allowed != signalBurst {
		t.Errorf("allowed %d messages after an hour idle, want the burst of %d", allowed, signalBurst)
	}
}

func TestSignalLimiter_glareYieldsAreCappedPerWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := limiterAt(&now)
	for i := 0; i < glareYieldsPerWindow; i++ {
		if !l.allowGlareYield() {
			t.Fatalf("yield %d refused", i+1)
		}
	}
	if l.allowGlareYield() {
		t.Fatal("a yield beyond the cap was allowed")
	}
	now = now.Add(glareYieldWindow + time.Second)
	if !l.allowGlareYield() {
		t.Fatal("the window never reopened")
	}
}

// A glare past the peer's cap is refused with the typed error rather than
// building yet another throwaway connection.
func TestHandleOffer_glareBeyondTheCapIsRateLimited(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "glare-cap")
	client.addAudioTrack("client-stream")
	sfuPeer := serverPeer(t, s, "glare-cap")
	published, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), "audio-pub", "pub-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sfuPeer.AddTrack(published); err != nil {
		t.Fatal(err)
	}
	client.expect(MessageTypeOffer)
	for i := 0; i < glareYieldsPerWindow; i++ {
		sfuPeer.limiter.allowGlareYield()
	}
	offer, err := client.pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := sfuPeer.HandleOffer(offer.SDP); !errors.Is(err, ErrSignalRateLimited) {
		t.Errorf("HandleOffer past the glare cap = %v, want ErrSignalRateLimited", err)
	}
}

// A client that floods offers is told why and disconnected.
func TestSignalingLoop_offerFloodClosesThePeer(t *testing.T) {
	s := newTestServer(t)
	conn := dialSignal(t, s, "room=flood")
	sendJoin(t, conn, "flood")
	flood(t, conn, MessageTypeOffer, json.RawMessage(`{"sdp":"not an sdp"}`))
	expectRateLimited(t, conn)
}

func TestSignalingLoop_iceCandidateFloodClosesThePeer(t *testing.T) {
	s := newTestServer(t)
	conn := dialSignal(t, s, "room=flood-ice")
	sendJoin(t, conn, "flood-ice")
	flood(t, conn, MessageTypeICECandidate, json.RawMessage(`{"candidate":"candidate:1"}`))
	expectRateLimited(t, conn)
}

// Messages the limiter does not cover are never throttled, and an ordinary
// negotiation stays far inside the allowance.
func TestSignalingLoop_normalNegotiationIsNotThrottled(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "calm-limit")
	client.addAudioTrack("client-stream")
	client.offer()
	client.expect(MessageTypeAnswer)
	for i := 0; i < 3*signalBurst; i++ {
		client.write(ClientMessage{Type: MessageTypeAudioState, Data: json.RawMessage(`{"enabled":true}`)})
	}
	client.write(ClientMessage{Type: MessageTypeICECandidate, Data: json.RawMessage(`{"candidate":"candidate:1"}`)})
	peer := serverPeer(t, s, "calm-limit")
	if peer.closed.Load() {
		t.Error("the peer was closed during a normal negotiation")
	}
}

func flood(t *testing.T, conn *websocket.Conn, typ MessageType, data json.RawMessage) {
	t.Helper()
	for i := 0; i < signalBurst+5; i++ {
		if err := conn.WriteJSON(ClientMessage{Type: typ, Data: data}); err != nil {
			return // the SFU already hung up
		}
	}
}

func expectRateLimited(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m rawFrame
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("socket ended without a %s frame: %v", rateLimitedCode, err)
		}
		if m.Type != MessageTypeError {
			continue
		}
		var e ErrorData
		if err := json.Unmarshal(m.Data, &e); err != nil {
			t.Fatal(err)
		}
		if e.Code == rateLimitedCode {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
