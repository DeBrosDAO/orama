package constants

// Global-node listeners live in 31000–31099. That block is not the index
// (10100–10199), the tenant range (10000–10099), the SFU media range
// (20000–30099), Kubo's cluster swarm (4101), the node libp2p port (4001),
// the cluster gateway (8080), Tor's client SOCKS (9050), or TURN
// (49152–65535). CometBFT's stock 26656/26657 are inside the SFU range and
// are not used.
//
// ChainP2PPort through ChainPrometheusPort occupy 31000–31004. The rest of
// this block is the other global services. Loopback listeners are not opened
// in the firewall.
const (
	// GlobalIPFSSwarmPort is the public Kubo swarm (TCP and QUIC).
	GlobalIPFSSwarmPort = 31010
	// GlobalIPFSAPIPort is the public Kubo RPC, on 127.0.0.1, token-gated.
	GlobalIPFSAPIPort = 31011
	// GlobalIPFSGatewayPort is Kubo's HTTP gateway, on 127.0.0.1.
	GlobalIPFSGatewayPort = 31012
	// GlobalProviderPort is the storage provider's upload and retrieval HTTP.
	GlobalProviderPort = 31013
	// GlobalRelayMetricsPort is the relay's metrics listener, on 127.0.0.1.
	// It is not a public service; it stays in this block so it cannot land
	// on a cluster port.
	GlobalRelayMetricsPort = 31014
	// GlobalTorORPort is the relay's ORPort.
	GlobalTorORPort = 31020
	// GlobalTorDirPort is a dirauth's DirPort.
	GlobalTorDirPort = 31021

	// GlobalPortBase is the first port of the block. GlobalPortEnd is the last.
	GlobalPortBase = 31000
	GlobalPortEnd  = 31099
)

// Global unit names and state directories. The cluster installer does not
// enable these units; a global-role install does. core and the unit templates
// share the paths so a report does not probe a directory the unit does not use.
const (
	GlobalIPFSUnit     = "orama-global-ipfs.service"
	GlobalProviderUnit = "orama-global-provider.service"
	GlobalRelayUnit    = "orama-global-relay.service"

	GlobalIPFSHome     = "/var/lib/orama-global/ipfs"
	GlobalProviderHome = "/var/lib/orama-global/provider"
	GlobalRelayHome    = "/var/lib/orama-global/relay"

	// GlobalIPFSAPITokenFile is the public Kubo RPC bearer, mode 0640, in GlobalIPFSHome.
	GlobalIPFSAPITokenFile = "api-token"
	// GlobalMonitorFile is the status file a provider or relay writes in its home.
	// The node report reads it. No process in this repo writes it yet.
	GlobalMonitorFile = "monitor.json"
)

// LocalGlobalIPFSAPIURL is the public Kubo RPC on this node.
func LocalGlobalIPFSAPIURL() string { return hostPortURL("127.0.0.1", GlobalIPFSAPIPort) }
