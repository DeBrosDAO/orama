package gateway

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
)

// The registered address is where the host listens. It must never be the
// gateway's HTTP API port: peers dialled /ip4/<wg>/tcp/10004, hit the HTTP
// server, failed the multistream handshake, and the namespace mesh had 0
// connected peers.
func TestAdvertisedLibp2pAddr_theWireGuardListener(t *testing.T) {
	addrs := mustParseAddrs(t,
		"/ip4/10.0.0.6/udp/9999/quic-v1",
		"/ip4/10.0.0.6/tcp/43043",
	)
	ip, port, err := advertisedLibp2pAddr(addrs)
	if err != nil {
		t.Fatalf("advertisedLibp2pAddr: %v", err)
	}
	if ip.String() != "10.0.0.6" || port != 43043 {
		t.Errorf("got %s:%d, want 10.0.0.6:43043 (the TCP listener, not QUIC)", ip, port)
	}
}

// A listener on every interface, on loopback or on a public address is not
// one the namespace's other gateways can be sent to over the overlay.
func TestAdvertisedLibp2pAddr_refusesAddressesOffTheOverlay(t *testing.T) {
	for _, raw := range []string{
		"/ip4/0.0.0.0/tcp/43043",
		"/ip4/127.0.0.1/tcp/43043",
		"/ip4/217.76.56.2/tcp/43043",
		"/ip4/10.0.1.6/tcp/43043",
		"/ip6/::1/tcp/43043",
	} {
		if ip, port, err := advertisedLibp2pAddr(mustParseAddrs(t, raw)); err == nil {
			t.Errorf("%s: registered %s:%d", raw, ip, port)
		}
	}
}

// With a public and an overlay listener, the overlay one is registered.
func TestAdvertisedLibp2pAddr_picksTheOverlayAmongOthers(t *testing.T) {
	addrs := mustParseAddrs(t, "/ip4/217.76.56.2/tcp/1111", "/ip4/10.0.0.3/tcp/2222")
	ip, port, err := advertisedLibp2pAddr(addrs)
	if err != nil {
		t.Fatalf("advertisedLibp2pAddr: %v", err)
	}
	if ip.String() != "10.0.0.3" || port != 2222 {
		t.Errorf("got %s:%d, want 10.0.0.3:2222", ip, port)
	}
}

// No listener is an error at register time, not a row with port 0.
func TestAdvertisedLibp2pAddr_noListener(t *testing.T) {
	if _, _, err := advertisedLibp2pAddr(nil); err == nil {
		t.Error("a host with no listener produced an address")
	}
	if _, _, err := advertisedLibp2pAddr(mustParseAddrs(t, "/ip4/10.0.0.6/udp/9999/quic-v1")); err == nil {
		t.Error("a UDP-only host produced a TCP address")
	}
}

func mustParseAddrs(t *testing.T, raws ...string) []multiaddr.Multiaddr {
	t.Helper()
	out := make([]multiaddr.Multiaddr, 0, len(raws))
	for _, r := range raws {
		m, err := multiaddr.NewMultiaddr(r)
		if err != nil {
			t.Fatalf("parse multiaddr %q: %v", r, err)
		}
		out = append(out, m)
	}
	return out
}
