//go:build e2e_fleet

package webrtc

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	// allocationLifetime is how long a TURN allocation lives without a
	// refresh (pion turn's default): the SFU's relay, refreshed with the
	// credential it was created with, dies about this long after that
	// credential's expiry.
	allocationLifetime = 600 * time.Second
	// longCall outlasts the SFU's former 600 s credential and the allocation
	// lifetime after it, with a margin.
	longCall      = sfuTTL + allocationLifetime + 3*time.Minute
	longCallEvery = 5 * time.Second
	glareBudget   = time.Minute
)

// noErrorFrames fails the test if the SFU sent p an error frame (offer_failed
// among them) so far.
func noErrorFrames(t *testing.T, name string, p *services.RTCPeer) {
	t.Helper()
	for {
		select {
		case m, ok := <-p.Messages:
			if !ok {
				return
			}
			if m.Type == services.MsgError {
				t.Errorf("%s was sent an error frame: %s", name, m.Data)
			}
		default:
			return
		}
	}
}

// TestSignal_joinGlareIsAnsweredNotRefused: a peer that publishes the moment
// it has joined offers while the SFU's own offer, carrying the tracks already
// in the room, is outstanding. The SFU yields: it answers the client's offer
// (no offer_failed) and offers its tracks again, so both directions carry
// media (docs/WEBRTC.md#negotiation-offer-glare-and-the-polite-sfu).
func TestSignal_joinGlareIsAnsweredNotRefused(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	room := "e2e-glare-" + fx.n.Name
	first := joinPeer(t, fx, 0, room, true)
	listener := joinPeer(t, fx, 2, room, false)
	waitMedia(t, []*services.RTCPeer{first}, []*services.RTCPeer{listener}) // the room now holds first's track

	late := joinPeer(t, fx, 1, room, true) // offers at once, as the SFU offers first's track
	waitMedia(t, []*services.RTCPeer{first, late}, []*services.RTCPeer{late, first, listener})
	for name, p := range map[string]*services.RTCPeer{"first": first, "late": late, "listener": listener} {
		noErrorFrames(t, name, p)
	}
}

// joinVideoPeer joins room through member i's gateway; a publisher also
// publishes a video track.
func joinVideoPeer(t *testing.T, fx *fixture, i int, room string, publish bool) *services.RTCPeer {
	t.Helper()
	c := fx.c.PinTo(fx.members[i%len(fx.members)].PublicIP)
	p, err := services.JoinRoom(t.Context(), c, member(t, fx.n, "runtime"), room, "video-"+strconv.Itoa(i))
	if err != nil {
		t.Fatalf("peer %d joining %s: %v", i, room, err)
	}
	t.Cleanup(p.Close)
	if publish {
		err = p.StartVideo()
	} else {
		err = p.Start(false)
	}
	if err != nil {
		t.Fatalf("peer %d: %v", i, err)
	}
	return p
}

// TestSignal_subscriberKeyframeRequestReachesPublisher: a subscriber's PLI on
// the video it receives is relayed by the SFU to the publisher of the track
// (docs/WEBRTC.md#keyframes-plifir).
func TestSignal_subscriberKeyframeRequestReachesPublisher(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	room := "e2e-keyframe-" + fx.n.Name
	pub := joinVideoPeer(t, fx, 0, room, true)
	sub := joinVideoPeer(t, fx, 1, room, false)

	eventually.Require(t, time.Second, mediaBudget, "the subscriber's PLI to reach the publisher", func() (bool, error) {
		if err := pub.PublishVideo(); err != nil {
			return false, err
		}
		if sub.Received() == 0 {
			return false, fmt.Errorf("the subscriber has received no video yet (ICE connected=%v)", sub.Connected())
		}
		if err := sub.RequestKeyframe(); err != nil {
			return false, err
		}
		if pub.KeyframeRequests() == 0 {
			return false, fmt.Errorf("the publisher has seen no PLI or FIR yet")
		}
		return true, nil
	})
}

// TestMedia_callOutlivesTheSFUsTURNCredential: the SFU's own relay credential
// used to be minted with the 600 s signalling TTL, so after it expired the
// SFU's TURN allocation could not be refreshed and media stopped. The call
// here lasts longer than that credential and the allocation lifetime after it,
// and media still flows (docs/WEBRTC.md#turn-credential-protocol). The
// clients use the 24 h REST credential so that only the SFU's side can fail.
func TestMedia_callOutlivesTheSFUsTURNCredential(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	room := "e2e-longcall-" + fx.n.Name
	_, rest := restCreds(t, fx.c, fx.token)
	join := func(i int, publish bool) *services.RTCPeer {
		c := fx.c.PinTo(fx.members[i%len(fx.members)].PublicIP)
		p, err := services.JoinRoom(t.Context(), c, member(t, fx.n, "runtime"), room, "long-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("peer %d joining %s: %v", i, room, err)
		}
		t.Cleanup(p.Close)
		p.Creds = rest
		if err := p.Start(publish); err != nil {
			t.Fatalf("peer %d: %v", i, err)
		}
		return p
	}
	pub, sub := join(0, true), join(1, false)
	waitMedia(t, []*services.RTCPeer{pub}, []*services.RTCPeer{sub})

	deadline := time.Now().Add(longCall)
	eventually.Require(t, longCallEvery, longCall+time.Minute, "the long call to run its course", func() (bool, error) {
		if err := pub.Publish(); err != nil {
			return false, eventually.Stop(fmt.Errorf("publishing during the long call: %w", err))
		}
		return !time.Now().Before(deadline), nil
	})
	before := sub.Received()
	eventually.Require(t, time.Second, mediaBudget, "media after the SFU's first credential expired", func() (bool, error) {
		if err := pub.Publish(); err != nil {
			return false, err
		}
		if got := sub.Received(); got <= before {
			return false, fmt.Errorf("the subscriber has received nothing since %d packets (ICE connected=%v)", before, sub.Connected())
		}
		return true, nil
	})
}
