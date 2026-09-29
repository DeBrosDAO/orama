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
	// GlobalIndexerPort is the chain indexer's read API (orama-global
	// indexer), on 127.0.0.1 only. The gateway proxies /v1/chain/index/ to it.
	GlobalIndexerPort = 31015
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
	GlobalArchiverUnit = "orama-global-archiver.service"
	GlobalIndexerUnit  = "orama-global-indexer.service"
	GlobalRepairUnit   = "orama-global-repair.service"

	// GlobalStateRoot is the root-owned parent of every global state
	// directory. Root keeps its own files for the global role here (the
	// validator sign floor, a migration's recipient key, quarantined keys),
	// where no service account can write.
	GlobalStateRoot    = "/var/lib/orama-global"
	GlobalIPFSHome     = "/var/lib/orama-global/ipfs"
	GlobalProviderHome = "/var/lib/orama-global/provider"
	GlobalRelayHome    = "/var/lib/orama-global/relay"
	GlobalArchiverHome = "/var/lib/orama-global/archiver"
	GlobalRepairHome   = "/var/lib/orama-global/repair"
	GlobalIndexerHome  = "/var/lib/orama-global/indexer"

	// GlobalNetnsPriorForwardFile, in GlobalStateRoot, holds the value net.ipv4.ip_forward had
	// (0 or 1) before the first `orama global install --colocated` turned it on. Removing the
	// co-located layout puts that value back.
	GlobalNetnsPriorForwardFile = "netns-prior-ip-forward"

	// GlobalBinDir holds the binaries the global units run: root-owned, 0755,
	// outside /opt/orama so the units' tmpfs over /opt/orama does not hide them.
	GlobalBinDir = "/usr/lib/orama-global/bin"

	// GlobalIPFSAPITokenFile is the public Kubo RPC bearer, mode 0640, in GlobalIPFSHome.
	GlobalIPFSAPITokenFile = "api-token"
	// GlobalMonitorFile is the status file a provider or relay writes in its home.
	// The node report reads it. No process in this repo writes it yet.
	GlobalMonitorFile = "monitor.json"
)

// LocalGlobalIPFSAPIURL is the public Kubo RPC on this node.
func LocalGlobalIPFSAPIURL() string { return hostPortURL("127.0.0.1", GlobalIPFSAPIPort) }

// LocalGlobalIndexerURL is the chain indexer's read API on this node.
func LocalGlobalIndexerURL() string { return hostPortURL("127.0.0.1", GlobalIndexerPort) }

// The co-located layout's veth pair (docs/RUN_A_GLOBAL_NODE.md, "Sharing a
// machine with a cluster node"). It lives here so the namespace layout
// (pkg/globalnetns) and the readers of the chain's listeners (the cluster
// gateway, the node report) agree on one address and cannot drift.
const (
	// SystemdUnitDir is where the installers write units, the co-located namespace unit among them.
	// The readers that decide whether the machine is co-located look for that unit here.
	SystemdUnitDir = "/etc/systemd/system"

	// GlobalNetnsHostAddr is the veth end in the root namespace. It is the
	// only source the namespace's firewall lets reach the chain's RPC, REST
	// and the indexer.
	GlobalNetnsHostAddr = "198.18.0.1"
	// GlobalNetnsAddr is the veth end inside the orama-global namespace. On a
	// co-located machine the chain's RPC (31001) and REST API (31003) and the
	// indexer (31015) listen on it instead of on loopback, so the host can
	// reach them and nothing else can: they are not published.
	GlobalNetnsAddr = "198.18.0.2"
)

// ColocatedChainRPCURL is the chain's CometBFT RPC on a co-located machine.
func ColocatedChainRPCURL() string { return hostPortURL(GlobalNetnsAddr, ChainRPCPort) }

// ColocatedChainAPIURL is the chain's REST API on a co-located machine.
func ColocatedChainAPIURL() string { return hostPortURL(GlobalNetnsAddr, ChainAPIPort) }

// ColocatedGlobalIndexerURL is the chain indexer's read API on a co-located machine.
func ColocatedGlobalIndexerURL() string { return hostPortURL(GlobalNetnsAddr, GlobalIndexerPort) }
