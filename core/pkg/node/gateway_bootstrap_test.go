package node

import (
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
)

func loopbackHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("libp2p host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// The genesis node has no configured bootstrap peer; its gateway still dials
// its own node, so it is never left with no peer at all.
func TestGatewayBootstrapPeers_selfFirstEvenWithNoneConfigured(t *testing.T) {
	h := loopbackHost(t)
	peers, err := gatewayBootstrapPeers(h, "127.0.0.1", nil)
	if err != nil {
		t.Fatalf("gatewayBootstrapPeers: %v", err)
	}
	if len(peers) != 1 || !strings.HasPrefix(peers[0], "/ip4/127.0.0.1/tcp/") || !strings.HasSuffix(peers[0], "/p2p/"+h.ID().String()) {
		t.Fatalf("peers = %v, want this node's overlay address with its id", peers)
	}
}

func TestGatewayBootstrapPeers_configuredFollowWithoutDuplicatesOrBlanks(t *testing.T) {
	h := loopbackHost(t)
	self, err := overlayMultiaddr(h, "127.0.0.1")
	if err != nil {
		t.Fatalf("overlayMultiaddr: %v", err)
	}
	other := "/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWHNzqqHg1jgvcfNaG1jUfZJ1KLLG7rLrUFbGpoUBUDEPt"
	peers, err := gatewayBootstrapPeers(h, "127.0.0.1", []string{self, " ", other})
	if err != nil {
		t.Fatalf("gatewayBootstrapPeers: %v", err)
	}
	if len(peers) != 2 || peers[0] != self || peers[1] != other {
		t.Fatalf("peers = %v, want [self, other]", peers)
	}
}

func TestGatewayBootstrapPeers_noListenerOnTheOverlayIsAnError(t *testing.T) {
	h := loopbackHost(t)
	if _, err := gatewayBootstrapPeers(h, "10.0.0.9", nil); err == nil || !strings.Contains(err.Error(), "10.0.0.9") {
		t.Fatalf("err = %v, want one naming the overlay IP", err)
	}
}

func TestGatewayBootstrapPeers_noHostIsAnError(t *testing.T) {
	if _, err := gatewayBootstrapPeers(nil, "10.0.0.1", nil); err == nil {
		t.Fatal("a missing host must be an error, not an empty peer list")
	}
}
