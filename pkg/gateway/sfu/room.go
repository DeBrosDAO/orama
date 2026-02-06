package sfu

import (
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Common errors
var (
	ErrRoomFull          = errors.New("room is full")
	ErrRoomClosed        = errors.New("room is closed")
	ErrPeerNotFound      = errors.New("peer not found")
	ErrPeerNotInitialized = errors.New("peer not initialized")
	ErrPeerClosed        = errors.New("peer is closed")
	ErrWebSocketClosed   = errors.New("websocket connection closed")
)

// Room represents a WebRTC room with multiple participants
type Room struct {
	ID        string
	Namespace string

	// Participants in the room
	peers   map[string]*Peer
	peersMu sync.RWMutex

	// WebRTC API for creating peer connections
	api *webrtc.API

	// Configuration
	config *Config
	logger *zap.Logger

	// State
	closed   bool
	closedMu sync.RWMutex

	// Callbacks
	onEmpty func(*Room)
}

// NewRoom creates a new room
func NewRoom(id, namespace string, api *webrtc.API, config *Config, logger *zap.Logger) *Room {
	return &Room{
		ID:        id,
		Namespace: namespace,
		peers:     make(map[string]*Peer),
		api:       api,
		config:    config,
		logger:    logger.With(zap.String("room_id", id)),
	}
}

// AddPeer adds a new peer to the room
func (r *Room) AddPeer(peer *Peer) error {
	r.closedMu.RLock()
	if r.closed {
		r.closedMu.RUnlock()
		return ErrRoomClosed
	}
	r.closedMu.RUnlock()

	r.peersMu.Lock()

	// Check max participants
	if r.config.MaxParticipants > 0 && len(r.peers) >= r.config.MaxParticipants {
		r.peersMu.Unlock()
		return ErrRoomFull
	}

	// Initialize peer connection
	pcConfig := webrtc.Configuration{
		ICEServers: r.config.ICEServers,
	}

	if err := peer.InitPeerConnection(r.api, pcConfig); err != nil {
		r.peersMu.Unlock()
		return err
	}

	// Set up peer close handler
	peer.OnClose(func(p *Peer) {
		r.RemovePeer(p.ID)
	})

	r.peers[peer.ID] = peer
	peerInfo := peer.GetInfo() // Get info while holding lock
	totalPeers := len(r.peers)

	// Release lock BEFORE broadcasting to avoid deadlock
	// (broadcastMessage also acquires the lock)
	r.peersMu.Unlock()

	r.logger.Info("Peer added to room",
		zap.String("peer_id", peer.ID),
		zap.String("user_id", peer.UserID),
		zap.Int("total_peers", totalPeers),
	)

	// Notify other peers (now safe since we released the lock)
	r.broadcastMessage(peer.ID, NewServerMessage(MessageTypeParticipantJoined, &ParticipantJoinedData{
		Participant: peerInfo,
	}))

	return nil
}

// RemovePeer removes a peer from the room
func (r *Room) RemovePeer(peerID string) error {
	r.peersMu.Lock()
	peer, ok := r.peers[peerID]
	if !ok {
		r.peersMu.Unlock()
		return ErrPeerNotFound
	}

	delete(r.peers, peerID)
	remainingPeers := len(r.peers)
	r.peersMu.Unlock()

	// Close the peer
	if err := peer.Close(); err != nil {
		r.logger.Warn("Error closing peer",
			zap.String("peer_id", peerID),
			zap.Error(err),
		)
	}

	r.logger.Info("Peer removed from room",
		zap.String("peer_id", peerID),
		zap.Int("remaining_peers", remainingPeers),
	)

	// Notify other peers
	r.broadcastMessage(peerID, NewServerMessage(MessageTypeParticipantLeft, &ParticipantLeftData{
		ParticipantID: peerID,
	}))

	// Check if room is empty
	if remainingPeers == 0 && r.onEmpty != nil {
		r.onEmpty(r)
	}

	return nil
}

// GetPeer returns a peer by ID
func (r *Room) GetPeer(peerID string) (*Peer, error) {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	peer, ok := r.peers[peerID]
	if !ok {
		return nil, ErrPeerNotFound
	}

	return peer, nil
}

// GetPeers returns all peers in the room
func (r *Room) GetPeers() []*Peer {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	return peers
}

// GetParticipants returns info about all participants
func (r *Room) GetParticipants() []ParticipantInfo {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	participants := make([]ParticipantInfo, 0, len(r.peers))
	for _, peer := range r.peers {
		participants = append(participants, peer.GetInfo())
	}
	return participants
}

// GetParticipantCount returns the number of participants
func (r *Room) GetParticipantCount() int {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	return len(r.peers)
}

// BroadcastTrack broadcasts a track from one peer to all other peers
func (r *Room) BroadcastTrack(sourcePeerID string, track *webrtc.TrackRemote) {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	// Create a local track from the remote track
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		track.Codec().RTPCodecCapability,
		track.ID(),
		track.StreamID(),
	)
	if err != nil {
		r.logger.Error("Failed to create local track",
			zap.String("source_peer", sourcePeerID),
			zap.String("track_id", track.ID()),
			zap.Error(err),
		)
		return
	}

	// Forward RTP packets from remote track to local track
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, readErr := track.Read(buf)
			if readErr != nil {
				r.logger.Debug("Track read ended",
					zap.String("track_id", track.ID()),
					zap.Error(readErr),
				)
				return
			}

			if _, writeErr := localTrack.Write(buf[:n]); writeErr != nil {
				r.logger.Debug("Track write failed",
					zap.String("track_id", track.ID()),
					zap.Error(writeErr),
				)
				return
			}
		}
	}()

	// Add track to all other peers
	for peerID, peer := range r.peers {
		if peerID == sourcePeerID {
			continue
		}

		if _, err := peer.AddTrack(localTrack); err != nil {
			r.logger.Warn("Failed to add track to peer",
				zap.String("target_peer", peerID),
				zap.String("track_id", track.ID()),
				zap.Error(err),
			)
			continue
		}

		// Notify peer about new track
		peer.SendMessage(NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{
			ParticipantID: sourcePeerID,
			TrackID:       track.ID(),
			Kind:          track.Kind().String(),
		}))
	}

	r.logger.Info("Track broadcast to room",
		zap.String("source_peer", sourcePeerID),
		zap.String("track_id", track.ID()),
		zap.String("kind", track.Kind().String()),
	)
}

// broadcastMessage sends a message to all peers except the specified one
func (r *Room) broadcastMessage(excludePeerID string, msg *ServerMessage) {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	for peerID, peer := range r.peers {
		if peerID == excludePeerID {
			continue
		}

		if err := peer.SendMessage(msg); err != nil {
			r.logger.Warn("Failed to send message to peer",
				zap.String("peer_id", peerID),
				zap.Error(err),
			)
		}
	}
}

// Close closes the room and all peer connections
func (r *Room) Close() error {
	r.closedMu.Lock()
	if r.closed {
		r.closedMu.Unlock()
		return nil
	}
	r.closed = true
	r.closedMu.Unlock()

	r.logger.Info("Closing room")

	r.peersMu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	r.peers = make(map[string]*Peer)
	r.peersMu.Unlock()

	// Close all peers
	for _, peer := range peers {
		if err := peer.Close(); err != nil {
			r.logger.Warn("Error closing peer",
				zap.String("peer_id", peer.ID),
				zap.Error(err),
			)
		}
	}

	return nil
}

// OnEmpty sets a callback for when the room becomes empty
func (r *Room) OnEmpty(fn func(*Room)) {
	r.onEmpty = fn
}

// IsClosed returns whether the room is closed
func (r *Room) IsClosed() bool {
	r.closedMu.RLock()
	defer r.closedMu.RUnlock()
	return r.closed
}
