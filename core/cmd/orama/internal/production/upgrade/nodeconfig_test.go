package upgrade

import (
	"net"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestOlricSeedsFromMultiaddrs_usesTheOtherNodes(t *testing.T) {
	got := olricSeedsFromMultiaddrs([]string{
		"/ip4/10.0.0.2/tcp/4001",
		"/ip4/10.0.0.1/tcp/4001/p2p/12D3KooW",
		"/ip4/10.0.0.1/tcp/4001/p2p/12D3KooW",
		"not-a-multiaddr",
	}, "10.0.0.2")
	want := net.JoinHostPort("10.0.0.1", strconv.Itoa(constants.OlricMemberlistPort))
	if len(got) != 1 || got[0] != want {
		t.Fatalf("seeds = %v, want [%s]", got, want)
	}
}

func TestOlricSeedsFromMultiaddrs_genesisHasNone(t *testing.T) {
	if got := olricSeedsFromMultiaddrs(nil, "10.0.0.1"); len(got) != 0 {
		t.Fatalf("genesis seeds = %v", got)
	}
}
