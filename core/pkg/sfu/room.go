package sfu

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// For testing: allows overriding time.After
var timeAfter = func(d time.Duration) <-chan time.Time { return time.After(d) }

const (
	reconnectTimeout  = 15 * time.Second
	emptyRoomTTL      = 60 * time.Second
	rtpBufferSize     = 8192
)

var (
	ErrRoomFull    = errors.New("room is full")
	ErrRoomClosed  = errors.New("room is closed")
	ErrPeerNotFound = errors.New("peer not found")
)

// publishedTrack holds a local track being forwarded from a remote source.
type publishedTrack struct {
	sourcePeerID    string
	sourceUserID    string
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteTrackSSRC uint32
	kind            string
}

// Room is a WebRTC room with multiple participants sharing media tracks.
type Room struct {
	ID        string
	Namespace string

	peers   map[string]*Peer
	peersMu sync.RWMutex

	publishedTracks   map[string]*publishedTrack // key: localTrack.ID()
	publishedTracksMu sync.RWMutex

	api    *webrtc.API
	config *Config
	logger *zap.Logger

	closed   bool
	closedMu sync.RWMutex

	onEmpty func(*Room)
}

// RoomManager manages the lifecycle of rooms.
type RoomManager struct {
	rooms  map[string]*Room // key: roomID
	mu     sync.RWMutex
	config *Config
	logger *zap.Logger
}

// NewRoomManager creates a new room manager.
func NewRoomManager(cfg *Config, logger *zap.Logger) *RoomManager {
	return &RoomManager{
		rooms:  make(map[string]*Room),
		config: cfg,
		logger: logger.With(zap.String("component", "room-manager")),
	}
}

// GetOrCreateRoom returns an existing room or creates a new one.
func (rm *RoomManager) GetOrCreateRoom(roomID string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, ok := rm.rooms[roomID]; ok && !room.IsClosed() {
		return room
	}

	api := newWebRTCAPI(rm.config)
	room := &Room{
		ID:              roomID,
		Namespace:       rm.config.Namespace,
		peers:           make(map[string]*Peer),
		publishedTracks: make(map[string]*publishedTrack),
		api:             api,
		config:          rm.config,
		logger:          rm.logger.With(zap.String("room_id", roomID)),
	}

	room.onEmpty = func(r *Room) {
		// Start empty room cleanup timer
		go func() {
			<-timeAfter(emptyRoomTTL)
			if r.GetParticipantCount() == 0 {
				rm.mu.Lock()
				delete(rm.rooms, r.ID)
				rm.mu.Unlock()
				r.Close()
				rm.logger.Info("Empty room cleaned up", zap.String("room_id", r.ID))
			}
		}()
	}

	rm.rooms[roomID] = room
	rm.logger.Info("Room created", zap.String("room_id", roomID))
	return room
}

// GetRoom returns a room by ID, or nil if not found.
func (rm *RoomManager) GetRoom(roomID string) *Room {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.rooms[roomID]
}

// CloseAll closes all rooms (for graceful shutdown).
func (rm *RoomManager) CloseAll() {
	rm.mu.Lock()
	rooms := make([]*Room, 0, len(rm.rooms))
	for _, r := range rm.rooms {
		rooms = append(rooms, r)
	}
	rm.rooms = make(map[string]*Room)
	rm.mu.Unlock()

	for _, r := range rooms {
		r.Close()
	}
}

// RoomCount returns the number of active rooms.
func (rm *RoomManager) RoomCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.rooms)
}

// newWebRTCAPI creates a Pion WebRTC API with codecs and interceptors.
func newWebRTCAPI(cfg *Config) *webrtc.API {
	m := &webrtc.MediaEngine{}

	// Audio: Opus
	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack", Parameter: ""},
		{Type: "nack", Parameter: "pli"},
	}

	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio)

	// Video: VP8
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP8,
			ClockRate:    90000,
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo)

	// Video: H264
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 125,
	}, webrtc.RTPCodecTypeVideo)

	// Interceptors: NACK + PLI
	i := &interceptor.Registry{}
	if f, err := nack.NewResponderInterceptor(); err == nil {
		i.Add(f)
	}
	if f, err := nack.NewGeneratorInterceptor(); err == nil {
		i.Add(f)
	}
	if f, err := intervalpli.NewReceiverInterceptor(); err == nil {
		i.Add(f)
	}

	// SettingEngine: restrict media ports
	se := webrtc.SettingEngine{}
	if cfg.MediaPortStart > 0 && cfg.MediaPortEnd > 0 {
		se.SetEphemeralUDPPortRange(uint16(cfg.MediaPortStart), uint16(cfg.MediaPortEnd))
	}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(i),
		webrtc.WithSettingEngine(se),
	)
}

// --- Room methods ---

// AddPeer adds a peer to the room and notifies other participants.
func (r *Room) AddPeer(peer *Peer) error {
	r.closedMu.RLock()
	if r.closed {
		r.closedMu.RUnlock()
		return ErrRoomClosed
	}
	r.closedMu.RUnlock()

	// Build ICE servers for TURN
	iceServers := r.buildICEServers()

	r.peersMu.Lock()
	if len(r.peers) >= 100 { // Hard cap
		r.peersMu.Unlock()
		return ErrRoomFull
	}

	if err := peer.InitPeerConnection(r.api, iceServers); err != nil {
		r.peersMu.Unlock()
		return err
	}

	peer.OnClose(func(p *Peer) { r.RemovePeer(p.ID) })

	r.peers[peer.ID] = peer
	info := peer.GetInfo()
	total := len(r.peers)
	r.peersMu.Unlock()

	r.logger.Info("Peer joined", zap.String("peer_id", peer.ID), zap.Int("total", total))

	// Notify others
	r.broadcastMessage(peer.ID, NewServerMessage(MessageTypeParticipantJoined, &ParticipantJoinedData{
		Participant: info,
	}))

	return nil
}

// RemovePeer removes a peer and cleans up their published tracks.
func (r *Room) RemovePeer(peerID string) {
	r.peersMu.Lock()
	peer, ok := r.peers[peerID]
	if !ok {
		r.peersMu.Unlock()
		return
	}
	delete(r.peers, peerID)
	remaining := len(r.peers)
	r.peersMu.Unlock()

	// Remove published tracks from this peer
	r.publishedTracksMu.Lock()
	var removed []string
	for trackID, pt := range r.publishedTracks {
		if pt.sourcePeerID == peerID {
			delete(r.publishedTracks, trackID)
			removed = append(removed, trackID)
		}
	}
	r.publishedTracksMu.Unlock()

	// Remove RTPSenders for this peer's tracks from all other peers
	if len(removed) > 0 {
		r.removeTrackSendersFromPeers(removed)
	}

	peer.Close()

	r.logger.Info("Peer left", zap.String("peer_id", peerID), zap.Int("remaining", remaining))

	r.broadcastMessage(peerID, NewServerMessage(MessageTypeParticipantLeft, &ParticipantLeftData{
		PeerID: peerID,
	}))

	// Notify about removed tracks
	for _, trackID := range removed {
		r.broadcastMessage(peerID, NewServerMessage(MessageTypeTrackRemoved, &TrackRemovedData{
			PeerID:  peerID,
			UserID:  peer.UserID,
			TrackID: trackID,
		}))
	}

	if remaining == 0 && r.onEmpty != nil {
		r.onEmpty(r)
	}
}

// removeTrackSendersFromPeers removes RTPSenders for the given track IDs from all peers.
// This fixes the ghost track bug from the original implementation.
func (r *Room) removeTrackSendersFromPeers(trackIDs []string) {
	trackIDSet := make(map[string]bool, len(trackIDs))
	for _, id := range trackIDs {
		trackIDSet[id] = true
	}

	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	for _, peer := range r.peers {
		if peer.pc == nil {
			continue
		}
		for _, sender := range peer.pc.GetSenders() {
			if sender.Track() == nil {
				continue
			}
			if trackIDSet[sender.Track().ID()] {
				if err := peer.pc.RemoveTrack(sender); err != nil {
					r.logger.Warn("Failed to remove track sender",
						zap.String("peer_id", peer.ID),
						zap.String("track_id", sender.Track().ID()),
						zap.Error(err))
				}
			}
		}
	}
}

// BroadcastTrack creates a local track from a remote track and forwards it to all other peers.
func (r *Room) BroadcastTrack(sourcePeerID string, track *webrtc.TrackRemote) {
	codec := track.Codec()

	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		codec.RTPCodecCapability,
		track.Kind().String()+"-"+sourcePeerID,
		sourcePeerID,
	)
	if err != nil {
		r.logger.Error("Failed to create local track", zap.Error(err))
		return
	}

	// Look up source peer's UserID
	r.peersMu.RLock()
	var sourceUserID string
	if sourcePeer, ok := r.peers[sourcePeerID]; ok {
		sourceUserID = sourcePeer.UserID
	}
	r.peersMu.RUnlock()

	// Store for future joiners
	r.publishedTracksMu.Lock()
	r.publishedTracks[localTrack.ID()] = &publishedTrack{
		sourcePeerID:    sourcePeerID,
		sourceUserID:    sourceUserID,
		localTrack:      localTrack,
		remoteTrackSSRC: uint32(track.SSRC()),
		kind:            track.Kind().String(),
	}
	r.publishedTracksMu.Unlock()

	// RTP forwarding loop with proper buffer size
	go func() {
		buf := make([]byte, rtpBufferSize)
		for {
			n, _, err := track.Read(buf)
			if err != nil {
				return
			}
			if _, err := localTrack.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	// Add to all current peers except the source
	r.peersMu.RLock()
	for peerID, peer := range r.peers {
		if peerID == sourcePeerID {
			continue
		}
		if _, err := peer.AddTrack(localTrack); err != nil {
			r.logger.Warn("Failed to add track to peer",
				zap.String("peer_id", peerID), zap.Error(err))
			continue
		}
		peer.SendMessage(NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{
			PeerID:   sourcePeerID,
			UserID:   sourceUserID,
			TrackID:  localTrack.ID(),
			StreamID: localTrack.StreamID(),
			Kind:     track.Kind().String(),
		}))
	}
	r.peersMu.RUnlock()
}

// SendExistingTracksTo sends all published tracks to a newly joined peer.
// Uses batch mode for a single renegotiation.
func (r *Room) SendExistingTracksTo(peer *Peer) {
	r.publishedTracksMu.RLock()
	var tracks []*publishedTrack
	for _, pt := range r.publishedTracks {
		if pt.sourcePeerID != peer.ID {
			tracks = append(tracks, pt)
		}
	}
	r.publishedTracksMu.RUnlock()

	if len(tracks) == 0 {
		return
	}

	peer.StartTrackBatch()
	for _, pt := range tracks {
		if _, err := peer.AddTrack(pt.localTrack); err != nil {
			r.logger.Warn("Failed to add existing track", zap.Error(err))
			continue
		}
		peer.SendMessage(NewServerMessage(MessageTypeTrackAdded, &TrackAddedData{
			PeerID:   pt.sourcePeerID,
			UserID:   pt.sourceUserID,
			TrackID:  pt.localTrack.ID(),
			StreamID: pt.localTrack.StreamID(),
			Kind:     pt.kind,
		}))
	}
	peer.EndTrackBatch()

	// Request keyframes for video tracks after negotiation settles
	go func() {
		<-timeAfter(300 * time.Millisecond)
		r.RequestKeyframeForAllVideoTracks()
	}()
}

// RequestKeyframe sends a PLI to the source peer for a video track.
func (r *Room) RequestKeyframe(trackID string) {
	r.publishedTracksMu.RLock()
	pt, ok := r.publishedTracks[trackID]
	r.publishedTracksMu.RUnlock()
	if !ok || pt.kind != "video" {
		return
	}

	r.peersMu.RLock()
	source, ok := r.peers[pt.sourcePeerID]
	r.peersMu.RUnlock()
	if !ok || source.pc == nil {
		return
	}

	pli := &rtcp.PictureLossIndication{MediaSSRC: pt.remoteTrackSSRC}
	if err := source.pc.WriteRTCP([]rtcp.Packet{pli}); err != nil {
		r.logger.Debug("Failed to send PLI", zap.String("track_id", trackID), zap.Error(err))
	}
}

// RequestKeyframeForAllVideoTracks sends PLIs for all video tracks.
func (r *Room) RequestKeyframeForAllVideoTracks() {
	r.publishedTracksMu.RLock()
	var ids []string
	for id, pt := range r.publishedTracks {
		if pt.kind == "video" {
			ids = append(ids, id)
		}
	}
	r.publishedTracksMu.RUnlock()

	for _, id := range ids {
		r.RequestKeyframe(id)
	}
}

// GetParticipants returns info about all participants.
func (r *Room) GetParticipants() []ParticipantInfo {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	infos := make([]ParticipantInfo, 0, len(r.peers))
	for _, p := range r.peers {
		infos = append(infos, p.GetInfo())
	}
	return infos
}

// GetParticipantCount returns the number of participants.
func (r *Room) GetParticipantCount() int {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	return len(r.peers)
}

// IsClosed returns whether the room is closed.
func (r *Room) IsClosed() bool {
	r.closedMu.RLock()
	defer r.closedMu.RUnlock()
	return r.closed
}

// Close closes the room and all peer connections.
func (r *Room) Close() error {
	r.closedMu.Lock()
	if r.closed {
		r.closedMu.Unlock()
		return nil
	}
	r.closed = true
	r.closedMu.Unlock()

	r.peersMu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.peers = make(map[string]*Peer)
	r.peersMu.Unlock()

	for _, p := range peers {
		p.Close()
	}

	r.logger.Info("Room closed")
	return nil
}

func (r *Room) broadcastMessage(excludePeerID string, msg *ServerMessage) {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	for id, peer := range r.peers {
		if id == excludePeerID {
			continue
		}
		peer.SendMessage(msg)
	}
}

// buildICEServers constructs ICE server config from TURN settings.
func (r *Room) buildICEServers() []webrtc.ICEServer {
	if len(r.config.TURNServers) == 0 || r.config.TURNSecret == "" {
		return nil
	}

	var urls []string
	for _, ts := range r.config.TURNServers {
		if ts.Secure {
			urls = append(urls, fmt.Sprintf("turns:%s:%d", ts.Host, ts.Port))
		} else {
			urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=udp", ts.Host, ts.Port))
			urls = append(urls, fmt.Sprintf("turn:%s:%d?transport=tcp", ts.Host, ts.Port))
		}
	}

	ttl := time.Duration(r.config.TURNCredentialTTL) * time.Second
	username, password := turn.GenerateCredentials(r.config.TURNSecret, r.config.Namespace, ttl)

	return []webrtc.ICEServer{
		{
			URLs:       urls,
			Username:   username,
			Credential: password,
		},
	}
}
