package sfu

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Peer represents a participant in a room
type Peer struct {
	ID          string
	UserID      string
	DisplayName string

	// WebRTC connection
	pc *webrtc.PeerConnection

	// Tracks published by this peer (local tracks that others receive)
	localTracks   map[string]*webrtc.TrackLocalStaticRTP
	localTracksMu sync.RWMutex

	// Track receivers for consuming other peers' tracks
	trackReceivers   map[string]*webrtc.RTPReceiver
	trackReceiversMu sync.RWMutex

	// WebSocket connection for signaling
	conn   *websocket.Conn
	connMu sync.Mutex

	// State
	audioMuted           bool
	videoMuted           bool
	closed               bool
	closedMu             sync.RWMutex
	negotiationPending   bool
	negotiationPendingMu sync.Mutex
	initialOfferHandled  bool
	initialOfferMu       sync.Mutex

	// Room reference
	room   *Room
	logger *zap.Logger

	// Callbacks
	onClose func(*Peer)
}

// NewPeer creates a new peer
func NewPeer(userID, displayName string, conn *websocket.Conn, room *Room, logger *zap.Logger) *Peer {
	return &Peer{
		ID:             uuid.New().String(),
		UserID:         userID,
		DisplayName:    displayName,
		localTracks:    make(map[string]*webrtc.TrackLocalStaticRTP),
		trackReceivers: make(map[string]*webrtc.RTPReceiver),
		conn:           conn,
		room:           room,
		logger:         logger,
	}
}

// InitPeerConnection initializes the WebRTC peer connection
func (p *Peer) InitPeerConnection(api *webrtc.API, config webrtc.Configuration) error {
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	p.pc = pc

	// Handle ICE connection state changes
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		p.logger.Info("ICE connection state changed",
			zap.String("peer_id", p.ID),
			zap.String("state", state.String()),
		)

		if state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateClosed {
			p.handleDisconnect()
		}
	})

	// Handle ICE candidates
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		p.logger.Debug("ICE candidate generated",
			zap.String("peer_id", p.ID),
			zap.String("candidate", candidate.String()),
		)

		candidateJSON := candidate.ToJSON()
		data := &ICECandidateData{
			Candidate: candidateJSON.Candidate,
		}
		if candidateJSON.SDPMid != nil {
			data.SDPMid = *candidateJSON.SDPMid
		}
		if candidateJSON.SDPMLineIndex != nil {
			data.SDPMLineIndex = *candidateJSON.SDPMLineIndex
		}
		if candidateJSON.UsernameFragment != nil {
			data.UsernameFragment = *candidateJSON.UsernameFragment
		}

		p.SendMessage(NewServerMessage(MessageTypeICECandidate, data))
	})

	// Handle incoming tracks from remote peers
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		p.logger.Info("Track received from client",
			zap.String("peer_id", p.ID),
			zap.String("user_id", p.UserID),
			zap.String("track_id", track.ID()),
			zap.String("stream_id", track.StreamID()),
			zap.String("kind", track.Kind().String()),
			zap.String("codec_mime", codec.MimeType),
			zap.Uint32("codec_clock_rate", codec.ClockRate),
			zap.Uint8("codec_payload_type", uint8(codec.PayloadType)),
		)

		p.trackReceiversMu.Lock()
		p.trackReceivers[track.ID()] = receiver
		p.trackReceiversMu.Unlock()

		// Forward track to other peers in the room
		p.room.BroadcastTrack(p.ID, track)
	})

	// Handle negotiation needed - only trigger when in stable state
	pc.OnNegotiationNeeded(func() {
		p.logger.Debug("Negotiation needed",
			zap.String("peer_id", p.ID),
			zap.String("signaling_state", pc.SignalingState().String()),
		)

		// Only create offer if we're in stable state
		// Otherwise, mark negotiation as pending
		if pc.SignalingState() == webrtc.SignalingStateStable {
			p.createAndSendOffer()
		} else {
			p.negotiationPendingMu.Lock()
			p.negotiationPending = true
			p.negotiationPendingMu.Unlock()
			p.logger.Debug("Negotiation queued - not in stable state",
				zap.String("peer_id", p.ID),
				zap.String("signaling_state", pc.SignalingState().String()),
			)
		}
	})

	// Handle signaling state changes to process pending negotiations
	pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		p.logger.Debug("Signaling state changed",
			zap.String("peer_id", p.ID),
			zap.String("state", state.String()),
		)

		// When we return to stable state, check if negotiation was pending
		if state == webrtc.SignalingStateStable {
			p.negotiationPendingMu.Lock()
			pending := p.negotiationPending
			p.negotiationPending = false
			p.negotiationPendingMu.Unlock()

			if pending {
				p.logger.Debug("Processing pending negotiation", zap.String("peer_id", p.ID))
				p.createAndSendOffer()
			}
		}
	})

	return nil
}

// createAndSendOffer creates an SDP offer and sends it to the peer
func (p *Peer) createAndSendOffer() {
	if p.pc == nil {
		return
	}

	// Double-check signaling state before creating offer
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.logger.Debug("Skipping offer - not in stable state",
			zap.String("peer_id", p.ID),
			zap.String("signaling_state", p.pc.SignalingState().String()),
		)
		// Mark as pending so it will be retried when state becomes stable
		p.negotiationPendingMu.Lock()
		p.negotiationPending = true
		p.negotiationPendingMu.Unlock()
		return
	}

	p.logger.Info("Creating SDP offer", zap.String("peer_id", p.ID))

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.logger.Error("Failed to create offer",
			zap.String("peer_id", p.ID),
			zap.Error(err),
		)
		return
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.logger.Error("Failed to set local description",
			zap.String("peer_id", p.ID),
			zap.Error(err),
		)
		return
	}

	p.logger.Info("Sending SDP offer", zap.String("peer_id", p.ID))
	p.SendMessage(NewServerMessage(MessageTypeOffer, &OfferData{
		SDP: offer.SDP,
	}))
}

// HandleOffer processes an SDP offer from the client
func (p *Peer) HandleOffer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}

	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return err
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return err
	}

	p.SendMessage(NewServerMessage(MessageTypeAnswer, &AnswerData{
		SDP: answer.SDP,
	}))

	return nil
}

// HandleAnswer processes an SDP answer from the client
func (p *Peer) HandleAnswer(sdp string) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}

	return p.pc.SetRemoteDescription(answer)
}

// HandleICECandidate processes an ICE candidate from the client
func (p *Peer) HandleICECandidate(data *ICECandidateData) error {
	if p.pc == nil {
		return ErrPeerNotInitialized
	}

	return p.pc.AddICECandidate(data.ToWebRTCCandidate())
}

// AddTrack adds a track to send to this peer (from another peer)
func (p *Peer) AddTrack(track *webrtc.TrackLocalStaticRTP) (*webrtc.RTPSender, error) {
	if p.pc == nil {
		return nil, ErrPeerNotInitialized
	}

	return p.pc.AddTrack(track)
}

// SendMessage sends a signaling message to the peer via WebSocket
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

// GetInfo returns public information about this peer
func (p *Peer) GetInfo() ParticipantInfo {
	p.localTracksMu.RLock()
	hasAudio := false
	hasVideo := false
	for _, track := range p.localTracks {
		if track.Kind() == webrtc.RTPCodecTypeAudio {
			hasAudio = true
		} else if track.Kind() == webrtc.RTPCodecTypeVideo {
			hasVideo = true
		}
	}
	p.localTracksMu.RUnlock()

	return ParticipantInfo{
		ID:          p.ID,
		UserID:      p.UserID,
		DisplayName: p.DisplayName,
		HasAudio:    hasAudio,
		HasVideo:    hasVideo,
		AudioMuted:  p.audioMuted,
		VideoMuted:  p.videoMuted,
	}
}

// SetAudioMuted sets the audio mute state
func (p *Peer) SetAudioMuted(muted bool) {
	p.audioMuted = muted
}

// SetVideoMuted sets the video mute state
func (p *Peer) SetVideoMuted(muted bool) {
	p.videoMuted = muted
}

// MarkInitialOfferHandled marks that the initial offer has been processed.
// Returns true if this is the first time it's being marked (i.e., this was the first offer).
func (p *Peer) MarkInitialOfferHandled() bool {
	p.initialOfferMu.Lock()
	defer p.initialOfferMu.Unlock()

	if p.initialOfferHandled {
		return false
	}
	p.initialOfferHandled = true
	return true
}

// handleDisconnect handles peer disconnection
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

// Close closes the peer connection and cleans up resources
func (p *Peer) Close() error {
	p.closedMu.Lock()
	if p.closed {
		p.closedMu.Unlock()
		return nil
	}
	p.closed = true
	p.closedMu.Unlock()

	p.logger.Info("Closing peer", zap.String("peer_id", p.ID))

	// Close WebSocket
	p.connMu.Lock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
	p.connMu.Unlock()

	// Close peer connection
	if p.pc != nil {
		return p.pc.Close()
	}

	return nil
}

// OnClose sets a callback for when the peer is closed
func (p *Peer) OnClose(fn func(*Peer)) {
	p.onClose = fn
}
