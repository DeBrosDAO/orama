package gateway

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
)

// TestExtractLibp2pTCPPort_FindsPort verifies the helper finds the TCP port
// from a typical libp2p host.Addrs() result.
//
// This is the regression guard for the bug where peer_discovery was
// announcing the gateway's HTTP API port (e.g. 10004) instead of the
// libp2p host's actual TCP port (random per restart). With the wrong
// port in the multiaddr, every cross-node libp2p dial landed on the HTTP
// server and failed the multistream handshake with "message did not have
// trailing newline" — leaving the cluster's namespace mesh with 0
// connected peers and silently dropping all cross-node pubsub traffic.
func TestExtractLibp2pTCPPort_FindsPort(t *testing.T) {
	addrs := mustParseAddrs(t,
		"/ip4/127.0.0.1/tcp/43043",
		"/ip4/217.76.56.2/tcp/43043",
	)

	port, err := extractLibp2pTCPPort(addrs)
	if err != nil {
		t.Fatalf("extractLibp2pTCPPort: %v", err)
	}
	if port != 43043 {
		t.Errorf("port = %d, want 43043", port)
	}
}

// TestExtractLibp2pTCPPort_SkipsNonTCPAddrs verifies the helper does not
// fail when the host advertises non-TCP transports (e.g. QUIC, WebSocket).
// It must find the first TCP entry and return that.
func TestExtractLibp2pTCPPort_SkipsNonTCPAddrs(t *testing.T) {
	addrs := mustParseAddrs(t,
		"/ip4/127.0.0.1/udp/9999/quic-v1",
		"/ip4/127.0.0.1/tcp/43043",
		"/ip4/217.76.56.2/tcp/43043",
	)

	port, err := extractLibp2pTCPPort(addrs)
	if err != nil {
		t.Fatalf("extractLibp2pTCPPort: %v", err)
	}
	if port != 43043 {
		t.Errorf("port = %d, want 43043 (TCP port should be picked, not QUIC)", port)
	}
}

// TestExtractLibp2pTCPPort_NoAddrsReturnsError verifies the helper returns
// an error rather than silently announcing port 0 when the host hasn't
// reported any addresses yet (e.g. called too early in lifecycle).
//
// A silent failure mode here is exactly what masked the original bug for
// so long — we'd rather get a loud error at register time than write
// `/ip4/.../tcp/0/...` to the discovery table.
func TestExtractLibp2pTCPPort_NoAddrsReturnsError(t *testing.T) {
	_, err := extractLibp2pTCPPort(nil)
	if err == nil {
		t.Error("expected error for nil addrs, got nil")
	}
}

// TestExtractLibp2pTCPPort_AllUDPReturnsError verifies the helper returns
// an error when no TCP transports are present (UDP-only host). Persisting
// a TCP multiaddr that no listener serves would be the same class of bug.
func TestExtractLibp2pTCPPort_AllUDPReturnsError(t *testing.T) {
	addrs := mustParseAddrs(t,
		"/ip4/127.0.0.1/udp/9999/quic-v1",
		"/ip4/217.76.56.2/udp/9999/quic-v1",
	)

	if _, err := extractLibp2pTCPPort(addrs); err == nil {
		t.Error("expected error for TCP-less addrs, got nil")
	}
}

// TestExtractLibp2pTCPPort_AllAddrsShareSamePort verifies the realistic
// libp2p output shape: one entry per detected interface IP, all sharing
// the same OS-assigned port (because the listener binds 0.0.0.0:RANDOM).
// We take the first; we expect them all equal.
func TestExtractLibp2pTCPPort_AllAddrsShareSamePort(t *testing.T) {
	addrs := mustParseAddrs(t,
		"/ip4/127.0.0.1/tcp/55555",
		"/ip4/10.0.0.6/tcp/55555",
		"/ip4/51.38.128.56/tcp/55555",
	)

	port, err := extractLibp2pTCPPort(addrs)
	if err != nil {
		t.Fatalf("extractLibp2pTCPPort: %v", err)
	}
	if port != 55555 {
		t.Errorf("port = %d, want 55555", port)
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
