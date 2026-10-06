package sfu

import (
	"fmt"
	"time"

	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

const (
	// turnGatherPoll and turnGatherTimeout bound the wait for a running ICE
	// gathering to finish before the configuration is replaced.
	turnGatherPoll    = 100 * time.Millisecond
	turnGatherTimeout = 30 * time.Second
)

// turnRefreshLoop keeps the SFU's own TURN credential for this peer valid for
// as long as the peer lives.
//
// The SFU's PeerConnection relays through TURN (relay-only), and pion's TURN
// client refreshes its allocation with the credential it was created with,
// which the TURN server rejects once its timestamp has passed. Handing pion a
// new credential with SetConfiguration only reaches the next gathering ("the
// new servers will be used on the next ICE restart"), never an allocation that
// exists. So shortly before the credential would run out the loop does what
// the W3C API prescribes: SetConfiguration with a fresh credential, then an
// ICE restart, which gathers a new allocation authenticated with it. The
// credential's lifetime (sfuTURNCredentialTTL) is long enough that this
// happens once a day, not once per client credential refresh.
func (p *Peer) turnRefreshLoop() {
	for {
		select {
		case <-p.done:
			return
		case <-p.after(sfuTURNRefreshInterval):
		}
		if err := p.refreshTURNCredentials(); err != nil {
			// The credential will lapse and the relay with it; ending the
			// peer lets the client rejoin on a fresh one instead of
			// keeping a call whose media silently stops.
			p.logger.Error("TURN credential refresh failed, disconnecting peer", zap.Error(err))
			p.handleDisconnect()
			return
		}
	}
}

// refreshTURNCredentials swaps in a fresh TURN credential and schedules the
// ICE restart that makes the connection use it.
func (p *Peer) refreshTURNCredentials() error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	if err := p.swapTURNConfiguration(); err != nil {
		return err
	}

	p.negotiationMu.Lock()
	p.iceRestartWanted = true
	p.negotiationMu.Unlock()
	p.requestOffer()

	p.logger.Info("Refreshed the SFU's TURN credential, ICE restart requested")
	return nil
}

// swapTURNConfiguration replaces the ICE servers once no gathering is running.
// A gathering starts only under sigMu (an offer or an answer being made), so
// the check that matters is the one made while holding it: waiting happens
// outside the lock, which a long gathering must not keep from the signaling,
// and when a gathering started between the wait and the lock the wait begins
// again.
func (p *Peer) swapTURNConfiguration() error {
	for {
		if err := p.waitGatheringIdle(); err != nil {
			return err
		}
		swapped, err := p.setTURNConfigurationIfIdle()
		if err != nil || swapped {
			return err
		}
	}
}

// setTURNConfigurationIfIdle sets the fresh ICE servers under sigMu, which is
// what keeps pion's own reads of its configuration (offers, answers) apart
// from the write; false when a gathering is running.
func (p *Peer) setTURNConfigurationIfIdle() (bool, error) {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	if p.iceGathering() == webrtc.ICEGatheringStateGathering {
		return false, nil
	}
	cfg := p.pc.GetConfiguration()
	cfg.ICEServers = p.room.buildICEServers()
	if err := p.pc.SetConfiguration(cfg); err != nil {
		return false, fmt.Errorf("failed to set refreshed TURN credentials on peer %s: %w", p.ID, err)
	}
	return true, nil
}

// iceGathering is the gathering state of the connection; a test replaces it
// through gatheringState to interleave a gathering deterministically.
func (p *Peer) iceGathering() webrtc.ICEGatheringState {
	if p.gatheringState != nil {
		return p.gatheringState()
	}
	return p.pc.ICEGatheringState()
}

// waitGatheringIdle waits until the connection is not gathering candidates.
// pion's SetConfiguration rewrites the ICE agent's server list, which a
// gathering in progress is reading (a data race inside pion, seen under the
// race detector); outside a gathering it is safe.
func (p *Peer) waitGatheringIdle() error {
	deadline := time.Now().Add(turnGatherTimeout)
	for p.iceGathering() == webrtc.ICEGatheringStateGathering {
		if time.Now().After(deadline) {
			return fmt.Errorf("ICE gathering of peer %s did not finish within %s, cannot swap its TURN credentials", p.ID, turnGatherTimeout)
		}
		select {
		case <-p.done:
			return ErrPeerClosed
		case <-time.After(turnGatherPoll):
		}
	}
	return nil
}
