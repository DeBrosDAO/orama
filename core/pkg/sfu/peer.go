package sfu

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

var (
	ErrPeerNotInitialized = errors.New("peer connection not initialized")
	ErrPeerClosed         = errors.New("peer is closed")
	ErrWebSocketClosed    = errors.New("websocket connection closed")
)

// Peer represents a participant in a room with a WebRTC PeerConnection.
type Peer struct {
	ID     string
	UserID string

	pc   *webrtc.PeerConnection
	conn *websocket.Conn
	room *Room

	// Negotiation state machine
	negotiationPending bool
	batchingTracks     bool
	negotiationMu      sync.Mutex

	// Connection state
	closed   bool
	closedMu sync.RWMutex
	connMu   sync.Mutex

	logger  *zap.Logger
	onClose func(*Peer)
}

// NewPeer creates a new peer
func NewPeer(userID string, conn *websocket.Conn, room *Room, logger *zap.Logger) *Peer {
	return &Peer{
		ID:     uuid.New().String(),
		UserID: userID,
		conn:   conn,
		room:   room,
		logger: logger.With(zap.String("peer_id", "")), // Updated after ID assigned
	}
}

// InitPeerConnection creates and configures the WebRTC PeerConnection.
func (p *Peer) InitPeerConnection(api *webrtc.API, iceServers []webrtc.ICEServer) error {
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers:         iceServers,
		ICETransportPolicy: webrtc.ICETransportPolicyRelay, // Force TURN relay
	})
	if err != nil {
		return err
	}
	p.pc = pc
	p.logger = p.logger.With(zap.String("peer_id", p.ID))

	// ICE connection state changes
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		p.logger.Info("ICE state changed", zap.String("state", state.String()))

		switch state {
		case webrtc.ICEConnectionStateDisconnected:
			// Give 15 seconds to reconnect before removing
			go p.handleReconnectTimeout()
		case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateClosed:
			p.handleDisconnect()
		}
	})

	// ICE candidate generation
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		c := candidate.ToJSON()
		data := &ICECandidateData{Candidate: c.Candidate}
		if c.SDPMid != nil {
			data.SDPMid = *c.SDPMid
		}
		if c.SDPMLineIndex != nil {
			data.SDPMLineIndex = *c.SDPMLineIndex
		}
		if c.UsernameFragment != nil {
			data.UsernameFragment = *c.UsernameFragment
		}
		p.SendMessage(NewServerMessage(MessageTypeICECandidate, data))
	})

	// Incoming tracks from the client
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		p.logger.Info("Track received",
			zap.String("track_id", track.ID()),
			zap.String("kind", track.Kind().String()),
			zap.String("codec", track.Codec().MimeType))

		// Read RTCP feedback (PLI/NACK) in background
		go p.readRTCP(receiver, track)

		// Forward track to all other peers
		p.room.BroadcastTrack(p.ID, track)
	})

	// Negotiation needed — only when stable
	pc.OnNegotiationNeeded(func() {
		p.negotiationMu.Lock()
		if p.batchingTracks {
			p.negotiationPending = true
			p.negotiationMu.Unlock()
			return
		}
		p.negotiationMu.Unlock()

		if pc.SignalingState() == webrtc.SignalingStateStable {
			p.createAndSendOffer()
		} else {
			p.negotiationMu.Lock()
			p.negotiationPending = true
			p.negotiationMu.Unlock()
		}
	})

	// When state returns to stable, fire pending negotiation
	pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		if state == webrtc.SignalingStateStable {
			p.negotiationMu.Lock()
			pending := p.negotiationPending
			p.negotiationPending = false
			p.negotiationMu.Unlock()

			if pending {
				p.createAndSendOffer()
			}
		}
	})

	return nil
}

func (p *Peer) createAndSendOffer() {
	if p.pc == nil {
		return
	}
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.negotiationMu.Lock()
		p.negotiationPending = true
		p.negotiationMu.Unlock()
		return
	}

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.logger.Error("Failed to create offer", zap.Error(err))
		return
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.logger.Error("Failed to set local description", zap.Error(err))
		return
	}
	p.SendMessage(NewServerMessage(MessageTypeOffer, &OfferData{SDP: offer.SDP}))
}

// HandleOffer processes an SDP offer from the client
func (p *Peer) HandleOffer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: sdp,
	}); err != nil {
		return err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return err
	}
	p.SendMessage(NewServerMessage(MessageTypeAnswer, &AnswerData{SDP: answer.SDP}))
	return nil
}

// HandleAnswer processes an SDP answer from the client
func (p *Peer) HandleAnswer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	return p.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: sdp,
	})
}

// HandleICECandidate adds a remote ICE candidate
func (p *Peer) HandleICECandidate(data *ICECandidateData) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}
	return p.pc.AddICECandidate(data.ToWebRTCCandidate())
}

// AddTrack adds a local track to send to this peer
func (p *Peer) AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	if p.pc == nil {
		return nil, ErrPeerNotInitialized
	}
	return p.pc.AddTrack(track)
}

// StartTrackBatch suppresses renegotiation during bulk track additions
func (p *Peer) StartTrackBatch() {
	p.negotiationMu.Lock()
	p.batchingTracks = true
	p.negotiationMu.Unlock()
}

// EndTrackBatch ends batching and fires deferred renegotiation
func (p *Peer) EndTrackBatch() {
	p.negotiationMu.Lock()
	p.batchingTracks = false
	pending := p.negotiationPending
	p.negotiationPending = false
	p.negotiationMu.Unlock()

	if pending && p.pc != nil && p.pc.SignalingState() == webrtc.SignalingStateStable {
		p.createAndSendOffer()
	}
}

// SendMessage sends a signaling message via WebSocket
func (p *Peer) SendMessage(msg *ServerMessage) error {
	p.closedMu.RLock()
	if p.closed {
		p.closedMu.RUnlock()
		return ErrPeerClosed
	}
	p.closedMu.RUnlock()

	p.connMu.Lock()
	defer p.connMu.Unlock()
	if p.conn == nil {
		return ErrWebSocketClosed
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.conn.WriteMessage(websocket.TextMessage, data)
}

// GetInfo returns public info about this peer
func (p *Peer) GetInfo() ParticipantInfo {
	return ParticipantInfo{PeerID: p.ID, UserID: p.UserID}
}

// handleReconnectTimeout waits 15 seconds for ICE reconnection before removing the peer.
func (p *Peer) handleReconnectTimeout() {
	// Use a channel that closes when peer state changes
	// Check after 15 seconds if still disconnected
	<-timeAfter(reconnectTimeout)

	if p.pc == nil {
		return
	}
	state := p.pc.ICEConnectionState()
	if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
		p.logger.Info("Peer did not reconnect within timeout, removing")
		p.handleDisconnect()
	}
}

func (p *Peer) handleDisconnect() {
	p.closedMu.Lock()
	if p.closed {
		p.closedMu.Unlock()
		return
	}
	p.closed = true
	p.closedMu.Unlock()

	if p.onClose != nil {
		p.onClose(p)
	}
}

// Close closes the peer connection and WebSocket
func (p *Peer) Close() error {
	p.closedMu.Lock()
	if p.closed {
		p.closedMu.Unlock()
		return nil
	}
	p.closed = true
	p.closedMu.Unlock()

	p.connMu.Lock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
	p.connMu.Unlock()

	if p.pc != nil {
		return p.pc.Close()
	}
	return nil
}

// OnClose sets the disconnect callback
func (p *Peer) OnClose(fn func(*Peer)) {
	p.onClose = fn
}

// readRTCP reads RTCP feedback and forwards PLI/FIR to the source peer
func (p *Peer) readRTCP(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote) {
	localTrackID := track.Kind().String() + "-" + p.ID

	for {
		packets, _, err := receiver.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range packets {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				p.room.RequestKeyframe(localTrackID)
			}
		}
	}
}
