package sfu

import (
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// rtcpReader is the RTCP read side of an RTPSender or an RTPReceiver.
type rtcpReader interface {
	ReadRTCP() ([]rtcp.Packet, interceptor.Attributes, error)
}

// sendRTCP writes RTCP feedback to this peer.
func (p *Peer) sendRTCP(packets []rtcp.Packet) error {
	if p.rtcpOut != nil {
		return p.rtcpOut(packets)
	}
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	return p.pc.WriteRTCP(packets)
}

// wantsKeyframe reports whether packets contain a PLI or FIR.
func wantsKeyframe(packets []rtcp.Packet) bool {
	for _, pkt := range packets {
		switch pkt.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			return true
		}
	}
	return false
}

// readRTCP reads RTCP feedback and forwards PLI/FIR to the source peer
func (p *Peer) readRTCP(receiver rtcpReader, track *webrtc.TrackRemote) {
	localTrackID := track.Kind().String() + "-" + p.ID

	for {
		packets, _, err := receiver.ReadRTCP()
		if err != nil {
			return
		}
		if wantsKeyframe(packets) {
			p.room.RequestKeyframe(localTrackID)
		}
	}
}

// readSenderRTCP reads the RTCP this peer, as a subscriber, sends back about
// the track it receives, and relays its PLI/FIR to the publisher through the
// room. Reading is also what drains the sender's RTCP buffer, which the NACK
// responder and the other interceptors need to see their feedback. It ends
// when the sender stops: the track was removed or the connection closed.
func (p *Peer) readSenderRTCP(sender rtcpReader, trackID string) {
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if wantsKeyframe(packets) {
			p.room.RequestKeyframe(trackID)
		}
	}
}
