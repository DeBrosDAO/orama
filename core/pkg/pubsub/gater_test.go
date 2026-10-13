package pubsub

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

var testOverlay = netip.MustParsePrefix("10.0.0.0/24")

// connAddrs is the remote address of a connection, for the gater's intercepts.
type connAddrs struct{ remote multiaddr.Multiaddr }

func (c connAddrs) LocalMultiaddr() multiaddr.Multiaddr  { return nil }
func (c connAddrs) RemoteMultiaddr() multiaddr.Multiaddr { return c.remote }

func mustAddr(t *testing.T, s string) multiaddr.Multiaddr {
	t.Helper()
	ma, err := multiaddr.NewMultiaddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return ma
}

func TestOverlayGater_refusesToDialOutsideTheOverlay(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	for name, addr := range map[string]string{
		"public":   "/ip4/8.8.8.8/tcp/4001",
		"loopback": "/ip4/127.0.0.1/tcp/4001",
		"ipv6":     "/ip6/::1/tcp/4001",
		"dns":      "/dns4/example.com/tcp/4001",
		"udp":      "/ip4/10.0.0.5/udp/4001",
		"relay":    "/ip4/8.8.8.8/tcp/4001/p2p-circuit",
	} {
		if g.InterceptAddrDial("", mustAddr(t, addr)) {
			t.Errorf("%s: dial to %s allowed", name, addr)
		}
	}
	if !g.InterceptAddrDial("", mustAddr(t, "/ip4/10.0.0.5/tcp/4001")) {
		t.Error("dial to an overlay address refused")
	}
	if g.InterceptAddrDial("", nil) {
		t.Error("dial to a nil address allowed")
	}
}

func TestOverlayGater_refusesToDialAPeerItWasNotTold(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	id := peer.ID("peer-a")
	if g.InterceptPeerDial(id) {
		t.Fatal("dial to an unknown peer allowed")
	}
	g.Pin(id)
	if !g.InterceptPeerDial(id) {
		t.Fatal("dial to an allowlisted peer refused")
	}
}

func TestOverlayGater_inboundFromAnUnknownPeerIsRefused(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	cm := connAddrs{mustAddr(t, "/ip4/10.0.0.7/tcp/5000")}
	id := peer.ID("peer-a")
	if g.InterceptSecured(network.DirInbound, id, cm) {
		t.Fatal("an unknown peer's inbound connection was accepted")
	}
	g.Pin(id)
	if !g.InterceptSecured(network.DirInbound, id, cm) {
		t.Fatal("an allowlisted peer's inbound connection was refused")
	}
}

func TestOverlayGater_inboundFromOutsideTheOverlayIsRefusedEvenForAnAllowedPeer(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	id := peer.ID("peer-a")
	g.Pin(id)
	for _, addr := range []string{"/ip4/8.8.8.8/tcp/5000", "/ip4/127.0.0.1/tcp/5000", "/ip6/::1/tcp/5000"} {
		cm := connAddrs{mustAddr(t, addr)}
		if g.InterceptAccept(cm) {
			t.Errorf("accepted a connection from %s", addr)
		}
		if g.InterceptSecured(network.DirInbound, id, cm) {
			t.Errorf("secured a connection from %s", addr)
		}
	}
	if !g.InterceptAccept(connAddrs{mustAddr(t, "/ip4/10.0.0.9/tcp/5000")}) {
		t.Error("a connection from the overlay was refused")
	}
}

// End to end on loopback: a host with the gate refuses a stranger that dials
// it, accepts the peer it was told about, and refuses to dial an unlisted one.
func TestOverlayGater_hostRefusesAStrangerAndAcceptsAnAllowedPeer(t *testing.T) {
	g := NewOverlayGater(loopback)
	server, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.ConnectionGater(g))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dial := func(t *testing.T) (peer.ID, error) {
		t.Helper()
		c, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err = c.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()})
		if err == nil {
			// The client sees a connection once its side is upgraded; the
			// server's gate closes it at its own handshake.
			time.Sleep(200 * time.Millisecond)
			if server.Network().Connectedness(c.ID()) == network.Connected {
				return c.ID(), nil
			}
			return c.ID(), context.Canceled
		}
		return c.ID(), err
	}

	if _, err := dial(t); err == nil {
		t.Fatal("a stranger connected to the gated host")
	}
	friend, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer friend.Close()
	g.Pin(friend.ID())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := friend.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatalf("an allowlisted peer could not connect: %v", err)
	}

	stranger, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	if err := server.Connect(ctx, peer.AddrInfo{ID: stranger.ID(), Addrs: stranger.Addrs()}); err == nil {
		t.Fatal("the gated host dialled a peer it was not told about")
	}
}

// A gate whose prefix is the overlay keeps a host from dialling loopback or a
// public address even for a peer it knows.
func TestOverlayGater_hostWithTheOverlayPrefixDoesNotDialLoopback(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.ConnectionGater(g))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	target, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	g.Pin(target.ID())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.Connect(ctx, peer.AddrInfo{ID: target.ID(), Addrs: target.Addrs()}); err == nil {
		t.Fatal("dialled a loopback address with an overlay-only gate")
	}
}

func TestMesh_connectPeersAllowsThePeersItDials(t *testing.T) {
	a, b := startNode(t), startNode(t)
	bs, err := b.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	if a.gater.InterceptPeerDial(b.id) {
		t.Fatal("b allowed before it was named")
	}
	b.gater.Pin(a.id)
	if _, err := a.mesh.ConnectPeers(context.Background(), bs.Addrs, nil); err != nil {
		t.Fatal(err)
	}
	if !a.gater.InterceptPeerDial(b.id) {
		t.Fatal("ConnectPeers did not allowlist the peer it dialled")
	}
}

// The first round: a connects to b before b's gateway has told b about a. b
// refuses; once b learns a (its own round), a's next round connects.
func TestMesh_anEarlyInboundIsRefusedAndTheNextRoundConnects(t *testing.T) {
	a, b := startNode(t), startNode(t)
	bs, err := b.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.mesh.ConnectPeers(context.Background(), bs.Addrs, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if b.mesh.host.Network().Connectedness(a.id) == network.Connected {
		t.Fatalf("b accepted a before it was told about a (result %+v)", res)
	}
	as, err := a.mesh.Self()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.mesh.ConnectPeers(context.Background(), as.Addrs, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err = a.mesh.ConnectPeers(context.Background(), bs.Addrs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Connected == 1 && len(res.Failed) == 0 {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("a never connected to b after b learned a: %+v", res)
}

// An address that starts on the overlay but layers a relay circuit or a
// websocket on it is not an overlay TCP address and is refused.
func TestOverlayGater_onlyAPlainOverlayTCPAddressPasses(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	for _, addr := range []string{
		"/ip4/10.0.0.5/tcp/4001/p2p/12D3KooWHNzqqHg1jgvcfNaG1jUfZJ1KLLG7rLrUFbGpoUBUDEPt/p2p-circuit",
		"/ip4/10.0.0.5/tcp/4001/ws",
		"/ip4/10.0.0.5/udp/4001/quic-v1",
	} {
		if g.InterceptAddrDial("", mustAddr(t, addr)) {
			t.Errorf("dial to %s allowed", addr)
		}
	}
	if !g.InterceptAddrDial("", mustAddr(t, "/ip4/10.0.0.5/tcp/4001/p2p/12D3KooWHNzqqHg1jgvcfNaG1jUfZJ1KLLG7rLrUFbGpoUBUDEPt")) {
		t.Error("an overlay TCP address with its peer id was refused")
	}
}

// Each round replaces the mesh peers: one no longer named — departed or
// removed from the registry — is refused from then on. Pinned bootstrap peers
// stay.
func TestOverlayGater_aPeerDroppedFromTheRoundIsNoLongerAdmitted(t *testing.T) {
	g := NewOverlayGater(testOverlay)
	boot, a, b := peer.ID("boot"), peer.ID("peer-a"), peer.ID("peer-b")
	g.Pin(boot)
	g.SetMeshPeers(a, b)
	if !g.InterceptPeerDial(a) || !g.InterceptPeerDial(b) {
		t.Fatal("peers named in the round are not admitted")
	}
	g.SetMeshPeers(b)
	if g.InterceptPeerDial(a) {
		t.Error("a peer dropped from the round is still admitted")
	}
	if !g.InterceptPeerDial(b) || !g.InterceptPeerDial(boot) {
		t.Error("a peer still named, or a pinned one, is refused")
	}
}
