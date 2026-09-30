package pubsub

import (
	"net/netip"
	"sync"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// OverlayGater is the pubsub host's connection gate. The host listens on the
// node's WireGuard address, and WireGuard admits every node of the cluster, so
// "on the overlay" is not "a pubsub service": any overlay peer could otherwise
// connect and subscribe to or publish on any namespace's topics, or be
// dialled at any address GossipSub hears about. The gate admits a connection
// only when both hold:
//
//   - its remote address is an IPv4 TCP address inside the overlay prefix
//     (dials and accepts alike), and
//   - the remote peer id is one the service has been told about: the node
//     libp2p hosts it bootstraps from, and the peers the gateway passed to
//     Mesh.ConnectPeers.
//
// The gateway passes every live registered peer on each 15-second round, so
// after one round two services allow each other. A peer that connects before
// this node's gateway has told the service about it is refused at the security
// handshake; its own gateway dials again on the next round, by which time this
// side has learned it. Peers are added and never removed: the set is bounded by
// the cluster's nodes and is reset when the service restarts.
type OverlayGater struct {
	overlay netip.Prefix

	mu      sync.RWMutex
	allowed map[peer.ID]struct{}
}

// NewOverlayGater returns a gate that admits only addresses inside overlay and
// only peers added with Allow.
func NewOverlayGater(overlay netip.Prefix) *OverlayGater {
	return &OverlayGater{overlay: overlay, allowed: make(map[peer.ID]struct{})}
}

// Allow admits the peers. Call it before dialling them.
func (g *OverlayGater) Allow(ids ...peer.ID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range ids {
		g.allowed[id] = struct{}{}
	}
}

func (g *OverlayGater) isAllowed(id peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.allowed[id]
	return ok
}

// inOverlay reports whether addr is /ip4/<overlay ip>/tcp/<port>.
func (g *OverlayGater) inOverlay(addr multiaddr.Multiaddr) bool {
	if addr == nil {
		return false
	}
	ip, err := addr.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		return false
	}
	parsed, err := netip.ParseAddr(ip)
	if err != nil || !g.overlay.Contains(parsed) {
		return false
	}
	_, err = addr.ValueForProtocol(multiaddr.P_TCP)
	return err == nil
}

// InterceptPeerDial refuses to dial a peer the service was not told about.
func (g *OverlayGater) InterceptPeerDial(p peer.ID) bool { return g.isAllowed(p) }

// InterceptAddrDial refuses to dial an address outside the overlay, whatever
// GossipSub or the peerstore holds for the peer.
func (g *OverlayGater) InterceptAddrDial(_ peer.ID, addr multiaddr.Multiaddr) bool {
	return g.inOverlay(addr)
}

// InterceptAccept refuses an inbound connection from outside the overlay before
// any handshake.
func (g *OverlayGater) InterceptAccept(cm network.ConnMultiaddrs) bool {
	return g.inOverlay(cm.RemoteMultiaddr())
}

// InterceptSecured refuses a peer that is not on the allowlist, in either
// direction, once its identity is known.
func (g *OverlayGater) InterceptSecured(_ network.Direction, p peer.ID, cm network.ConnMultiaddrs) bool {
	return g.isAllowed(p) && g.inOverlay(cm.RemoteMultiaddr())
}

// InterceptUpgraded admits a connection that passed the checks above.
func (g *OverlayGater) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}
