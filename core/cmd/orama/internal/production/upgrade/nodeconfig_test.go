package upgrade

import (
	"net"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestOlricSeeds_usesBootstrapPeersAndWireGuard(t *testing.T) {
	// Genesis has no bootstrap peer. Its WireGuard config names the other
	// two nodes, and also a private key that must not become a seed.
	wg := []byte("[Interface]\nPrivateKey = not-a-seed\nAddress = 10.0.0.1/24\n\n[Peer]\nAllowedIPs = 10.0.0.2/32\n\n[Peer]\nAllowedIPs = 10.0.0.3/32\n")
	got := olricSeeds(nil, "10.0.0.1", wg)
	port := strconv.Itoa(constants.OlricMemberlistPort)
	want := []string{
		net.JoinHostPort("10.0.0.2", port),
		net.JoinHostPort("10.0.0.3", port),
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("seeds = %v, want %v", got, want)
	}
}

func TestOlricSeeds_skipsSelfAndDuplicates(t *testing.T) {
	wg := []byte("AllowedIPs = 10.0.0.1/32, 10.0.0.2/32\n")
	got := olricSeeds([]string{"/ip4/10.0.0.2/tcp/4001/p2p/abc"}, "10.0.0.1", wg)
	want := net.JoinHostPort("10.0.0.2", strconv.Itoa(constants.OlricMemberlistPort))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("seeds = %v, want [%s]", got, want)
	}
}
