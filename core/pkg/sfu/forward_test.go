package sfu

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
)

// scriptedSource yields n packets, then io.EOF.
type scriptedSource struct{ n int }

func (s *scriptedSource) Read(b []byte) (int, interceptor.Attributes, error) {
	if s.n == 0 {
		return 0, nil, io.EOF
	}
	s.n--
	return copy(b, []byte{0x80, 0x6f}), nil, nil
}

// failingSink fails every write with err and counts them.
type failingSink struct {
	err    error
	writes int
}

func (s *failingSink) Write(b []byte) (int, error) {
	s.writes++
	return 0, s.err
}

// One subscriber's failed write must not end forwarding for the others.
func TestForwardRTP_writeErrorDoesNotStopForwarding(t *testing.T) {
	for name, err := range map[string]error{
		"closed subscriber":  io.ErrClosedPipe,
		"other write error":  errors.New("subscriber write failed"),
		"wrapped closed one": errors.Join(io.ErrClosedPipe, errors.New("another binding failed")),
	} {
		t.Run(name, func(t *testing.T) {
			sink := &failingSink{err: err}
			forwardRTP(&scriptedSource{n: 5}, sink, testLogger(), nil)
			if sink.writes != 5 {
				t.Errorf("sink saw %d writes, want all 5: forwarding stopped at the first error", sink.writes)
			}
		})
	}
}

func TestForwardRTP_sourceEndsImmediately(t *testing.T) {
	sink := &failingSink{}
	forwardRTP(&scriptedSource{n: 0}, sink, testLogger(), nil)
	if sink.writes != 0 {
		t.Errorf("sink saw %d writes from an empty source, want 0", sink.writes)
	}
}

// --- keyframe limiter ---

func TestKeyframeLimiter_firstSendsNowThenDefersThenCoalesces(t *testing.T) {
	l := newKeyframeLimiter(500 * time.Millisecond)
	t0 := time.Now()

	if a, _ := l.reserve(t0); a != keyframeSendNow {
		t.Fatalf("first request = %v, want send now", a)
	}
	a, wait := l.reserve(t0.Add(100 * time.Millisecond))
	if a != keyframeDefer || wait != 400*time.Millisecond {
		t.Fatalf("second request = %v wait %s, want defer for 400ms", a, wait)
	}
	if a, _ := l.reserve(t0.Add(200 * time.Millisecond)); a != keyframeCoalesced {
		t.Fatalf("third request = %v, want coalesced into the deferred one", a)
	}

	l.fired(t0.Add(500 * time.Millisecond))
	if a, _ := l.reserve(t0.Add(600 * time.Millisecond)); a != keyframeDefer {
		t.Fatalf("request right after the deferred PLI = %v, want defer", a)
	}
}

func TestKeyframeLimiter_sendsNowAfterInterval(t *testing.T) {
	l := newKeyframeLimiter(time.Second)
	t0 := time.Now()
	l.reserve(t0)
	if a, _ := l.reserve(t0.Add(time.Second)); a != keyframeSendNow {
		t.Errorf("request one interval later = %v, want send now", a)
	}
}

// --- subscriber RTCP -> publisher PLI ---

// scriptedRTCP returns each batch once, then io.EOF.
type scriptedRTCP struct {
	mu      sync.Mutex
	batches [][]rtcp.Packet
}

func (r *scriptedRTCP) ReadRTCP() ([]rtcp.Packet, interceptor.Attributes, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.batches) == 0 {
		return nil, nil, io.EOF
	}
	b := r.batches[0]
	r.batches = r.batches[1:]
	return b, nil, nil
}

// roomWithPublisher is a room holding a publisher whose RTCP is captured, and
// one published video track of it with the given limiter.
func roomWithPublisher(t *testing.T, limiter *keyframeLimiter) (*Room, *[]rtcp.Packet, *sync.Mutex) {
	t.Helper()
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("kf")
	var mu sync.Mutex
	var got []rtcp.Packet
	pub := NewPeer("pub", nil, room, testLogger())
	pub.rtcpOut = func(p []rtcp.Packet) error {
		mu.Lock()
		got = append(got, p...)
		mu.Unlock()
		return nil
	}
	room.peers[pub.ID] = pub
	room.publishedTracks["video-"+pub.ID] = &publishedTrack{
		sourcePeerID: pub.ID, kind: "video", remoteTrackSSRC: 4242, keyframes: limiter,
	}
	return room, &got, &mu
}

func TestReadSenderRTCP_pliReachesPublisherAsPLIForItsSSRC(t *testing.T) {
	room, got, mu := roomWithPublisher(t, newKeyframeLimiter(time.Millisecond))
	sub := NewPeer("sub", nil, room, testLogger())
	trackID := "video-" + room.snapshotPeers("")[0].ID

	sub.readSenderRTCP(&scriptedRTCP{batches: [][]rtcp.Packet{
		{&rtcp.ReceiverReport{}},                    // not a keyframe request
		{&rtcp.PictureLossIndication{MediaSSRC: 1}}, // subscriber's own SSRC view
	}}, trackID)

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("publisher received %d packets, want exactly 1 PLI", len(*got))
	}
	pli, ok := (*got)[0].(*rtcp.PictureLossIndication)
	if !ok || pli.MediaSSRC != 4242 {
		t.Errorf("publisher got %#v, want a PLI for the track's SSRC 4242", (*got)[0])
	}
}

func TestReadSenderRTCP_firIsRelayedAndBurstIsRateLimited(t *testing.T) {
	room, got, mu := roomWithPublisher(t, newKeyframeLimiter(80*time.Millisecond))
	sub := NewPeer("sub", nil, room, testLogger())
	trackID := "video-" + room.snapshotPeers("")[0].ID

	burst := make([][]rtcp.Packet, 20)
	for i := range burst {
		burst[i] = []rtcp.Packet{&rtcp.FullIntraRequest{}}
	}
	sub.readSenderRTCP(&scriptedRTCP{batches: burst}, trackID)

	time.Sleep(200 * time.Millisecond) // the deferred PLI
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 2 {
		t.Errorf("publisher received %d PLIs for a burst of 20 FIRs, want 2 (one now, one deferred)", len(*got))
	}
}

func TestRequestKeyframe_audioAndUnknownTracksAreIgnored(t *testing.T) {
	room, got, mu := roomWithPublisher(t, newKeyframeLimiter(time.Millisecond))
	room.publishedTracks["audio-x"] = &publishedTrack{kind: "audio", keyframes: newKeyframeLimiter(time.Millisecond)}
	room.RequestKeyframe("audio-x")
	room.RequestKeyframe("no-such-track")
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Errorf("publisher received %d packets, want 0", len(*got))
	}
}

func TestWantsKeyframe(t *testing.T) {
	if wantsKeyframe(nil) {
		t.Error("nil packets want no keyframe")
	}
	if wantsKeyframe([]rtcp.Packet{&rtcp.ReceiverReport{}, &rtcp.TransportLayerNack{}}) {
		t.Error("RR/NACK are not keyframe requests")
	}
	if !wantsKeyframe([]rtcp.Packet{&rtcp.ReceiverReport{}, &rtcp.PictureLossIndication{}}) {
		t.Error("PLI is a keyframe request")
	}
}
