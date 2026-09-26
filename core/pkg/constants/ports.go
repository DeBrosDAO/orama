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

	// IPFSClusterSwarmPort is ipfs-cluster's peer-to-peer listener, bound to
	// the node's WireGuard address: the only IPFS Cluster port peers dial.
	// +14 is the port nodes already listen on — orama-node used to derive it
	// from the REST API port — so the constant moved no live listener.
	IPFSClusterSwarmPort = IndexPortBase + 14 // 10114

	// Edge — not in 10100.
	WireGuardPort = 51820
)
