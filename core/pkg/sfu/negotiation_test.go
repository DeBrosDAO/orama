package sfu

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

const settleTimeout = 10 * time.Second

// glareClient is a pion client speaking the SFU's signaling protocol over a
// real WebSocket. Like AnChat it is the impolite peer: it never rolls back,
// and ignores an SFU offer that arrives while its own is outstanding.
type glareClient struct {
	t    *testing.T
	pc   *webrtc.PeerConnection
	conn *websocket.Conn

	// writeMu serializes writes once trickle ICE sends from pion's goroutines.
	writeMu sync.Mutex
	// held are the SFU's ICE candidates that arrived before the client had a
	// remote description to apply them to.
	held []ICECandidateData
}

func newGlareClient(t *testing.T, s *Server, roomID string) *glareClient {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	conn := dialSignal(t, s, "room="+roomID)
	sendJoin(t, conn, roomID)
	c := &glareClient{t: t, pc: pc, conn: conn}
	c.expect(MessageTypeWelcome)
	return c
}

// next returns the next frame that is not an ICE candidate, failing the test
// on an error frame.
func (c *glareClient) next() rawFrame {
	c.t.Helper()
	for {
		m := readFrame(c.t, c.conn)
		switch m.Type {
		case MessageTypeICECandidate:
			c.addCandidate(m)
			continue
		case MessageTypeError:
			c.t.Fatalf("SFU sent an error frame: %s", m.Data)
		}
		return m
	}
}

func (c *glareClient) expect(want MessageType) rawFrame {
	c.t.Helper()
	m := c.next()
	if m.Type != want {
		c.t.Fatalf("frame = %s %s, want %s", m.Type, m.Data, want)
	}
	return m
}

func (c *glareClient) send(typ MessageType, sdp string) {
	c.t.Helper()
	data, _ := json.Marshal(OfferData{SDP: sdp})
	c.write(ClientMessage{Type: typ, Data: data})
}

func (c *glareClient) write(m ClientMessage) {
	c.t.Helper()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteJSON(m); err != nil {
		c.t.Errorf("send %s: %v", m.Type, err)
	}
}

func (c *glareClient) sdpOf(m rawFrame) string {
	c.t.Helper()
	var d OfferData
	if err := json.Unmarshal(m.Data, &d); err != nil {
		c.t.Fatal(err)
	}
	return d.SDP
}

// offer makes the client's own offer, sets it locally and sends it.
func (c *glareClient) offer() {
	c.t.Helper()
	o, err := c.pc.CreateOffer(nil)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.pc.SetLocalDescription(o); err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeOffer, o.SDP)
}

// answer applies an SFU offer and sends the answer.
func (c *glareClient) answer(sfuOffer string) {
	c.t.Helper()
	if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sfuOffer}); err != nil {
		c.t.Fatal(err)
	}
	a, err := c.pc.CreateAnswer(nil)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.pc.SetLocalDescription(a); err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeAnswer, a.SDP)
}

func opusCapability() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
}

func (c *glareClient) addAudioTrack(streamID string) *webrtc.TrackLocalStaticRTP {
	c.t.Helper()
	track, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), "audio-client", streamID)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.pc.AddTrack(track); err != nil {
		c.t.Fatal(err)
	}
	return track
}

// newTestServer is a server whose rooms run without TURN: nothing in these
// tests can reach one, and the SFU's relay-only connection then gathers
// nothing, which is all the signaling needs.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(testConfig(), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	s.config.TURNServers = nil
	s.config.TURNSecret = ""
	t.Cleanup(s.roomManager.CloseAll)
	return s
}

func serverPeer(t *testing.T, s *Server, roomID string) *Peer {
	t.Helper()
	room := s.roomManager.GetRoom(roomID)
	if room == nil {
		t.Fatalf("room %s does not exist", roomID)
	}
	peers := room.snapshotPeers("")
	if len(peers) != 1 {
		t.Fatalf("room %s has %d peers, want 1", roomID, len(peers))
	}
	return peers[0]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The SFU offers a track at the same moment the client offers its own. The SFU
// rolls its offer back, answers the client's, and offers again: both ends end
// up stable with each other's tracks and no offer_failed is sent.
func TestHandleOffer_glareRollsBackAndReoffersTheTrack(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "glare")
	client.addAudioTrack("client-stream")
	sfuPeer := serverPeer(t, s, "glare")

	published, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), "audio-pub", "pub-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sfuPeer.AddTrack(published); err != nil {
		t.Fatal(err)
	}
	// The SFU's offer is out; the client does not answer it but offers too.
	client.expect(MessageTypeOffer)
	client.offer()

	// The SFU answers the client's offer, then offers the track again.
	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	reoffer := client.expect(MessageTypeOffer)
	if !strings.Contains(client.sdpOf(reoffer), "pub-stream") {
		t.Fatal("the SFU's second offer lost the track its rolled-back offer carried")
	}
	client.answer(client.sdpOf(reoffer))

	waitFor(t, "the SFU to be stable", func() bool {
		return sfuPeer.pc.SignalingState() == webrtc.SignalingStateStable &&
			sfuPeer.pc.CurrentLocalDescription() != nil &&
			strings.Contains(sfuPeer.pc.CurrentLocalDescription().SDP, "pub-stream")
	})
	if !strings.Contains(sfuPeer.pc.CurrentRemoteDescription().SDP, "client-stream") {
		t.Error("the SFU never applied the client's track")
	}
	if got := client.pc.SignalingState(); got != webrtc.SignalingStateStable {
		t.Errorf("client signaling state = %s, want stable", got)
	}
}

// With no glare an offer from the SFU is answered as before.
func TestHandleOffer_noGlareAnswersNormally(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "calm")
	client.addAudioTrack("client-stream")
	client.offer()

	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	sfuPeer := serverPeer(t, s, "calm")
	waitFor(t, "the SFU to be stable", func() bool {
		return sfuPeer.pc.SignalingState() == webrtc.SignalingStateStable
	})
}

// A garbled offer is reported to the client (offer_failed in the signaling
// loop) and leaves the connection usable.
func TestHandleOffer_garbledSDPIsAnError(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "garbled")
	sfuPeer := serverPeer(t, s, "garbled")

	err := sfuPeer.HandleOffer("not an sdp")
	if err == nil {
		t.Fatal("HandleOffer accepted a garbled SDP")
	}
	if !strings.Contains(err.Error(), "failed to apply client offer") {
		t.Errorf("error = %v, want it wrapped with context", err)
	}
	if got := sfuPeer.pc.SignalingState(); got != webrtc.SignalingStateStable {
		t.Errorf("signaling state after a bad offer = %s, want stable", got)
	}
	_ = client
}

func TestHandleOffer_uninitializedPeer(t *testing.T) {
	p := NewPeer("u", nil, nil, testLogger())
	if err := p.HandleOffer("x"); err != ErrPeerNotInitialized {
		t.Errorf("HandleOffer = %v, want ErrPeerNotInitialized", err)
	}
}

// Tracks added while a batch is open produce a single offer when it ends.
func TestTrackBatch_oneOfferAfterEnd(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "batch")
	sfuPeer := serverPeer(t, s, "batch")

	sfuPeer.StartTrackBatch()
	for _, id := range []string{"audio-a", "audio-b"} {
		tr, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), id, "stream-"+id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sfuPeer.AddTrack(tr); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := sfuPeer.pc.SignalingState(); got != webrtc.SignalingStateStable {
		t.Fatalf("signaling state while the batch is open = %s: an offer was made", got)
	}

	sfuPeer.EndTrackBatch()
	offer := client.expect(MessageTypeOffer)
	sdp := client.sdpOf(offer)
	if !strings.Contains(sdp, "stream-audio-a") || !strings.Contains(sdp, "stream-audio-b") {
		t.Error("the single offer after the batch does not carry both tracks")
	}
}

// Glare after the call is established: the client already publishes one track,
// then the SFU offers a subscription while the client offers a second track.
// The client's new m-line and the SFU's must not be taken for each other.
func TestHandleOffer_glareMidCallKeepsBothSidesTracks(t *testing.T) {
	s := newTestServer(t)
	client := newGlareClient(t, s, "midcall")
	client.addAudioTrack("first-stream")
	client.offer()
	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	sfuPeer := serverPeer(t, s, "midcall")
	waitFor(t, "the first negotiation to settle", func() bool {
		return sfuPeer.pc.SignalingState() == webrtc.SignalingStateStable && sfuPeer.pc.CurrentRemoteDescription() != nil
	})

	published, err := webrtc.NewTrackLocalStaticRTP(opusCapability(), "audio-pub", "pub-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sfuPeer.AddTrack(published); err != nil {
		t.Fatal(err)
	}
	// pion queues negotiation-needed behind transport start-up, which cannot
	// finish here (nothing to connect to), so the owed offer is requested
	// directly.
	sfuPeer.requestOffer()
	client.expect(MessageTypeOffer) // ignored by the client, which offers instead
	client.addAudioTrack("second-stream")
	client.offer()

	answer = client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	reoffer := client.expect(MessageTypeOffer)
	client.answer(client.sdpOf(reoffer))

	waitFor(t, "the SFU to hold both tracks of the client and its own", func() bool {
		local, remote := sfuPeer.pc.CurrentLocalDescription(), sfuPeer.pc.CurrentRemoteDescription()
		return sfuPeer.pc.SignalingState() == webrtc.SignalingStateStable &&
			local != nil && remote != nil &&
			strings.Contains(local.SDP, "pub-stream") &&
			strings.Contains(remote.SDP, "first-stream") && strings.Contains(remote.SDP, "second-stream")
	})
}
