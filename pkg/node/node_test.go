package node

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func TestCalculateNextBackoff(t *testing.T) {
	if got := calculateNextBackoff(10 * time.Second); got <= 10*time.Second || got > 15*time.Second {
		t.Fatalf("unexpected next: %v", got)
	}
	if got := calculateNextBackoff(10 * time.Minute); got != 10*time.Minute {
		t.Fatalf("cap not applied: %v", got)
	}
}

func TestAddJitter(t *testing.T) {
	base := 10 * time.Second
	min := base - time.Duration(0.2*float64(base))
	max := base + time.Duration(0.2*float64(base))
	for i := 0; i < 100; i++ {
		got := addJitter(base)
		if got < time.Second || got < min || got > max {
			t.Fatalf("jitter out of range: %v", got)
		}
	}
}

func TestGetPeerId_WhenNoHost(t *testing.T) {
	n := &Node{}
	if id := n.GetPeerID(); id != "" {
		t.Fatalf("GetPeerID() = %q; want empty string when host is nil", id)
	}
}

func TestLoadOrCreateIdentity(t *testing.T) {
	t.Run("first run creates file with correct perms and round-trips", func(t *testing.T) {
		tempDir := t.TempDir()
		cfg := &config.Config{}
		cfg.Node.DataDir = tempDir

		n, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode() error: %v", err)
		}

		priv, err := n.loadOrCreateIdentity()
		if err != nil {
			t.Fatalf("loadOrCreateIdentity() error: %v", err)
		}
		if priv == nil {
			t.Fatalf("returned private key is nil")
		}

		identityFile := filepath.Join(tempDir, "identity.key")

		// File exists
		fi, err := os.Stat(identityFile)
		if err != nil {
			t.Fatalf("identity file not created: %v", err)
		}

		// Permissions are 0600
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("identity file permissions are incorrect: %v", got)
		}

		// Saved key can be unmarshaled and matches returned key
		data, err := os.ReadFile(identityFile)
		if err != nil {
			t.Fatalf("failed to read identity file: %v", err)
		}
		priv2, err := crypto.UnmarshalPrivateKey(data)
		if err != nil {
			t.Fatalf("UnmarshalPrivateKey: %v", err)
		}

		b1, err := crypto.MarshalPrivateKey(priv)
		if err != nil {
			t.Fatalf("MarshalPrivateKey(priv): %v", err)
		}
		b2, err := crypto.MarshalPrivateKey(priv2)
		if err != nil {
			t.Fatalf("MarshalPrivateKey(priv2): %v", err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("saved key differs from returned key")
		}
	})

	t.Run("second run returns same identity", func(t *testing.T) {
		tempDir := t.TempDir()
		cfg := &config.Config{}
		cfg.Node.DataDir = tempDir

		// First run
		n1, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode() error: %v", err)
		}
		priv1, err := n1.loadOrCreateIdentity()
		if err != nil {
			t.Fatalf("loadOrCreateIdentity(first) error: %v", err)
		}

		// Second run with a new Node pointing to the same dir
		n2, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode() error: %v", err)
		}
		priv2, err := n2.loadOrCreateIdentity()
		if err != nil {
			t.Fatalf("loadOrCreateIdentity(second) error: %v", err)
		}

		// Compare marshaled keys
		b1, err := crypto.MarshalPrivateKey(priv1)
		if err != nil {
			t.Fatalf("MarshalPrivateKey(priv1): %v", err)
		}
		b2, err := crypto.MarshalPrivateKey(priv2)
		if err != nil {
			t.Fatalf("MarshalPrivateKey(priv2): %v", err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("second run did not return the same identity")
		}
	})
}

func TestHashBootstrapConnections(t *testing.T) {
	cfg := &config.Config{}

	n, err := NewNode(cfg)
	if err != nil {
		t.Fatalf("NewNode() error: %v", err)
	}

	// Assert: Does not have bootstrap connections
	conns := n.hasBootstrapConnections()
	if conns != false {
		t.Fatalf("expected false, got %v", conns)
	}

	// Variation: Fresh libp2p still zero connections
	h, err := libp2p.New()
	if err != nil {
		t.Fatalf("libp2p.New() error: %v", err)
	}
	defer h.Close()

	n.host = h
	conns = n.hasBootstrapConnections()
	if conns != false {
		t.Fatalf("expected false, got %v", conns)
	}

	// Assert: Return true if connected to at least one bootstrap peer
	t.Run("returns true when connected to at least one configured bootstrap peer", func(t *testing.T) {
		// Fresh node and config
		cfg := &config.Config{}
		n2, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}

		// Create two hosts (A and B) listening on localhost TCP
		hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/localhost/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New (A): %v", err)
		}
		defer hA.Close()

		hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/localhost/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New (B): %v", err)
		}
		defer hB.Close()

		// Build B's bootstrap multiaddr: <one-of-B.Addrs>/p2p/<B.ID>
		var base multiaddr.Multiaddr
		for _, a := range hB.Addrs() {
			if strings.Contains(a.String(), "/tcp/") {
				base = a
				break
			}
		}
		if base == nil {
			t.Skip("no TCP listen address for host B")
		}
		pidMA, err := multiaddr.NewMultiaddr("/p2p/" + hB.ID().String())
		if err != nil {
			t.Fatalf("NewMultiaddr(/p2p/<id>): %v", err)
		}
		bootstrap := base.Encapsulate(pidMA).String()

		// Configure node A with B as a bootstrap peer
		n2.host = hA
		n2.config.Discovery.BootstrapPeers = []string{bootstrap}

		// Connect A -> B
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := hA.Connect(ctx, peer.AddrInfo{ID: hB.ID(), Addrs: hB.Addrs()}); err != nil {
			t.Fatalf("connect A->B: %v", err)
		}

		// Wait until connected
		deadline := time.Now().Add(2 * time.Second)
		for {
			if hA.Network().Connectedness(hB.ID()) == network.Connected {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("A not connected to B after timeout")
			}
			time.Sleep(10 * time.Millisecond)
		}

		// Assert: hasBootstrapConnections returns true
		if !n2.hasBootstrapConnections() {
			t.Fatalf("expected hasBootstrapConnections() to be true")
		}
	})

	t.Run("returns false when connected peers are not in the bootstrap list", func(t *testing.T) {
		// Fresh node and config
		cfg := &config.Config{}
		n2, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}

		// Create three hosts (A, B, C) listening on localhost TCP
		hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/localhost/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New (A): %v", err)
		}
		defer hA.Close()

		hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/localhost/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New (B): %v", err)
		}
		defer hB.Close()

		hC, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/localhost/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New (C): %v", err)
		}
		defer hC.Close()

		// Build C's bootstrap multiaddr: <one-of-C.Addrs>/p2p/<C.ID>
		var baseC multiaddr.Multiaddr
		for _, a := range hC.Addrs() {
			if strings.Contains(a.String(), "/tcp/") {
				baseC = a
				break
			}
		}
		if baseC == nil {
			t.Skip("no TCP listen address for host C")
		}
		pidC, err := multiaddr.NewMultiaddr("/p2p/" + hC.ID().String())
		if err != nil {
			t.Fatalf("NewMultiaddr(/p2p/<id>): %v", err)
		}
		bootstrapC := baseC.Encapsulate(pidC).String()

		// Configure node A with ONLY C as a bootstrap peer
		n2.host = hA
		n2.config.Discovery.BootstrapPeers = []string{bootstrapC}

		// Connect A -> B (but C is in the bootstrap list, not B)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := hA.Connect(ctx, peer.AddrInfo{ID: hB.ID(), Addrs: hB.Addrs()}); err != nil {
			t.Fatalf("connect A->B: %v", err)
		}

		// Wait until A is connected to B
		deadline := time.Now().Add(2 * time.Second)
		for {
			if hA.Network().Connectedness(hB.ID()) == network.Connected {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("A not connected to B after timeout")
			}
			time.Sleep(10 * time.Millisecond)
		}

		// Assert: hasBootstrapConnections should be false (connected peer is not in bootstrap list)
		if n2.hasBootstrapConnections() {
			t.Fatalf("expected hasBootstrapConnections() to be false")
		}
	})

}
