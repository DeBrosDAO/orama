package sfu

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// sfuMidPrefix starts every mid the SFU assigns to a transceiver of its own.
// Browsers number theirs ("0", "1", ...), so the two sets never meet.
const sfuMidPrefix = "sfu"

// The a=setup values (RFC 4145) that matter to the DTLS role.
const (
	setupActive  = "active"
	setupPassive = "passive"
)

// Why glare is resolved like this. The SFU is the polite peer, which in the
// W3C API is a rollback of its own offer. pion (v4.2.22) does not implement
// rollback: its signaling state table has no transition out of have-local-offer
// but an answer, and SetLocalDescription(rollback) is refused. The only way
// back to stable is therefore an answer, and the only party that could
// provide one, the client, has deliberately ignored the offer. So the SFU
// writes that answer itself, as a stand-in for the client: yieldToClientOfferLocked.
//
// The stand-in also fixes the DTLS role when no answer did before; see
// standInSetup. An answer that came from nowhere must not change anything the real client
// has agreed to, and a mid collision must not let the client's new m-line be
// taken for one of the SFU's. Both are what claimMid and transportOf are for.

var (
	iceUfragLine   = regexp.MustCompile(`(?m)^a=ice-ufrag:.*$`)
	icePwdLine     = regexp.MustCompile(`(?m)^a=ice-pwd:.*$`)
	fingerprintRe  = regexp.MustCompile(`(?m)^a=fingerprint:.*$`)
	setupLine      = regexp.MustCompile(`(?m)^a=setup:.*$`)
	setupValue     = regexp.MustCompile(`(?m)^a=setup:(\w+)`)
	iceUfragValue  = regexp.MustCompile(`(?m)^a=ice-ufrag:(.*?)\r?$`)
	icePwdValue    = regexp.MustCompile(`(?m)^a=ice-pwd:(.*?)\r?$`)
	fingerprintVal = regexp.MustCompile(`(?m)^a=fingerprint:(.*?)\r?$`)
)

// claimMid gives the transceiver behind sender an SFU-owned mid unless it
// already has one. The caller holds sigMu.
func (p *Peer) claimMid(sender *webrtc.RTPSender) error {
	for _, t := range p.pc.GetTransceivers() {
		if t.Sender() != sender || t.Mid() != "" {
			continue
		}
		mid := fmt.Sprintf("%s%d", sfuMidPrefix, p.nextMid.Add(1))
		if err := t.SetMid(mid); err != nil {
			return fmt.Errorf("failed to assign mid %s to a track for peer %s: %w", mid, p.ID, err)
		}
	}
	return nil
}

// yieldToClientOfferLocked takes the SFU out of have-local-offer so that the
// client's offer, which wins, can be applied: the outstanding offer is answered
// with a stand-in answer that accepts it as it is (nothing is withdrawn, so the
// tracks stay attached to their transceivers) and carries the client's
// transport parameters. The offer is owed again afterwards, and goes out
// once the client's offer is answered. The caller holds sigMu.
func (p *Peer) yieldToClientOfferLocked(clientOffer string) error {
	if !p.limiter.allowGlareYield() {
		return fmt.Errorf("peer %s made the SFU yield its own offer more than %d times in %s: %w",
			p.ID, glareYieldsPerWindow, glareYieldWindow, ErrSignalRateLimited)
	}
	pending := p.pc.PendingLocalDescription()
	if pending == nil {
		return fmt.Errorf("peer %s is in have-local-offer without a pending offer", p.ID)
	}
	reference := clientOffer
	if current := p.pc.CurrentRemoteDescription(); current != nil {
		reference = current.SDP
	}
	answer, err := standInAnswer(p.room.api, pending.SDP, reference, p.standInSetup(clientOffer))
	if err != nil {
		return fmt.Errorf("failed to answer the SFU's own offer on glare: %w", err)
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answer,
	}); err != nil {
		return fmt.Errorf("failed to apply the stand-in answer on glare: %w", err)
	}

	p.negotiationMu.Lock()
	p.negotiationPending = true
	// An ICE restart in the yielded offer already happened locally; the answer
	// to the client's offer carries the new credentials.
	p.iceRestartWanted = false
	p.iceRestartOffered = false
	p.negotiationMu.Unlock()
	p.logger.Info("Offer glare: yielded to the client's offer, the SFU's own is owed again", zap.String("peer_id", p.ID))
	return nil
}

// standInAnswer builds an answer to offer as a throwaway client would give it,
// then points its transport parameters (ICE credentials, DTLS fingerprint) at
// those of reference, the real client's description, and sets its DTLS role to
// setup. A throwaway client's own parameters would, if applied, redirect ICE
// and DTLS to nobody.
func standInAnswer(api *webrtc.API, offer, reference, setup string) (string, error) {
	ufrag, pwd, fingerprint, err := transportOf(reference)
	if err != nil {
		return "", err
	}
	standIn, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", fmt.Errorf("failed to create the stand-in connection: %w", err)
	}
	defer standIn.Close()

	if err := standIn.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("stand-in refused the SFU's offer: %w", err)
	}
	answer, err := standIn.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("stand-in could not answer the SFU's offer: %w", err)
	}

	sdp := answer.SDP
	sdp = iceUfragLine.ReplaceAllLiteralString(sdp, "a=ice-ufrag:"+ufrag)
	sdp = icePwdLine.ReplaceAllLiteralString(sdp, "a=ice-pwd:"+pwd)
	sdp = fingerprintRe.ReplaceAllLiteralString(sdp, "a=fingerprint:"+fingerprint)
	sdp = setupLine.ReplaceAllLiteralString(sdp, "a=setup:"+setup)
	return sdp, nil
}

// standInSetup is the a=setup value of the stand-in answer, which is the one
// place the SFU's DTLS role gets fixed when glare hits before any answer did.
// The role is the complement of the stand-in's: a stand-in "passive" makes the
// SFU, the offerer, the DTLS client. The caller holds sigMu.
//
// Once a negotiation completed the role is already settled and the stand-in
// keeps it; otherwise it is the one the client's crossing offer implies:
// "active" means the client is the DTLS client, so the SFU must be the server,
// while "actpass" (what a client offers) leaves the choice to the SFU, which
// takes the client role, as pion does when it answers an actpass offer.
func (p *Peer) standInSetup(clientOffer string) string {
	sfuIsClient := setupOf(clientOffer) != setupActive
	local, remote := p.pc.CurrentLocalDescription(), p.pc.CurrentRemoteDescription()
	if local != nil && remote != nil {
		if local.Type == webrtc.SDPTypeAnswer {
			sfuIsClient = setupOf(local.SDP) == setupActive
		} else {
			sfuIsClient = setupOf(remote.SDP) == setupPassive
		}
	}
	if sfuIsClient {
		return setupPassive
	}
	return setupActive
}

// setupOf reads the first a=setup value of an SDP; "" when it has none.
func setupOf(sdp string) string {
	m := setupValue.FindStringSubmatch(sdp)
	if m == nil {
		return ""
	}
	return m[1]
}

// transportOf reads the ICE credentials and DTLS fingerprint of an SDP.
func transportOf(sdp string) (ufrag, pwd, fingerprint string, err error) {
	find := func(re *regexp.Regexp, what string) (string, error) {
		m := re.FindStringSubmatch(sdp)
		if m == nil {
			return "", fmt.Errorf("the client's description has no %s", what)
		}
		return strings.TrimSpace(m[1]), nil
	}
	if ufrag, err = find(iceUfragValue, "ice-ufrag"); err != nil {
		return
	}
	if pwd, err = find(icePwdValue, "ice-pwd"); err != nil {
		return
	}
	fingerprint, err = find(fingerprintVal, "fingerprint")
	return
}
