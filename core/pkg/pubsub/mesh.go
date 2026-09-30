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
}

// NewMesh returns the mesh of h, which dials and advertises only addresses
// inside overlay, the WireGuard prefix.
func NewMesh(h host.Host, overlay netip.Prefix) *Mesh {
	return &Mesh{host: h, overlay: overlay}
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
// /ip4/<overlay ip>/tcp/<port>/p2p/<peer id>. The whole list is refused, and
// nothing dialled, when any entry is not such an address or there are more than
// MaxMeshPeers: the caller is another process on the node, and the service
// dials nothing outside the overlay on its behalf. A peer that does not answer
// is reported in Failed, not an error, so one node that is down does not keep
// the others from connecting.
func (m *Mesh) ConnectPeers(ctx context.Context, addrs []string) (MeshConnectResult, error) {
	if len(addrs) > MaxMeshPeers {
		return MeshConnectResult{}, fmt.Errorf("%d peers, at most %d are accepted", len(addrs), MaxMeshPeers)
	}
	type target struct {
		addr string
		info peer.AddrInfo
	}
	targets := make([]target, 0, len(addrs))
	for _, a := range addrs {
		info, err := m.parsePeer(a)
		if err != nil {
			return MeshConnectResult{}, err
		}
		if info.ID == m.host.ID() {
			continue
		}
		targets = append(targets, target{addr: a, info: info})
	}

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
	return result, nil
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
