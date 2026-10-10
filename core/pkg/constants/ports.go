package constants

// Index internals live in 10100–10199. Edge ports (53, 80, 443, 51820, 9050)
// stay outside this block. Tenant namespaces stay in 10000–10099.
const (
	IndexPortBase = 10100

	RQLiteHTTPPort      = IndexPortBase + 0 // 10100
	RQLiteRaftPort      = IndexPortBase + 1 // 10101
	OlricHTTPPort       = IndexPortBase + 2 // 10102
	OlricMemberlistPort = IndexPortBase + 3 // 10103
	GatewayAPIPort      = IndexPortBase + 4 // 10104
	PubsubAPIPort       = IndexPortBase + 5 // 10105
	VaultHTTPPort       = IndexPortBase + 6 // 10106
	IPFSAPIPort         = IndexPortBase + 7 // 10107
	IPFSClusterAPIPort  = IndexPortBase + 8 // 10108
	NtfyListenPort      = IndexPortBase + 9 // 10109

	// IPFSClusterKuboProxyPort is where the cluster unit's bearer proxy
	// listens on 127.0.0.1 for ipfs-cluster's connector (ipfs.ServeCluster).
	// It is TCP, not a unix socket: ipfs-cluster v1.1.6 dials a /unix address
	// with a transport that ignores request cancellation, which disables
	// pin_timeout, unpin_timeout and ipfs_request_timeout.
	IPFSClusterKuboProxyPort = IndexPortBase + 10 // 10110

	// IPFSClusterSwarmPort is ipfs-cluster's peer-to-peer listener, bound to
	// the node's WireGuard address: the only IPFS Cluster port peers dial.
	// +14 is the port nodes already listen on — orama-node used to derive it
	// from the REST API port — so the constant moved no live listener.
	IPFSClusterSwarmPort = IndexPortBase + 14 // 10114

	// IndexPortEnd is the last port of the index block. Deployment allocators
	// start at the next port, so a user process cannot bind rqlite, Olric,
	// the gateway, or the cluster swarm.
	IndexPortEnd = IndexPortBase + 99 // 10199

	// Edge — not in 10100.
	WireGuardPort = 51820
	// DNSPort is where a nameserver node's CoreDNS answers, on every address.
	DNSPort = 53
)
