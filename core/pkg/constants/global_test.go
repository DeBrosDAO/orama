package constants_test

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestGlobalPorts_doNotCollideWithClusterOrChain(t *testing.T) {
	if constants.ChainP2PPort != 31000 || constants.ChainRPCPort != 31001 ||
		constants.ChainGRPCPort != 31002 || constants.ChainAPIPort != 31003 ||
		constants.ChainPrometheusPort != 31004 {
		t.Fatalf("chain ports = %d %d %d %d %d, want 31000-31004",
			constants.ChainP2PPort, constants.ChainRPCPort, constants.ChainGRPCPort,
			constants.ChainAPIPort, constants.ChainPrometheusPort)
	}
	cluster := map[int]string{
		constants.RQLiteHTTPPort:      "rqlite",
		constants.OlricHTTPPort:       "olric",
		constants.OlricMemberlistPort: "olric memberlist",
		constants.GatewayAPIPort:      "gateway",
		constants.IPFSAPIPort:         "ipfs",
		constants.ChainP2PPort:        "chain p2p",
		constants.ChainRPCPort:        "chain rpc",
		constants.ChainGRPCPort:       "chain grpc",
		constants.ChainAPIPort:        "chain api",
		constants.ChainPrometheusPort: "chain prometheus",
	}
	if constants.RQLiteHTTPPort != 10100 || constants.OlricHTTPPort != 10102 ||
		constants.OlricMemberlistPort != 10103 || constants.GatewayAPIPort != 10104 ||
		constants.IPFSAPIPort != 10107 {
		t.Fatalf("cluster ports drifted: rqlite %d olric %d/%d gateway %d ipfs %d",
			constants.RQLiteHTTPPort, constants.OlricHTTPPort, constants.OlricMemberlistPort,
			constants.GatewayAPIPort, constants.IPFSAPIPort)
	}
	global := []int{
		constants.GlobalIPFSSwarmPort,
		constants.GlobalIPFSAPIPort,
		constants.GlobalIPFSGatewayPort,
		constants.GlobalProviderPort,
		constants.GlobalRelayMetricsPort,
		constants.GlobalTorORPort,
		constants.GlobalTorDirPort,
	}
	seen := map[int]bool{}
	for _, p := range global {
		if seen[p] {
			t.Errorf("global port %d is used twice", p)
		}
		seen[p] = true
		if name, ok := cluster[p]; ok {
			t.Errorf("global port %d collides with %s", p, name)
		}
		if p < constants.GlobalPortBase || p > constants.GlobalPortEnd {
			t.Errorf("global port %d is outside %d-%d", p, constants.GlobalPortBase, constants.GlobalPortEnd)
		}
	}
	if constants.GlobalIPFSSwarmPort != 31010 || constants.GlobalIPFSAPIPort != 31011 ||
		constants.GlobalIPFSGatewayPort != 31012 || constants.GlobalProviderPort != 31013 ||
		constants.GlobalRelayMetricsPort != 31014 || constants.GlobalTorORPort != 31020 ||
		constants.GlobalTorDirPort != 31021 {
		t.Fatalf("global ports drifted: swarm %d api %d gateway %d provider %d relay %d or %d dir %d",
			constants.GlobalIPFSSwarmPort, constants.GlobalIPFSAPIPort, constants.GlobalIPFSGatewayPort,
			constants.GlobalProviderPort, constants.GlobalRelayMetricsPort, constants.GlobalTorORPort,
			constants.GlobalTorDirPort)
	}
}

// The 31000 block must not overlap a port some other Orama service already
// owns, or a range the plan keeps for SFU media and TURN.
func TestGlobalPortBlock_overlapsNothingElse(t *testing.T) {
	occupied := []struct {
		name       string
		start, end int
	}{
		{"libp2p", constants.NodeLibP2PPort, constants.NodeLibP2PPort},
		{"cluster kubo swarm", constants.IPFSSwarmPort, constants.IPFSSwarmPort},
		{"cluster kubo gateway", constants.IPFSGatewayPort, constants.IPFSGatewayPort},
		{"tor socks", 9050, 9050},
		{"tenant and index", 10000, constants.IndexPortEnd},
		{"sfu media", 20000, 30099},
		{"turn", 49152, 65535},
	}
	for _, block := range occupied {
		if constants.GlobalPortEnd >= block.start && constants.GlobalPortBase <= block.end {
			t.Errorf("global block %d-%d overlaps %s %d-%d",
				constants.GlobalPortBase, constants.GlobalPortEnd, block.name, block.start, block.end)
		}
	}
}
