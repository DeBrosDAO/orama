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
	global := []int{constants.GlobalIPFSAPIPort, constants.GlobalRelayMetricsPort}
	if global[0] == global[1] {
		t.Fatalf("global ipfs and relay share port %d", global[0])
	}
	for _, p := range global {
		if name, ok := cluster[p]; ok {
			t.Errorf("global port %d collides with %s", p, name)
		}
		if p >= 10000 && p <= constants.IndexPortEnd {
			t.Errorf("global port %d is inside the tenant/index range", p)
		}
		if p >= constants.ChainP2PPort && p <= constants.ChainPrometheusPort {
			t.Errorf("global port %d is inside the chain block", p)
		}
	}
	if constants.GlobalIPFSAPIPort != 31107 {
		t.Errorf("GlobalIPFSAPIPort = %d, want 31107", constants.GlobalIPFSAPIPort)
	}
	if constants.GlobalRelayMetricsPort != 31110 {
		t.Errorf("GlobalRelayMetricsPort = %d, want 31110", constants.GlobalRelayMetricsPort)
	}
}
