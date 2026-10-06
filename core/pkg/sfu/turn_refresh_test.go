package sfu

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"github.com/pion/webrtc/v4"
)

var localUfrag = regexp.MustCompile(`a=ice-ufrag:(\S+)`)

func iceUsername(t *testing.T, p *Peer) (username, credential string) {
	t.Helper()
	p.sigMu.Lock()
	servers := p.pc.GetConfiguration().ICEServers
	p.sigMu.Unlock()
	if len(servers) != 1 {
		t.Fatalf("peer has %d ICE servers, want 1", len(servers))
	}
	cred, _ := servers[0].Credential.(string)
	return servers[0].Username, cred
}

// The SFU's own credential must outlive any client credential: it is what its
// relay allocation refreshes with for the whole call.
func TestBuildICEServers_sfuCredentialLivesLongerThanAClientCredential(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("ttl")
	servers := room.buildICEServers()
	expiry, err := strconv.ParseInt(strings.SplitN(servers[0].Username, ":", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(time.Unix(expiry, 0))
	if remaining < sfuTURNCredentialTTL-time.Minute || remaining > sfuTURNCredentialTTL+time.Minute {
		t.Errorf("credential expires in %s, want about %s", remaining, sfuTURNCredentialTTL)
	}
	if sfuTURNRefreshInterval >= sfuTURNCredentialTTL {
		t.Errorf("refresh interval %s is not before the credential expires (%s)", sfuTURNRefreshInterval, sfuTURNCredentialTTL)
	}
	if !turn.ValidateCredentials(testConfig().TURNSecret, servers[0].Username, servers[0].Credential.(string), "test-ns") {
		t.Error("the TURN server would not accept the credential")
	}
}

// Refreshing swaps in a fresh credential and makes the next offer an ICE
// restart: the only way pion's already-created relay allocation is replaced by
// one authenticated with the new credential.
func TestRefreshTURNCredentials_swapsCredentialAndOffersICERestart(t *testing.T) {
	cfg := testConfig()
	// A TURN server that refuses at once, so the SFU's gathering finishes
	// instead of waiting on an address nothing answers.
	cfg.TURNServers = []TURNServerConfig{{Host: "127.0.0.1", Port: 1}}
	s, err := NewServer(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.roomManager.CloseAll)
	client := newGlareClient(t, s, "refresh")
	client.expect(MessageTypeTURNCredentials)
	client.addAudioTrack("client-stream")
	client.offer()
	answer := client.expect(MessageTypeAnswer)
	if err := client.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: client.sdpOf(answer),
	}); err != nil {
		t.Fatal(err)
	}
	peer := serverPeer(t, s, "refresh")
	waitFor(t, "the first negotiation and gathering to settle", func() bool {
		return peer.pc.SignalingState() == webrtc.SignalingStateStable &&
			peer.pc.CurrentLocalDescription() != nil &&
			peer.pc.ICEGatheringState() == webrtc.ICEGatheringStateComplete
	})
	beforeUser, beforeCred := iceUsername(t, peer)
	ufragBefore := localUfrag.FindStringSubmatch(peer.pc.CurrentLocalDescription().SDP)[1]

	s.config.TURNSecret = "a-rotated-secret-of-32-bytes-long!"
	if err := peer.refreshTURNCredentials(); err != nil {
		t.Fatal(err)
	}

	user, cred := iceUsername(t, peer)
	if cred == beforeCred || !turn.ValidateCredentials(s.config.TURNSecret, user, cred, "test-ns") {
		t.Errorf("credential after refresh (%q, was %q) is not a fresh valid one", user, beforeUser)
	}
	restart := client.expect(MessageTypeOffer)
	if got := localUfrag.FindStringSubmatch(client.sdpOf(restart))[1]; got == ufragBefore {
		t.Error("the offer after the refresh carries the old ICE credentials: no ICE restart")
	}
}

func TestRefreshTURNCredentials_uninitializedPeer(t *testing.T) {
	p := NewPeer("u", nil, nil, testLogger())
	if err := p.refreshTURNCredentials(); err != ErrPeerNotInitialized {
		t.Errorf("refreshTURNCredentials = %v, want ErrPeerNotInitialized", err)
	}
}

func TestRefreshTURNCredentials_closedConnectionIsAnError(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("closedpc")
	peer := addPeerWithTimer(t, room, func(time.Duration) <-chan time.Time { return nil })
	peer.pc.Close()

	err := peer.refreshTURNCredentials()
	if err == nil || !strings.Contains(err.Error(), "failed to set refreshed TURN credentials") {
		t.Errorf("refreshTURNCredentials on a closed connection = %v, want a wrapped error", err)
	}
}

// addPeerWithTimer joins a socketless peer to room whose refresh loop waits on
// after, which the test controls.
func addPeerWithTimer(t *testing.T, room *Room, after func(time.Duration) <-chan time.Time) *Peer {
	t.Helper()
	p := NewPeer("u", nil, room, testLogger())
	p.after = after
	if err := room.AddPeer(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// The loop refreshes when its timer fires, waits sfuTURNRefreshInterval, and
// ends with the peer.
func TestTurnRefreshLoop_refreshesOnTimerAndEndsWithPeer(t *testing.T) {
	cfg := testConfig()
	room := NewRoomManager(cfg, testLogger()).GetOrCreateRoom("loop")

	var waited atomic.Int64
	fire := make(chan time.Time, 1)
	peer := addPeerWithTimer(t, room, func(d time.Duration) <-chan time.Time {
		waited.Store(int64(d))
		return fire
	})
	_, credBefore := iceUsername(t, peer)
	cfg.TURNSecret = "a-rotated-secret-of-32-bytes-long!"
	fire <- time.Now()

	waitFor(t, "the loop to refresh", func() bool {
		_, cred := iceUsername(t, peer)
		return cred != credBefore
	})
	if got := time.Duration(waited.Load()); got != sfuTURNRefreshInterval {
		t.Errorf("loop waited %s, want %s", got, sfuTURNRefreshInterval)
	}

	peer.Close()
	waitFor(t, "the refresh loop to end with its peer", func() bool { return goroutinesIn("turnRefreshLoop") == 0 })
}

// A refresh that fails would let the credential lapse and the relay die
// silently: the peer is disconnected so the client rejoins on a fresh one.
func TestTurnRefreshLoop_failedRefreshDisconnectsThePeer(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("failloop")
	fire := make(chan time.Time, 1)
	peer := addPeerWithTimer(t, room, func(time.Duration) <-chan time.Time { return fire })
	peer.pc.Close() // the refresh will fail
	fire <- time.Now()

	waitFor(t, "the peer to leave the room", func() bool { return room.GetParticipantCount() == 0 })
	select {
	case <-peer.done:
	case <-time.After(settleTimeout):
		t.Error("the peer left the room without being closed")
	}
}

// A gathering that starts between the wait and the lock (an offer with an ICE
// restart does that) must not be raced by SetConfiguration: the check is made
// again under sigMu, and the swap happens only when that one finds it idle.
func TestRefreshTURNCredentials_gatheringStartingBeforeTheLockIsWaitedOut(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("interleave")
	peer := addPeerWithTimer(t, room, func(time.Duration) <-chan time.Time { return nil })
	_, credBefore := iceUsername(t, peer)
	// Credentials are derived from the clock in seconds; a rotated secret makes
	// the swap observable however fast the test runs.
	room.config.TURNSecret = "a-rotated-secret-of-32-bytes-long!"

	script := []webrtc.ICEGatheringState{
		webrtc.ICEGatheringStateGathering, // waiting
		webrtc.ICEGatheringStateComplete,  // wait over
		webrtc.ICEGatheringStateGathering, // under the lock: a gathering began in between
		webrtc.ICEGatheringStateComplete,  // waiting again
		webrtc.ICEGatheringStateComplete,  // under the lock: idle, swap
	}
	underLock := []bool{false, false, true, false, true}
	var calls int
	peer.gatheringState = func() webrtc.ICEGatheringState {
		i := calls
		calls++
		if i >= len(script) {
			t.Errorf("gathering state read %d times, want %d", i+1, len(script))
			return webrtc.ICEGatheringStateComplete
		}
		if locked := !peer.sigMu.TryLock(); locked != underLock[i] {
			t.Errorf("read %d: sigMu held = %t, want %t", i+1, locked, underLock[i])
		} else if !locked {
			peer.sigMu.Unlock()
		}
		return script[i]
	}

	if err := peer.swapTURNConfiguration(); err != nil {
		t.Fatal(err)
	}
	if calls != len(script) {
		t.Errorf("gathering state read %d times, want %d", calls, len(script))
	}
	if _, credAfter := iceUsername(t, peer); credAfter == credBefore {
		t.Error("the credential was never swapped in")
	}
}

func TestWaitGatheringIdle_peerClosedWhileGathering(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("gather-closed")
	peer := addPeerWithTimer(t, room, func(time.Duration) <-chan time.Time { return nil })
	peer.gatheringState = func() webrtc.ICEGatheringState { return webrtc.ICEGatheringStateGathering }
	peer.Close()

	if err := peer.waitGatheringIdle(); err != ErrPeerClosed {
		t.Errorf("waitGatheringIdle = %v, want ErrPeerClosed", err)
	}
}
