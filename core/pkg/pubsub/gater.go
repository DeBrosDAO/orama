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
// The allowlist has two parts. The node hosts the service bootstraps from are
// pinned for its life. The mesh peers are replaced on every round the gateway
// sends (Mesh.ConnectPeers): each round names the peers this service dials and
// the ones it lets connect in, so a peer that left the registry — departed,
// or removed — stops being admitted within a round instead of staying trusted
// until the service restarts. A peer that connects before this node's gateway
// has named it is refused at the security handshake and dials again on its own
// next round.
type OverlayGater struct {
	overlay netip.Prefix

	mu     sync.RWMutex
	pinned map[peer.ID]struct{}
	mesh   map[peer.ID]struct{}
}

// NewOverlayGater returns a gate that admits only addresses inside overlay and
// only peers pinned with Pin or named in the last SetMeshPeers.
func NewOverlayGater(overlay netip.Prefix) *OverlayGater {
	return &OverlayGater{overlay: overlay, pinned: make(map[peer.ID]struct{}), mesh: make(map[peer.ID]struct{})}
}

// Pin admits the peers for the life of the gate: the bootstrap node hosts.
func (g *OverlayGater) Pin(ids ...peer.ID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range ids {
		g.pinned[id] = struct{}{}
	}
}

// SetMeshPeers replaces the mesh part of the allowlist with ids. Call it before
// dialling them.
func (g *OverlayGater) SetMeshPeers(ids ...peer.ID) {
	next := make(map[peer.ID]struct{}, len(ids))
	for _, id := range ids {
		next[id] = struct{}{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mesh = next
}

func (g *OverlayGater) isAllowed(id peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.pinned[id]; ok {
		return true
	}
	_, ok := g.mesh[id]
	return ok
}

// inOverlay reports whether addr is exactly /ip4/<overlay ip>/tcp/<port>,
// optionally followed by /p2p/<id>: nothing layered on it (a relay circuit, a
// websocket) passes, whatever overlay address it starts with.
func (g *OverlayGater) inOverlay(addr multiaddr.Multiaddr) bool {
	if addr == nil {
		return false
	}
	protos := addr.Protocols()
	if len(protos) == 3 && protos[2].Code == multiaddr.P_P2P {
		protos = protos[:2]
	}
	if len(protos) != 2 || protos[0].Code != multiaddr.P_IP4 || protos[1].Code != multiaddr.P_TCP {
		return false
	}
	ip, err := addr.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		return false
	}
	parsed, err := netip.ParseAddr(ip)
	return err == nil && g.overlay.Contains(parsed)
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
