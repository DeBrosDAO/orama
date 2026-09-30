package pubsub

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	// MaxMeshPeers bounds one ConnectPeers call.
	MaxMeshPeers = 256

	// meshDialTimeout is how long one peer gets to answer a dial. The peers are
	// on the WireGuard mesh and answer at once, or are down.
	meshDialTimeout = 5 * time.Second
)

// Mesh is the pubsub service's view of its libp2p host, for the one job
// GossipSub cannot do for itself: finding the pubsub service on every other
// node. A node's service listens on its own WireGuard address, on a port the
// OS picks, under an identity of its own; nothing in a node's configuration
// names another node's service, and the node libp2p hosts the service
// bootstraps from do not carry app topics. The orama gateway of each node
// publishes its service's address in the cluster registry and tells the
// service the others' (Self, ConnectPeers, served at /mesh/self and
// /mesh/peers).
type Mesh struct {
	host    host.Host
	overlay netip.Prefix
	gater   *OverlayGater
}

// NewMesh returns the mesh of h, which dials and advertises only addresses
// inside overlay, the WireGuard prefix. gater is the connection gate h was
// built with (libp2p.ConnectionGater); every peer ConnectPeers dials is added
// to its allowlist first.
func NewMesh(h host.Host, overlay netip.Prefix, gater *OverlayGater) *Mesh {
	return &Mesh{host: h, overlay: overlay, gater: gater}
}

// MeshSelf is how other nodes' services reach this one.
type MeshSelf struct {
	PeerID string   `json:"peer_id"`
	Addrs  []string `json:"addrs"` // /ip4/<overlay ip>/tcp/<port>/p2p/<peer id>
}

// MeshPeerFailure is a peer the service could not reach.
type MeshPeerFailure struct {
	Addr  string `json:"addr"`
	Error string `json:"error"`
}

// MeshConnectResult is the outcome of ConnectPeers: every requested peer is
// either connected or in Failed. Self is left out of both.
type MeshConnectResult struct {
	Connected int               `json:"connected"`
	Failed    []MeshPeerFailure `json:"failed,omitempty"`
}

// Self is the address this service's host listens on inside the overlay. A
// host with none — bound to every interface or to a public address — is an
// error: advertising it would hand other nodes an address they cannot use.
func (m *Mesh) Self() (MeshSelf, error) {
	var addrs []string
	for _, a := range m.host.Network().ListenAddresses() {
		ip, err := a.ValueForProtocol(multiaddr.P_IP4)
		if err != nil {
			continue
		}
		parsed, err := netip.ParseAddr(ip)
		if err != nil || !m.overlay.Contains(parsed) {
			continue
		}
		if _, err := a.ValueForProtocol(multiaddr.P_TCP); err != nil {
			continue
		}
		addrs = append(addrs, fmt.Sprintf("%s/p2p/%s", a, m.host.ID()))
	}
	if len(addrs) == 0 {
		return MeshSelf{}, fmt.Errorf("the pubsub libp2p host listens on no TCP address inside %s (listening on %v)",
			m.overlay, m.host.Network().ListenAddresses())
	}
	return MeshSelf{PeerID: m.host.ID().String(), Addrs: addrs}, nil
}

// ConnectPeers connects the host to every peer in addrs, a list of
// /ip4/<overlay ip>/tcp/<port>/p2p/<peer id>. A list of more than MaxMeshPeers
// is refused whole, as a caller error. An entry that is not such an address, or
// a peer that does not answer, is reported in Failed and the others are still
// dialled, so one bad registry row or one node that is down does not keep the
// rest from connecting. The service dials nothing outside the overlay on the
// caller's behalf, and the valid peers are added to the gate's allowlist, which
// is what lets them connect back.
//
// allow names the peers that may connect in without this host dialling them
// (see pubsub_mesh.go: a node dials its ring successors and allows its ring
// predecessors, so whoever dials it is always allowed). It is held to the same
// limit and validation; its entries are only allowlisted, never dialled.
func (m *Mesh) ConnectPeers(ctx context.Context, addrs, allow []string) (MeshConnectResult, error) {
	if len(addrs) > MaxMeshPeers || len(allow) > MaxMeshPeers {
		return MeshConnectResult{}, fmt.Errorf("%d peers to dial and %d to allow, at most %d of each are accepted", len(addrs), len(allow), MaxMeshPeers)
	}
	var result MeshConnectResult
	allowed, failed := m.parsePeers(allow, "not allowed: ")
	result.Failed = append(result.Failed, failed...)
	targets, failed := m.parsePeers(addrs, "")
	result.Failed = append(result.Failed, failed...)

	// This round names every mesh peer the service may talk to: the ones it
	// dials and the ones it lets in. Anything else leaves the allowlist.
	ids := make([]peer.ID, 0, len(allowed)+len(targets))
	for _, t := range append(allowed, targets...) {
		ids = append(ids, t.info.ID)
	}
	m.gater.SetMeshPeers(ids...)

	dialled := m.dialPeers(ctx, targets)
	dialled.Failed = append(result.Failed, dialled.Failed...)
	return dialled, nil
}

// meshTarget is one parsed peer entry.
type meshTarget struct {
	addr string
	info peer.AddrInfo
}

// parsePeers parses entries, dropping this host itself; an entry that is not
// a peer address is returned as a failure, its error prefixed with prefix.
func (m *Mesh) parsePeers(entries []string, prefix string) ([]meshTarget, []MeshPeerFailure) {
	var (
		out    []meshTarget
		failed []MeshPeerFailure
	)
	for _, a := range entries {
		info, err := m.parsePeer(a)
		if err != nil {
			failed = append(failed, MeshPeerFailure{Addr: a, Error: prefix + err.Error()})
			continue
		}
		if info.ID == m.host.ID() {
			continue
		}
		out = append(out, meshTarget{addr: a, info: info})
	}
	return out, failed
}

// dialPeers dials every target at once and reports the ones that failed.
func (m *Mesh) dialPeers(ctx context.Context, targets []meshTarget) MeshConnectResult {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result MeshConnectResult
	)
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := m.connect(ctx, t.info)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Failed = append(result.Failed, MeshPeerFailure{Addr: t.addr, Error: err.Error()})
				return
			}
			result.Connected++
		}()
	}
	wg.Wait()
	return result
}

func (m *Mesh) connect(ctx context.Context, info peer.AddrInfo) error {
	if m.host.Network().Connectedness(info.ID) == network.Connected {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, meshDialTimeout)
	defer cancel()
	if err := m.host.Connect(ctx, info); err != nil {
		return fmt.Errorf("connect to pubsub peer %s at %v: %w", info.ID, info.Addrs, err)
	}
	return nil
}

// parsePeer is addr as a dialable peer: a TCP address inside the overlay that
// names the peer it reaches.
func (m *Mesh) parsePeer(addr string) (peer.AddrInfo, error) {
	ma, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q: %w", addr, err)
	}
	info, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q does not name a peer (want /ip4/<ip>/tcp/<port>/p2p/<peer id>): %w", addr, err)
	}
	if len(info.Addrs) != 1 {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q: want exactly one transport address, got %d", addr, len(info.Addrs))
	}
	if n := len(info.Addrs[0].Protocols()); n != 2 {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q: want /ip4/<ip>/tcp/<port>/p2p/<peer id>, got %d transport parts", addr, n)
	}
	ip, err := info.Addrs[0].ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q is not an IPv4 address: %w", addr, err)
	}
	parsed, err := netip.ParseAddr(ip)
	if err != nil || !m.overlay.Contains(parsed) {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q is outside the WireGuard overlay %s", addr, m.overlay)
	}
	if _, err := info.Addrs[0].ValueForProtocol(multiaddr.P_TCP); err != nil {
		return peer.AddrInfo{}, fmt.Errorf("peer address %q has no TCP port: %w", addr, err)
	}
	return *info, nil
}
