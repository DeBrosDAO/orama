package tornettest

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/tornet"
)

func TestNetworkFile_isAValidNetwork(t *testing.T) {
	n, err := tornet.ParseNetwork(NetworkFile(t))
	if err != nil {
		t.Fatalf("the fixture is not a Tor network file: %v", err)
	}
	if len(n.Authorities) != tornet.MinAuthorities || !n.Private {
		t.Errorf("fixture = %+v", n)
	}
}
