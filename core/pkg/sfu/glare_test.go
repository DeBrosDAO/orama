package sfu

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const rtpInterval = 20 * time.Millisecond

// trickle makes the client send its own ICE candidates to the SFU, which the
// glare tests do not need but a test that connects does.
func (c *glareClient) trickle() {
	c.pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		init := cand.ToJSON()
		data := &ICECandidateData{Candidate: init.Candidate}
		if init.SDPMid != nil {
			data.SDPMid = *init.SDPMid
		}
		if init.SDPMLineIndex != nil {
			data.SDPMLineIndex = *init.SDPMLineIndex
		}
		raw, _ := json.Marshal(data)
		c.write(ClientMessage{Type: MessageTypeICECandidate, Data: raw})
	})
}

func (c *glareClient) addCandidate(m rawFrame) {
	var d ICECandidateData
	if err := json.Unmarshal(m.Data, &d); err != nil {
		return
	}
	c.held = append(c.held, d)
	c.applyHeld()
}

func (c *glareClient) applyHeld() {
	if c.pc.RemoteDescription() == nil {
		return
	}
	for _, d := range c.held {
		_ = c.pc.AddICECandidate(d.ToWebRTCCandidate())
	}
	c.held = nil
}

// pump keeps applying the SFU's candidates after the negotiation, until the
// socket closes.
func (c *glareClient) pump() {
	go func() {
		for {
			var m rawFrame
			if err := c.conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type == MessageTypeICECandidate {
				c.addCandidate(m)
			}
		}
	}()
}

// The DTLS role is fixed by the first answer, and on glare the first answer is
// the stand-in. It must be the complement of what the client's crossing offer
// implies.
func TestStandInSetup_firstNegotiationFollowsTheClientOffer(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("setup-first")
	peer := addPeerWithTimer(t, room, func(time.Duration) <-chan time.Time { return nil })

	tests := []struct {
		name        string
		clientSetup string
		want        string
	}{
		{"actpass leaves the choice to the SFU, which is the DTLS client", "actpass", setupPassive},
		{"active client makes the SFU the DTLS server", "active", setupActive},
		{"passive client makes the SFU the DTLS client", "passive", setupPassive},
		{"no setup line defaults to the actpass behaviour", "", setupPassive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offer := "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"
			if tt.clientSetup != "" {
				offer += "a=setup:" + tt.clientSetup + "\r\n"
			}
			if got := peer.standInSetup(offer); got != tt.want {
				t.Errorf("standInSetup(%q) = %q, want %q", tt.clientSetup, got, tt.want)
			}
		})
	}
}

// After a negotiation the role is settled: a crossing offer that says something
// else (a client restating "active") must not move it.
func TestStandInSetup_establishedRoleIsKept(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "setup-kept")
	client.addAudioTrack("first-stream")
	client.offer()
	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	peer := serverPeer(t, s, "setup-kept")
	waitFor(t, "the first negotiation to settle", func() bool {
		return peer.pc.SignalingState() == webrtc.SignalingStateStable && peer.pc.CurrentRemoteDescription() != nil
	})

	sfuAnswer := setupOf(peer.pc.CurrentLocalDescription().SDP)
	want := setupPassive // the SFU answered as the DTLS client
	if sfuAnswer == setupPassive {
		want = setupActive
	}
	peer.sigMu.Lock()
	got := peer.standInSetup("v=0\r\na=setup:" + setupPassive + "\r\n")
	peer.sigMu.Unlock()
	if got != want {
		t.Errorf("standInSetup = %q after the SFU answered %q, want %q", got, sfuAnswer, want)
	}
}

// Glare on the very first SFU offer, before any answer has fixed a DTLS role:
// the stand-in fixes it, and the client's own negotiation that follows must
// agree. DTLS connects on both ends and media flows both ways.
func TestHandleOffer_glareOnFirstOfferConnectsDTLSAndFlowsMedia(t *testing.T) {
	s := newTestServer(t)
	s.roomManager.GetOrCreateRoom("first-glare").allowDirectICE = true
	client := newGlareClient(t, s, "first-glare")
	client.trickle()
	sfuPeer := serverPeer(t, s, "first-glare")

	published, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), "audio-pub", "pub-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sfuPeer.AddTrack(published); err != nil {
		t.Fatal(err)
	}
	var received atomic.Bool
	client.pc.OnTrack(func(*webrtc.TrackRemote, *webrtc.RTPReceiver) { received.Store(true) })
	clientTrack := client.addAudioTrack("client-stream")

	// The SFU's first offer is out and unanswered when the client offers.
	client.expect(MessageTypeOffer)
	client.offer()
	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	client.applyHeld()
	reoffer := client.expect(MessageTypeOffer)
	client.answer(client.sdpOf(reoffer))
	client.applyHeld()
	client.pump()

	waitFor(t, "DTLS to connect on both ends", func() bool {
		return client.pc.ConnectionState() == webrtc.PeerConnectionStateConnected &&
			sfuPeer.pc.ConnectionState() == webrtc.PeerConnectionStateConnected
	})

	stop := make(chan struct{})
	defer close(stop)
	go writeRTP(clientTrack, stop)
	go writeRTP(published, stop)
	waitFor(t, "media to flow client to SFU", func() bool {
		sfuPeer.room.publishedTracksMu.RLock()
		defer sfuPeer.room.publishedTracksMu.RUnlock()
		return len(sfuPeer.room.publishedTracks) > 0
	})
	waitFor(t, "media to flow SFU to client", received.Load)
	if !strings.Contains(sfuPeer.pc.CurrentRemoteDescription().SDP, "client-stream") {
		t.Error("the SFU never applied the client's track")
	}
}

func writeRTP(track *webrtc.TrackLocalStaticRTP, stop <-chan struct{}) {
	var seq uint16
	ticker := time.NewTicker(rtpInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			seq++
			_ = track.WriteRTP(&rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 960},
				Payload: []byte{0x78, 0x01, 0x02, 0x03},
			})
		}
	}
}
