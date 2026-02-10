package sfu

import (
	"errors"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// Common errors
var (
	ErrRoomFull           = errors.New("room is full")
	ErrRoomClosed         = errors.New("room is closed")
	ErrPeerNotFound       = errors.New("peer not found")
	ErrPeerNotInitialized = errors.New("peer not initialized")
	ErrPeerClosed         = errors.New("peer is closed")
	ErrWebSocketClosed    = errors.New("websocket connection closed")
)

// publishedTrack holds information about a track published to the room
type publishedTrack struct {
	sourcePeerID    string
	sourceUserID    string
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteTrackSSRC uint32                  // SSRC of the remote track (for PLI requests)
	remoteTrack     *webrtc.TrackRemote     // Reference to the remote track
	kind            string
	forwarderActive bool // tracks if the RTP forwarder goroutine is still running
	packetCount     int  // number of packets forwarded (for debugging)
}

// Room represents a WebRTC room with multiple participants
type Room struct {
	ID        string
	Namespace string

	// Participants in the room
	peers   map[string]*Peer
	peersMu sync.RWMutex

	// Published tracks in the room (for sending to new joiners)
	publishedTracks   map[string]*publishedTrack // key: trackID
	publishedTracksMu sync.RWMutex

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
		ID:              id,
		Namespace:       namespace,
		peers:           make(map[string]*Peer),
		publishedTracks: make(map[string]*publishedTrack),
		api:             api,
		config:          config,
		logger:          logger.With(zap.String("room_id", id)),
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

	// Remove tracks published by this peer
	r.publishedTracksMu.Lock()
	removedTracks := make([]string, 0)
	for trackID, track := range r.publishedTracks {
		if track.sourcePeerID == peerID {
			delete(r.publishedTracks, trackID)
			removedTracks = append(removedTracks, trackID)
		}
	}
	r.publishedTracksMu.Unlock()

	if len(removedTracks) > 0 {
		r.logger.Info("Removed tracks for departing peer",
			zap.String("peer_id", peerID),
			zap.Strings("track_ids", removedTracks),
		)
	}

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

// SendExistingTracksTo sends all existing tracks from other participants to the specified peer.
// This should be called AFTER the welcome message is sent to ensure the client is ready.
func (r *Room) SendExistingTracksTo(peer *Peer) {
	r.publishedTracksMu.RLock()
	existingTracks := make([]*publishedTrack, 0, len(r.publishedTracks))
	for _, track := range r.publishedTracks {
		// Don't send peer's own tracks back to them
		if track.sourcePeerID != peer.ID {
			existingTracks = append(existingTracks, track)
		}
	}
	r.publishedTracksMu.RUnlock()

	if len(existingTracks) == 0 {
		r.logger.Info("No existing tracks to send to new peer",
			zap.String("peer_id", peer.ID),
		)
		return
	}

	r.logger.Info("Sending existing tracks to new peer",
		zap.String("peer_id", peer.ID),
		zap.Int("track_count", len(existingTracks)),
	)

	videoTrackIDs := make([]string, 0)

	for _, track := range existingTracks {
		// Log forwarder status to help diagnose video issues
		r.logger.Info("Adding existing track to new peer",
			zap.String("new_peer_id", peer.ID),
			zap.String("source_peer_id", track.sourcePeerID),
			zap.String("source_user_id", track.sourceUserID),
			zap.String("track_id", track.localTrack.ID()),
			zap.String("kind", track.kind),
			zap.Bool("forwarder_active", track.forwarderActive),
			zap.Int("packets_forwarded", track.packetCount),
		)

		// Warn if forwarder is no longer active
		if !track.forwarderActive {
			r.logger.Warn("WARNING: Track forwarder is NOT active - track may not receive data",
				zap.String("track_id", track.localTrack.ID()),
				zap.String("kind", track.kind),
				zap.Int("final_packet_count", track.packetCount),
			)
		}

		if _, err := peer.AddTrack(track.localTrack); err != nil {
			r.logger.Warn("Failed to add existing track to new peer",
				zap.String("peer_id", peer.ID),
				zap.String("track_id", track.localTrack.ID()),
				zap.Error(err),
			)
			continue
		}

		r.logger.Info("Track added to peer connection, renegotiation will be triggered",
			zap.String("peer_id", peer.ID),
			zap.String("track_id", track.localTrack.ID()),
			zap.String("kind", track.kind),
		)

		// Track video tracks for keyframe requests
		if track.kind == "video" {
			videoTrackIDs = append(videoTrackIDs, track.localTrack.ID())
		}

		// Notify new peer about the existing track
		peer.SendMessage(NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{
			ParticipantID: track.sourcePeerID,
			UserID:        track.sourceUserID,
			TrackID:       track.localTrack.ID(),
			StreamID:      track.localTrack.StreamID(),
			Kind:          track.kind,
		}))
	}

	// Request keyframes for video tracks after a short delay
	// This ensures the receiver has time to set up the track before receiving the keyframe
	if len(videoTrackIDs) > 0 {
		go func() {
			// Wait for negotiation to progress
			time.Sleep(500 * time.Millisecond)
			r.logger.Info("Requesting keyframes for new peer",
				zap.String("peer_id", peer.ID),
				zap.Int("video_track_count", len(videoTrackIDs)),
			)
			for _, trackID := range videoTrackIDs {
				r.RequestKeyframe(trackID)
			}
			// Request again after 1 second in case the first was too early
			time.Sleep(1 * time.Second)
			for _, trackID := range videoTrackIDs {
				r.RequestKeyframe(trackID)
			}
		}()
	}
}

// BroadcastTrack broadcasts a track from one peer to all other peers
func (r *Room) BroadcastTrack(sourcePeerID string, track *webrtc.TrackRemote) {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	// Get source peer's user ID for the track-added message
	sourceUserID := ""
	if sourcePeer, ok := r.peers[sourcePeerID]; ok {
		sourceUserID = sourcePeer.UserID
	}

	// Create a local track from the remote track
	// Use participant ID as the stream ID so clients can identify the source
	// Format: trackId stays the same, streamId = sourcePeerID (or sourceUserID if available)
	streamID := sourcePeerID
	if sourceUserID != "" {
		streamID = sourceUserID // Use userID for easier client-side mapping
	}

	r.logger.Info("Creating local track for broadcast",
		zap.String("source_peer_id", sourcePeerID),
		zap.String("source_user_id", sourceUserID),
		zap.String("original_track_id", track.ID()),
		zap.String("original_stream_id", track.StreamID()),
		zap.String("new_stream_id", streamID),
	)

	// Log codec information for debugging
	codec := track.Codec()
	r.logger.Info("Track codec info",
		zap.String("source_peer_id", sourcePeerID),
		zap.String("track_kind", track.Kind().String()),
		zap.String("mime_type", codec.MimeType),
		zap.Uint32("clock_rate", codec.ClockRate),
		zap.Uint16("channels", codec.Channels),
		zap.String("sdp_fmtp_line", codec.SDPFmtpLine),
	)

	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		codec.RTPCodecCapability,
		track.Kind().String()+"-"+sourcePeerID, // Include peer ID in track ID
		streamID,                               // Use participant/user ID as stream ID
	)
	if err != nil {
		r.logger.Error("Failed to create local track",
			zap.String("source_peer", sourcePeerID),
			zap.String("track_id", track.ID()),
			zap.Error(err),
		)
		return
	}

	// Store the track for new joiners
	pubTrack := &publishedTrack{
		sourcePeerID:    sourcePeerID,
		sourceUserID:    sourceUserID,
		localTrack:      localTrack,
		remoteTrackSSRC: uint32(track.SSRC()),
		remoteTrack:     track,
		kind:            track.Kind().String(),
		forwarderActive: true,
		packetCount:     0,
	}
	r.publishedTracksMu.Lock()
	r.publishedTracks[localTrack.ID()] = pubTrack
	r.publishedTracksMu.Unlock()

	r.logger.Info("Track stored for new joiners",
		zap.String("track_id", localTrack.ID()),
		zap.String("source_peer_id", sourcePeerID),
		zap.Int("total_published_tracks", len(r.publishedTracks)),
	)

	// Forward RTP packets from remote track to local track
	go func() {
		trackID := track.ID()
		localTrackID := localTrack.ID()
		trackKind := track.Kind().String()
		buf := make([]byte, 1600) // Slightly larger than MTU to handle RTP extensions
		packetCount := 0
		byteCount := 0
		startTime := time.Now()
		firstPacketReceived := false

		r.logger.Info("RTP forwarder started",
			zap.String("track_id", trackID),
			zap.String("local_track_id", localTrackID),
			zap.String("kind", trackKind),
			zap.String("source_peer_id", sourcePeerID),
		)

		// Start a goroutine to log warning if no packets received after 5 seconds
		go func() {
			time.Sleep(5 * time.Second)
			if !firstPacketReceived {
				r.logger.Warn("RTP forwarder WARNING: No packets received after 5 seconds - host may not be sending",
					zap.String("track_id", trackID),
					zap.String("local_track_id", localTrackID),
					zap.String("kind", trackKind),
					zap.String("source_peer_id", sourcePeerID),
				)
			}
		}()

		// For video tracks, periodically request keyframes to ensure receivers can decode
		if trackKind == "video" {
			go func() {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for range ticker.C {
					// Check if room is closed
					if r.IsClosed() {
						return
					}

					// Check if forwarder is still active
					r.publishedTracksMu.RLock()
					pt, ok := r.publishedTracks[localTrackID]
					active := ok && pt.forwarderActive
					r.publishedTracksMu.RUnlock()

					if !active {
						return
					}

					r.RequestKeyframe(localTrackID)
				}
			}()
		}

		// Mark forwarder as stopped when we exit
		defer func() {
			r.publishedTracksMu.Lock()
			if pt, ok := r.publishedTracks[localTrackID]; ok {
				pt.forwarderActive = false
				pt.packetCount = packetCount
			}
			r.publishedTracksMu.Unlock()

			r.logger.Info("RTP forwarder exiting",
				zap.String("track_id", trackID),
				zap.String("local_track_id", localTrackID),
				zap.String("kind", trackKind),
				zap.Duration("lifetime", time.Since(startTime)),
				zap.Int("total_packets", packetCount),
				zap.Int("total_bytes", byteCount),
			)
		}()

		for {
			n, _, readErr := track.Read(buf)
			if readErr != nil {
				r.logger.Info("RTP forwarder stopped - read error",
					zap.String("track_id", trackID),
					zap.String("local_track_id", localTrackID),
					zap.String("kind", trackKind),
					zap.Int("packets_forwarded", packetCount),
					zap.Int("bytes_forwarded", byteCount),
					zap.Error(readErr),
				)
				return
			}

			// Log first packet to confirm data is flowing
			if packetCount == 0 {
				firstPacketReceived = true
				r.logger.Info("RTP forwarder received FIRST packet",
					zap.String("track_id", trackID),
					zap.String("local_track_id", localTrackID),
					zap.String("kind", trackKind),
					zap.Int("packet_size", n),
					zap.Duration("time_to_first_packet", time.Since(startTime)),
				)
			}

			if _, writeErr := localTrack.Write(buf[:n]); writeErr != nil {
				r.logger.Info("RTP forwarder stopped - write error",
					zap.String("track_id", trackID),
					zap.String("local_track_id", localTrackID),
					zap.String("kind", trackKind),
					zap.Int("packets_forwarded", packetCount),
					zap.Int("bytes_forwarded", byteCount),
					zap.Error(writeErr),
				)
				return
			}

			packetCount++
			byteCount += n

			// Update packet count periodically (not every packet to reduce lock contention)
			if packetCount%50 == 0 {
				r.publishedTracksMu.Lock()
				if pt, ok := r.publishedTracks[localTrackID]; ok {
					pt.packetCount = packetCount
				}
				r.publishedTracksMu.Unlock()
			}

			// Log progress every 100 packets for video, 500 for audio
			logInterval := 500
			if trackKind == "video" {
				logInterval = 100
			}
			if packetCount%logInterval == 0 {
				r.logger.Info("RTP forwarder progress",
					zap.String("track_id", trackID),
					zap.String("kind", trackKind),
					zap.Int("packets", packetCount),
					zap.Int("bytes", byteCount),
				)
			}
		}
	}()

	// Add track to all other peers
	for peerID, peer := range r.peers {
		if peerID == sourcePeerID {
			continue
		}

		r.logger.Info("Adding track to peer",
			zap.String("target_peer", peerID),
			zap.String("source_peer", sourcePeerID),
			zap.String("source_user", sourceUserID),
			zap.String("track_id", localTrack.ID()),
			zap.String("stream_id", localTrack.StreamID()),
		)

		if _, err := peer.AddTrack(localTrack); err != nil {
			r.logger.Warn("Failed to add track to peer",
				zap.String("target_peer", peerID),
				zap.String("track_id", localTrack.ID()),
				zap.Error(err),
			)
			continue
		}

		// Notify peer about new track
		// Use consistent IDs: participantId=sourcePeerID, streamId matches the track
		peer.SendMessage(NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{
			ParticipantID: sourcePeerID,
			UserID:        sourceUserID,
			TrackID:       localTrack.ID(),       // Format: "{kind}-{participantId}"
			StreamID:      localTrack.StreamID(), // Same as userId for easy matching
			Kind:          track.Kind().String(),
		}))
	}

	r.logger.Info("Track broadcast to room",
		zap.String("source_peer", sourcePeerID),
		zap.String("source_user", sourceUserID),
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

// RequestKeyframe sends a PLI (Picture Loss Indication) to the source peer for a video track.
// This causes the source to send a keyframe, which is needed for new receivers to start decoding.
func (r *Room) RequestKeyframe(trackID string) {
	r.publishedTracksMu.RLock()
	track, ok := r.publishedTracks[trackID]
	r.publishedTracksMu.RUnlock()

	if !ok || track.kind != "video" {
		return
	}

	r.peersMu.RLock()
	sourcePeer, ok := r.peers[track.sourcePeerID]
	r.peersMu.RUnlock()

	if !ok || sourcePeer.pc == nil {
		r.logger.Debug("Cannot request keyframe - source peer not found",
			zap.String("track_id", trackID),
			zap.String("source_peer_id", track.sourcePeerID),
		)
		return
	}

	// Create a PLI packet
	pli := &rtcp.PictureLossIndication{
		MediaSSRC: track.remoteTrackSSRC,
	}

	// Send the PLI to the source peer
	if err := sourcePeer.pc.WriteRTCP([]rtcp.Packet{pli}); err != nil {
		r.logger.Debug("Failed to send PLI",
			zap.String("track_id", trackID),
			zap.String("source_peer_id", track.sourcePeerID),
			zap.Error(err),
		)
		return
	}

	r.logger.Debug("PLI keyframe request sent",
		zap.String("track_id", trackID),
		zap.String("source_peer_id", track.sourcePeerID),
		zap.Uint32("ssrc", track.remoteTrackSSRC),
	)
}

// RequestKeyframeForAllVideoTracks sends PLI requests for all video tracks in the room.
// This is useful when a new peer joins to ensure they get keyframes quickly.
func (r *Room) RequestKeyframeForAllVideoTracks() {
	r.publishedTracksMu.RLock()
	videoTrackIDs := make([]string, 0)
	for trackID, track := range r.publishedTracks {
		if track.kind == "video" {
			videoTrackIDs = append(videoTrackIDs, trackID)
		}
	}
	r.publishedTracksMu.RUnlock()

	for _, trackID := range videoTrackIDs {
		r.RequestKeyframe(trackID)
	}
}
