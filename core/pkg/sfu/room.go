package sfu

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// timeAfterHook lets a test replace time.After. Atomic because goroutines of
// earlier tests (empty-room cleanup, reconnect timeouts) may still be calling
// timeAfter while a later test installs its own.
var timeAfterHook atomic.Pointer[func(time.Duration) <-chan time.Time]

func timeAfter(d time.Duration) <-chan time.Time {
	if f := timeAfterHook.Load(); f != nil {
		return (*f)(d)
	}
	return time.After(d)
}

const (
	reconnectTimeout = 15 * time.Second
	emptyRoomTTL     = 60 * time.Second
	rtpBufferSize    = 8192
	maxRoomPeers     = 100
)

var (
	ErrRoomFull     = errors.New("room is full")
	ErrRoomClosed   = errors.New("room is closed")
	ErrPeerNotFound = errors.New("peer not found")
)

// publishedTrack holds a local track being forwarded from a remote source.
type publishedTrack struct {
	sourcePeerID    string
	sourceUserID    string
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteTrackSSRC uint32
	kind            string
	keyframes       *keyframeLimiter
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

	// allowDirectICE lets the room's peers use host candidates instead of the
	// TURN relay only; set by a test whose two ends share a process.
	allowDirectICE bool

	// reporter takes the room's join and leave events; nil reports nothing.
	reporter *reporter
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
	if len(r.peers) >= maxRoomPeers {
		r.peersMu.Unlock()
		return ErrRoomFull
	}

	peer.OnClose(func(p *Peer) { r.RemovePeer(p.ID) })

	if err := peer.InitPeerConnection(r.api, iceServers); err != nil {
		r.peersMu.Unlock()
		return fmt.Errorf("failed to create peer connection for %s: %w", peer.ID, err)
	}

	r.peers[peer.ID] = peer
	info := peer.GetInfo()
	total := len(r.peers)
	// Reported under the lock, which orders it against the peer's leave: a
	// RemovePeer that follows cannot queue its leave before this join. The
	// report never blocks.
	r.reportMembership(peer, ctrlauth.EventJoin, "")
	r.peersMu.Unlock()

	if len(iceServers) > 0 {
		go peer.turnRefreshLoop()
	}

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
	r.reportMembership(peer, ctrlauth.EventLeave, leaveReason(peer))
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

// leaveReason is why a peer that is being removed ended.
func leaveReason(p *Peer) string {
	switch {
	case p.kicked.Load():
		return ctrlauth.KickedReason
	case p.expired.Load():
		return ctrlauth.ExpiredReason
	}
	return ctrlauth.LeftReason
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
	for _, p := range peers {
		r.reportMembership(p, ctrlauth.EventLeave, ctrlauth.ClosedReason)
	}
	r.peersMu.Unlock()

	for _, p := range peers {
		p.Close()
	}

	r.logger.Info("Room closed")
	return nil
}

// snapshotPeers returns the room's peers except excludePeerID, copied under the
// lock so callers can write to them without holding it: a slow peer must not
// block joins, leaves or other writers.
func (r *Room) snapshotPeers(excludePeerID string) []*Peer {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	peers := make([]*Peer, 0, len(r.peers))
	for id, peer := range r.peers {
		if id != excludePeerID {
			peers = append(peers, peer)
		}
	}
	return peers
}

// broadcastMessage sends msg to every peer but excludePeerID. A peer whose
// write fails is disconnected by SendMessage; the others are unaffected.
func (r *Room) broadcastMessage(excludePeerID string, msg *ServerMessage) {
	for _, peer := range r.snapshotPeers(excludePeerID) {
		if err := peer.SendMessage(msg); err != nil {
			r.logger.Debug("Broadcast to peer failed",
				zap.String("peer_id", peer.ID), zap.String("type", string(msg.Type)), zap.Error(err))
		}
	}
}
