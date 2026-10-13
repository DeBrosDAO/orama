package sfu

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"go.uber.org/zap"
)

// reportMembership queues a join or leave of peer for the namespace.
func (r *Room) reportMembership(peer *Peer, eventType, reason string) {
	if r.reporter == nil {
		return
	}
	r.reporter.report(ctrlauth.MembershipEvent{
		Type:     eventType,
		Room:     r.ID,
		UserID:   peer.UserID,
		DeviceID: peer.DeviceID,
		PeerID:   peer.ID,
		Reason:   reason,
		At:       time.Now().UTC(),
	})
}

// peersOfUser returns every peer in the room authenticated as userID: one per
// device the user is connected from.
func (r *Room) peersOfUser(userID string) []*Peer {
	var out []*Peer
	for _, p := range r.snapshotPeers("") {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	return out
}

// KickUser removes every peer of userID from the room: each is told why, then
// its socket and PeerConnection are closed. It returns how many were removed.
func (r *Room) KickUser(userID string) int {
	peers := r.peersOfUser(userID)
	for _, p := range peers {
		r.KickPeer(p)
	}
	return len(peers)
}

// KickPeer removes one peer on the namespace's behalf.
func (r *Room) KickPeer(p *Peer) {
	p.kicked.Store(true)
	if err := p.SendMessage(NewServerMessage(MessageTypeKicked, &KickedData{Code: KickedCodeRemoved, Reason: "removed from the room by the namespace"})); err != nil {
		r.logger.Debug("Could not tell a kicked peer why", zap.String("peer_id", p.ID), zap.Error(err))
	}
	r.RemovePeer(p.ID)
}

// ExpirePeer removes peer because the admission it joined on has ended: it is
// told why, then its socket and PeerConnection are closed.
func (r *Room) ExpirePeer(peer *Peer) {
	peer.expired.Store(true)
	if err := peer.SendMessage(NewServerMessage(MessageTypeKicked, &KickedData{Code: KickedCodeExpired, Reason: "your admission to the room has ended"})); err != nil {
		r.logger.Debug("Could not tell a peer its admission ended", zap.String("peer_id", peer.ID), zap.Error(err))
	}
	r.RemovePeer(peer.ID)
}

// MuteUser stops (or resumes) the forwarding of every audio track of userID
// and tells the room. It returns how many peers of the user the room has.
func (r *Room) MuteUser(userID string, muted bool) int {
	peers := r.peersOfUser(userID)
	for _, p := range peers {
		r.setPeerMuted(p, muted)
	}
	return len(peers)
}

// setPeerMuted sets one peer's mute and tells the room, as one step per peer:
// two of them interleaving (a join settling its mute while the user is
// unmuted) would otherwise leave the room told the older state.
func (r *Room) setPeerMuted(p *Peer, muted bool) {
	p.muteMu.Lock()
	defer p.muteMu.Unlock()
	p.muted.Store(muted)
	r.broadcastMessage("", NewServerMessage(MessageTypeParticipantState, &ParticipantStateData{
		PeerID: p.ID, UserID: p.UserID, Kind: "audio", Enabled: !muted, Forced: true,
	}))
}

// relayState tells the others in the room that peer turned its audio or video
// on or off. The server does not act on it: the media itself shows whether a
// track is live. It is for the others' interfaces.
func (r *Room) relayState(peer *Peer, kind string, enabled bool) {
	r.broadcastMessage(peer.ID, NewServerMessage(MessageTypeParticipantState, &ParticipantStateData{
		PeerID: peer.ID, UserID: peer.UserID, Kind: kind, Enabled: enabled,
	}))
}
