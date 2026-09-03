package constants

import (
	"net"
	"net/netip"
	"strconv"
)

// IPFS ports that deliberately sit outside the 10100 index block.
//
// The API moved into the block (IPFSAPIPort) but the swarm and gateway did
// not: the swarm port is dialled by remote peers as part of a multiaddr, and
// 8080 is Kubo's conventional gateway port. Both are set by the installer at
// repo-init time, so they belong here rather than as literals at each use.
const (
	// IPFSSwarmPort is the libp2p swarm port of the node's Kubo daemon. Chosen
	// to avoid colliding with the orama node's own libp2p host on 4001.
	IPFSSwarmPort = 4101
	// IPFSGatewayPort is Kubo's read-only HTTP gateway.
	IPFSGatewayPort = 8080
)

// hostPortURL builds "http://host:port", bracketing IPv6 hosts correctly.
func hostPortURL(host string, port int) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// LocalRQLiteURL is the index RQLite HTTP API on this node.
func LocalRQLiteURL() string { return hostPortURL("localhost", RQLiteHTTPPort) }

// LocalGatewayURL is the index gateway HTTP API on this node.
func LocalGatewayURL() string { return hostPortURL("localhost", GatewayAPIPort) }

// LocalOlricURL is the index Olric HTTP API on this node.
func LocalOlricURL() string { return hostPortURL("localhost", OlricHTTPPort) }

// LocalIPFSAPIURL is the Kubo HTTP API on this node.
func LocalIPFSAPIURL() string { return hostPortURL("localhost", IPFSAPIPort) }

// LocalIPFSClusterURL is the ipfs-cluster REST API on this node.
func LocalIPFSClusterURL() string { return hostPortURL("localhost", IPFSClusterAPIPort) }

// RQLiteURLFor is the index RQLite HTTP API on a peer, addressed over the
// WireGuard overlay.
func RQLiteURLFor(host string) string { return hostPortURL(host, RQLiteHTTPPort) }

// GatewayURLFor is the index gateway HTTP API on a peer, addressed over the
// WireGuard overlay. These endpoints are firewalled off the public interface,
// so host must be a 10.0.0.x address.
func GatewayURLFor(host string) string { return hostPortURL(host, GatewayAPIPort) }

// OlricAddrFor is the index Olric host:port on a peer (not a URL — the Olric
// client takes bare addresses).
func OlricAddrFor(host string) string {
	return net.JoinHostPort(host, strconv.Itoa(OlricHTTPPort))
}

// RQLiteRaftAddrFor is the index RQLite raft advertise address on a peer.
func RQLiteRaftAddrFor(host string) string {
	return net.JoinHostPort(host, strconv.Itoa(RQLiteRaftPort))
}

// WireGuardSubnet is the internal WireGuard mesh CIDR. Every inter-node
// endpoint — raft, the HTTP API, Olric memberlist, the SFU — is advertised and
// reached on an address inside it; the public interface has those ports closed.
const WireGuardSubnet = "10.0.0.0/24"

// wireGuardOverlay is WireGuardSubnet parsed once. The CIDR is a compile-time
// constant, so a parse failure is a programming error rather than a runtime
// condition.
var wireGuardOverlay = netip.MustParsePrefix(WireGuardSubnet)

// WireGuardOverlay returns the mesh prefix.
func WireGuardOverlay() netip.Prefix { return wireGuardOverlay }
