package sfu

import (
	"fmt"

	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Negotiation. The SFU offers whenever it has something to tell the client
// (a track to send, an ICE restart); the client offers when it publishes. Both
// can happen at once, which is "glare". AnChat's iOS client cannot be the
// polite side (its rollback breaks audio), so the SFU is: when the client's
// offer arrives while the SFU's own is outstanding, the SFU rolls its offer
// back, answers the client's, and offers again afterwards.
//
// Every SDP exchange runs under sigMu, so offers and answers never interleave
// and a callback from pion cannot offer in the middle of answering. Whether an
// offer is owed is kept apart, in negotiationPending: it is set whenever
// something needs offering and cleared only when an offer was actually created,
// so a rolled-back offer's changes are still owed afterwards.

// initNegotiation wires pion's negotiation events to the peer. pion calls both
// handlers from its own goroutines, one of them while its operation queue is
// busy, so each hands off to a goroutine and never blocks.
func (p *Peer) initNegotiation() {
	p.pc.OnNegotiationNeeded(func() { go p.requestOffer() })
	p.pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		if state == webrtc.SignalingStateStable {
			p.flushOffer()
		}
	})
}

// requestOffer records that the client is owed an offer and sends it if the
// connection allows it now.
func (p *Peer) requestOffer() {
	p.negotiationMu.Lock()
	p.negotiationPending = true
	p.negotiationMu.Unlock()
	p.flushOffer()
}

// flushOffer sends the owed offer when there is one, no track batch is being
// assembled, and the connection is stable. When any of those is not true the
// debt stays, and the event that changes it (end of batch, back to stable)
// calls flushOffer again.
func (p *Peer) flushOffer() {
	if p.pc == nil || p.closed.Load() {
		return
	}
	p.sigMu.Lock()
	defer p.sigMu.Unlock()

	p.negotiationMu.Lock()
	ready := p.negotiationPending && !p.batchingTracks &&
		p.pc.SignalingState() == webrtc.SignalingStateStable
	if ready {
		p.negotiationPending = false
	}
	p.negotiationMu.Unlock()
	if !ready {
		return
	}

	if err := p.sendOfferLocked(); err != nil {
		p.negotiationMu.Lock()
		p.negotiationPending = true
		p.negotiationMu.Unlock()
		p.logger.Error("Failed to send offer", zap.Error(err))
	}
}

// sendOfferLocked creates the SFU's offer, with an ICE restart when one is
// wanted, and sends it. The caller holds sigMu.
func (p *Peer) sendOfferLocked() error {
	p.negotiationMu.Lock()
	restart := p.iceRestartWanted
	p.negotiationMu.Unlock()

	offer, err := p.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: restart})
	if err != nil {
		return fmt.Errorf("failed to create offer: %w", err)
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set local offer: %w", err)
	}
	if restart {
		p.negotiationMu.Lock()
		p.iceRestartOffered = true
		p.negotiationMu.Unlock()
	}
	if err := p.SendMessage(NewServerMessage(MessageTypeOffer, &OfferData{SDP: offer.SDP})); err != nil {
		return fmt.Errorf("failed to send offer: %w", err)
	}
	return nil
}

// HandleOffer processes an SDP offer from the client. If the SFU's own offer is
// outstanding it is rolled back first (the SFU is the polite peer) and offered
// again once the client's offer is answered.
func (p *Peer) HandleOffer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	p.sigMu.Lock()
	defer p.sigMu.Unlock()

	if p.pc.SignalingState() == webrtc.SignalingStateHaveLocalOffer {
		if err := p.yieldToClientOfferLocked(sdp); err != nil {
			return err
		}
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: sdp,
	}); err != nil {
		return fmt.Errorf("failed to apply client offer: %w", err)
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %w", err)
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("failed to set local answer: %w", err)
	}
	if err := p.SendMessage(NewServerMessage(MessageTypeAnswer, &AnswerData{SDP: answer.SDP})); err != nil {
		return fmt.Errorf("failed to send answer: %w", err)
	}
	return nil
}

// HandleAnswer processes an SDP answer from the client
func (p *Peer) HandleAnswer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	p.sigMu.Lock()
	defer p.sigMu.Unlock()

	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: sdp,
	}); err != nil {
		return fmt.Errorf("failed to apply client answer: %w", err)
	}
	p.negotiationMu.Lock()
	if p.iceRestartOffered {
		p.iceRestartWanted = false
		p.iceRestartOffered = false
	}
	p.negotiationMu.Unlock()
	return nil
}

// HandleICECandidate adds a remote ICE candidate
func (p *Peer) HandleICECandidate(data *ICECandidateData) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	return p.pc.AddICECandidate(data.ToWebRTCCandidate())
}

// AddTrack adds a local track to send to this peer and starts reading the
// RTCP the peer sends back for it, which is how its keyframe requests reach
// the publisher. The transceiver gets an SFU-owned mid before any offer can
// assign it a numeric one (offers are made under sigMu, as is this), so that
// it can never collide with a mid the client picks for its own new m-line.
func (p *Peer) AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	if p.pc == nil {
		return nil, ErrPeerNotInitialized
	}
	p.sigMu.Lock()
	sender, err := p.pc.AddTrack(track)
	if err == nil {
		err = p.claimMid(sender)
	}
	p.sigMu.Unlock()
	if err != nil {
		return nil, err
	}
	go p.readSenderRTCP(sender, track.ID())
	return sender, nil
}

// StartTrackBatch suppresses renegotiation during bulk track additions
func (p *Peer) StartTrackBatch() {
	p.negotiationMu.Lock()
	p.batchingTracks = true
	p.negotiationMu.Unlock()
}

// EndTrackBatch ends batching and sends the offer the batch made owed.
func (p *Peer) EndTrackBatch() {
	p.negotiationMu.Lock()
	p.batchingTracks = false
	p.negotiationMu.Unlock()
	p.flushOffer()
}
