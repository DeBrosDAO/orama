package client

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p"
)

func peerTestNetwork(t *testing.T) *NetworkInfoImpl {
	t.Helper()
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		t.Fatalf("libp2p host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	c := &Client{host: h, connected: true}
	return &NetworkInfoImpl{client: c}
}

// A peer the caller described wrongly is ErrInvalidPeer, which the gateway
// answers 400; it is not a failed dial.
func TestConnectToPeer_badAddressIsInvalidPeer(t *testing.T) {
	n := peerTestNetwork(t)
	ctx := WithInternalAuth(context.Background())
	for _, addr := range []string{
		"", "garbage", "/ip4/192.0.2.1/tcp/4001",
		"/p2p/12D3KooWGzxzKZYveHXtpG6AsrUJBcWxHBFS2HsEoGTxrMLvKXtf",
	} {
		if err := n.ConnectToPeer(ctx, addr); !errors.Is(err, ErrInvalidPeer) {
			t.Errorf("ConnectToPeer(%q) = %v, want ErrInvalidPeer", addr, err)
		}
	}
}

func TestDisconnectFromPeer_badIDIsInvalidPeerAndUnheldPeerIsNoOp(t *testing.T) {
	n := peerTestNetwork(t)
	ctx := WithInternalAuth(context.Background())
	if err := n.DisconnectFromPeer(ctx, "not a peer id"); !errors.Is(err, ErrInvalidPeer) {
		t.Errorf("bad peer id = %v, want ErrInvalidPeer", err)
	}
	if err := n.DisconnectFromPeer(ctx, "12D3KooWGzxzKZYveHXtpG6AsrUJBcWxHBFS2HsEoGTxrMLvKXtf"); err != nil {
		t.Errorf("disconnecting an unconnected peer = %v, want nil", err)
	}
}

func TestConnectToPeer_needsCredentialWithoutInternalAuth(t *testing.T) {
	n := peerTestNetwork(t)
	if err := n.ConnectToPeer(context.Background(), "x"); err == nil || errors.Is(err, ErrInvalidPeer) {
		t.Errorf("ConnectToPeer with no credential = %v, want an authentication error", err)
	}
}
