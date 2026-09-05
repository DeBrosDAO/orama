package constants_test

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The index services are addressed from ~40 call sites. A wrong port here is
// invisible at compile time and only shows up as a connection refused during an
// incident, so pin the exact strings.
func TestLocalURLs(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rqlite", constants.LocalRQLiteURL(), "http://localhost:10100"},
		{"gateway", constants.LocalGatewayURL(), "http://localhost:10104"},
		{"olric", constants.LocalOlricURL(), "http://localhost:10102"},
		{"ipfs api", constants.LocalIPFSAPIURL(), "http://localhost:10107"},
		{"ipfs cluster", constants.LocalIPFSClusterURL(), "http://localhost:10108"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// Peer addresses are built from dns_nodes.internal_ip, which is a WireGuard
// address today but must not silently produce a malformed URL if the overlay
// ever carries IPv6 — an unbracketed IPv6 host makes the URL unparseable.
func TestPeerAddressesBracketIPv6(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rqlite v4", constants.RQLiteURLFor("10.0.0.2"), "http://10.0.0.2:10100"},
		{"gateway v4", constants.GatewayURLFor("10.0.0.2"), "http://10.0.0.2:10104"},
		{"olric v4", constants.OlricAddrFor("10.0.0.2"), "10.0.0.2:10102"},
		{"raft v4", constants.RQLiteRaftAddrFor("10.0.0.2"), "10.0.0.2:10101"},
		{"gateway v6", constants.GatewayURLFor("fd00::2"), "http://[fd00::2]:10104"},
		{"olric v6", constants.OlricAddrFor("fd00::2"), "[fd00::2]:10102"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The index block is contiguous and each service owns exactly one port. A
// duplicate would make two services fight for the same listener at boot.
func TestIndexPortsAreDistinctAndInBlock(t *testing.T) {
	ports := map[int]string{
		constants.RQLiteHTTPPort:      "RQLiteHTTPPort",
		constants.RQLiteRaftPort:      "RQLiteRaftPort",
		constants.OlricHTTPPort:       "OlricHTTPPort",
		constants.OlricMemberlistPort: "OlricMemberlistPort",
		constants.GatewayAPIPort:      "GatewayAPIPort",
		constants.PubsubAPIPort:       "PubsubAPIPort",
		constants.VaultHTTPPort:       "VaultHTTPPort",
		constants.IPFSAPIPort:         "IPFSAPIPort",
		constants.IPFSClusterAPIPort:  "IPFSClusterAPIPort",
		constants.NtfyListenPort:      "NtfyListenPort",
	}
	if len(ports) != 10 {
		t.Fatalf("index ports collide: only %d distinct values for 10 services", len(ports))
	}
	for p, name := range ports {
		if p < constants.IndexPortBase || p >= constants.IndexPortBase+100 {
			t.Errorf("%s = %d is outside the index block %d-%d",
				name, p, constants.IndexPortBase, constants.IndexPortBase+99)
		}
	}
	// Tenant namespaces own 10000-10099; an index port landing there would be
	// handed out twice by the namespace port allocator.
	for p, name := range ports {
		if p < 10100 {
			t.Errorf("%s = %d overlaps the tenant range 10000-10099", name, p)
		}
	}
}

// IPFS swarm and gateway deliberately sit outside the index block: the swarm
// port is dialled by remote peers inside a multiaddr, and 8080 is Kubo's
// conventional gateway port.
func TestIPFSPortsStayOutsideIndexBlock(t *testing.T) {
	if constants.IPFSSwarmPort >= constants.IndexPortBase {
		t.Errorf("IPFSSwarmPort = %d should stay outside the index block", constants.IPFSSwarmPort)
	}
	if constants.IPFSGatewayPort >= constants.IndexPortBase {
		t.Errorf("IPFSGatewayPort = %d should stay outside the index block", constants.IPFSGatewayPort)
	}
	// 4001 is the orama node's own libp2p host; a collision would stop either
	// the node or Kubo from binding.
	if constants.IPFSSwarmPort == 4001 {
		t.Error("IPFSSwarmPort collides with the orama libp2p host port 4001")
	}
}
