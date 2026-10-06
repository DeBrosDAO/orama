package main

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p"
	libp2ppubsub "github.com/libp2p/go-libp2p-pubsub"
	"go.uber.org/zap"
)

func fixedIP(ip string, err error) func() (string, error) {
	return func() (string, error) { return ip, err }
}

// The libp2p host listened on 0.0.0.0, the public interface included. It
// listens on the node's WireGuard address, and refuses to start without one
// rather than falling back to every interface.
func TestOverlayListenAddr(t *testing.T) {
	got, err := overlayListenAddr(fixedIP("10.0.0.7", nil))
	if err != nil || got != "/ip4/10.0.0.7/tcp/0" {
		t.Fatalf("got %q, %v", got, err)
	}
	for name, ip := range map[string]func() (string, error){
		"no wg0":            fixedIP("", errors.New("wg0 interface not found")),
		"public address":    fixedIP("203.0.113.7", nil),
		"unspecified":       fixedIP("0.0.0.0", nil),
		"outside the mesh":  fixedIP("10.1.0.7", nil),
		"not an IP address": fixedIP("wg0", nil),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := overlayListenAddr(ip); err == nil {
				t.Fatalf("listened on %q", got)
			}
		})
	}
}

const bootstrapPeerID = "12D3KooWRBhwfeP2Y4TCx1SM6s9rUoHhR5STiGwxBhgFRcw3UERE"

func TestParseBootstrap(t *testing.T) {
	good := "/ip4/10.0.0.1/tcp/4001/p2p/" + bootstrapPeerID
	got := parseBootstrap([]string{"nonsense", "/ip4/10.0.0.2/tcp/4001", good}, zap.NewNop())
	if len(got) != 1 || got[0].ID.String() != bootstrapPeerID {
		t.Fatalf("got %v, want only the address that names a peer", got)
	}
	if got := parseBootstrap(nil, zap.NewNop()); len(got) != 0 {
		t.Fatalf("got %v from no addresses", got)
	}
}

// GossipSub builds with the service's options. Peer exchange is left out of
// them (WithPeerExchange(false)): it is off by default and the router exposes
// no getter, so what holds the line when it is on is the connection gate
// (pkg/pubsub gater tests).
func TestGossipSubOptions_buildARouter(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := libp2ppubsub.NewGossipSub(context.Background(), h, gossipSubOptions()...); err != nil {
		t.Fatal(err)
	}
}
