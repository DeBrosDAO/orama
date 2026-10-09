# Port map

> **At a glance.**
>
> - **Generated** from the port constants in core/pkg/constants, core/pkg/namespace, core/pkg/deployments, core/pkg/turn, core/pkg/sfu by `make whitepaper-gen`. Do not edit by hand: the gate fails when this file and the code disagree.

Every integer constant whose name marks it as a port, a port range bound or a port block size, sorted by value within each package. Chapter 2 explains the layout.

## pkg/constants

| Value | Constant | Meaning |
|---|---|---|
| 4001 | `core/pkg/constants/urls.go:NodeLibP2PPort` | NodeLibP2PPort is the orama node's own libp2p host, which bootstrap peers dial over the WireGuard overlay. |
| 4101 | `core/pkg/constants/urls.go:IPFSSwarmPort` | IPFSSwarmPort is the libp2p swarm port of the node's Kubo daemon. Chosen to avoid colliding with the orama node's own libp2p host on 4001. |
| 8080 | `core/pkg/constants/urls.go:IPFSGatewayPort` | IPFSGatewayPort is Kubo's read-only HTTP gateway. |
| 9050 | `core/pkg/constants/tor.go:TorSOCKSPort` | TorSOCKSPort is Tor's conventional client SOCKS port. It is an edge port, outside the 10100 index block. |
| 9052 | `core/pkg/constants/tor.go:TorNetSOCKSPort` | TorNetSOCKSPort is the SOCKS port a client of the Orama network binds on loopback. It is an edge port beside TorSOCKSPort, outside every block. (9051, the retired Anyone ControlPort, is not reused.) |
| 9800 | `core/pkg/constants/capacity.go:MaxPortsPerNode` | 10200–19999, above the index block |
| 10100 | `core/pkg/constants/ports.go:IndexPortBase` |  |
| 10100 | `core/pkg/constants/ports.go:RQLiteHTTPPort` |  |
| 10101 | `core/pkg/constants/ports.go:RQLiteRaftPort` |  |
| 10102 | `core/pkg/constants/ports.go:OlricHTTPPort` |  |
| 10103 | `core/pkg/constants/ports.go:OlricMemberlistPort` |  |
| 10104 | `core/pkg/constants/ports.go:GatewayAPIPort` |  |
| 10105 | `core/pkg/constants/ports.go:PubsubAPIPort` |  |
| 10106 | `core/pkg/constants/ports.go:VaultHTTPPort` |  |
| 10107 | `core/pkg/constants/ports.go:IPFSAPIPort` |  |
| 10108 | `core/pkg/constants/ports.go:IPFSClusterAPIPort` |  |
| 10109 | `core/pkg/constants/ports.go:NtfyListenPort` |  |
| 10110 | `core/pkg/constants/ports.go:IPFSClusterKuboProxyPort` | IPFSClusterKuboProxyPort is where the cluster unit's bearer proxy listens on 127.0.0.1 for ipfs-cluster's connector (ipfs.ServeCluster). It is TCP, not a unix socket: ipfs-cluster v1.1.6 dials a /unix address with a transport that ignores request cancellation, which disables pin_timeout, unpin_timeout and ipfs_request_timeout. |
| 10114 | `core/pkg/constants/ports.go:IPFSClusterSwarmPort` | IPFSClusterSwarmPort is ipfs-cluster's peer-to-peer listener, bound to the node's WireGuard address: the only IPFS Cluster port peers dial. +14 is the port nodes already listen on — orama-node used to derive it from the REST API port — so the constant moved no live listener. |
| 10199 | `core/pkg/constants/ports.go:IndexPortEnd` | IndexPortEnd is the last port of the index block. Deployment allocators start at the next port, so a user process cannot bind rqlite, Olric, the gateway, or the cluster swarm. |
| 31000 | `core/pkg/constants/chain.go:ChainP2PPort` | ChainP2PPort is CometBFT's peer-to-peer listener, on the WireGuard address. |
| 31000 | `core/pkg/constants/global.go:GlobalPortBase` | GlobalPortBase is the first port of the block. GlobalPortEnd is the last. |
| 31001 | `core/pkg/constants/chain.go:ChainRPCPort` | ChainRPCPort is CometBFT's JSON-RPC server, on 127.0.0.1 only. |
| 31002 | `core/pkg/constants/chain.go:ChainGRPCPort` | ChainGRPCPort is the Cosmos SDK gRPC server. |
| 31003 | `core/pkg/constants/chain.go:ChainAPIPort` | ChainAPIPort is the Cosmos SDK REST API. |
| 31004 | `core/pkg/constants/chain.go:ChainPrometheusPort` | ChainPrometheusPort is CometBFT's Prometheus metrics listener, on 127.0.0.1. |
| 31010 | `core/pkg/constants/global.go:GlobalIPFSSwarmPort` | GlobalIPFSSwarmPort is the public Kubo swarm (TCP and QUIC). |
| 31011 | `core/pkg/constants/global.go:GlobalIPFSAPIPort` | GlobalIPFSAPIPort is the public Kubo RPC, on 127.0.0.1 (the namespace address on a co-located machine), token-gated. |
| 31012 | `core/pkg/constants/global.go:GlobalIPFSGatewayPort` | GlobalIPFSGatewayPort is Kubo's HTTP gateway, on 127.0.0.1. |
| 31013 | `core/pkg/constants/global.go:GlobalProviderPort` | GlobalProviderPort is the storage provider's upload and retrieval HTTP. |
| 31015 | `core/pkg/constants/global.go:GlobalIndexerPort` | GlobalIndexerPort is the chain indexer's read API (orama-global indexer), on 127.0.0.1 only. The gateway proxies /v1/chain/index/ to it. |
| 31020 | `core/pkg/constants/global.go:GlobalTorORPort` | GlobalTorORPort is the relay's ORPort. |
| 31021 | `core/pkg/constants/global.go:GlobalTorDirPort` | GlobalTorDirPort is a dirauth's DirPort. |
| 31022 | `core/pkg/constants/global.go:GlobalTxGatePort` | GlobalTxGatePort is the validator tx gate (orama global txgate), on 127.0.0.1 (the namespace's own loopback when co-located). The validator onion service forwards to it, and only it: nothing else of the chain is reachable through the onion. |
| 31099 | `core/pkg/constants/global.go:GlobalPortEnd` |  |
| 51820 | `core/pkg/constants/ports.go:WireGuardPort` | Edge — not in 10100. |

## pkg/deployments

| Value | Constant | Meaning |
|---|---|---|
| 10000 | `core/pkg/deployments/types.go:MinPort` | Minimum allocatable port |
| 10000 | `core/pkg/deployments/types.go:ReservedMinPort` | Tenant namespace block |
| 10199 | `core/pkg/deployments/types.go:ReservedMaxPort` | Through the index block (constants.IndexPortEnd) |
| 10200 | `core/pkg/deployments/types.go:UserMinPort` | First port a deployment may bind |
| 19999 | `core/pkg/deployments/types.go:MaxPort` | Maximum allocatable port |

## pkg/namespace

| Value | Constant | Meaning |
|---|---|---|
| 5 | `core/pkg/namespace/types.go:PortsPerNamespace` | PortsPerNamespace is the tenant-default port block size (rqlite+olric+gateway). Must equal BlueprintTenant().PortNeedCount(). Other blueprints may use fewer. RQLite HTTP (0), RQLite Raft (1), Olric HTTP (2), Olric Memberlist (3), Gateway HTTP (4) |
| 53 | `core/pkg/namespace/types.go:NameserverDNSPort` | NameserverDNSPort is CoreDNS on the nameserver blueprint. Edge; not 10100. |
| 80 | `core/pkg/namespace/types.go:IndexCaddyHTTPPort` |  |
| 443 | `core/pkg/namespace/types.go:IndexCaddyHTTPSPort` |  |
| 500 | `core/pkg/namespace/types.go:SFUMediaPortsPerNamespace` |  |
| 800 | `core/pkg/namespace/types.go:TURNRelayPortsPerNamespace` |  |
| 3478 | `core/pkg/namespace/types.go:TURNDefaultPort` | TURN listen ports (standard) |
| 5349 | `core/pkg/namespace/types.go:TURNSPort` | TURNS (TURN over TLS on TCP) |
| 9050 | `core/pkg/namespace/types.go:IndexTorSOCKSPort` |  |
| 10000 | `core/pkg/namespace/types.go:NamespacePortRangeStart` | NamespacePortRangeStart is the beginning of the reserved port range for namespace services |
| 10099 | `core/pkg/namespace/types.go:NamespacePortRangeEnd` | NamespacePortRangeEnd is the end of the reserved port range for namespace services |
| 10100 | `core/pkg/namespace/types.go:IndexRQLiteHTTPPort` | Index internals occupy 10100–10199. Do not place these in the tenant pool (10000–10099). Edge ports stay outside this block. |
| 10101 | `core/pkg/namespace/types.go:IndexRQLiteRaftPort` |  |
| 10102 | `core/pkg/namespace/types.go:IndexOlricHTTPPort` |  |
| 10103 | `core/pkg/namespace/types.go:IndexOlricMemberlistPort` |  |
| 10104 | `core/pkg/namespace/types.go:IndexGatewayHTTPPort` |  |
| 10105 | `core/pkg/namespace/types.go:IndexPubsubPort` |  |
| 10106 | `core/pkg/namespace/types.go:IndexVaultPort` |  |
| 10107 | `core/pkg/namespace/types.go:IndexIPFSAPIPort` |  |
| 10108 | `core/pkg/namespace/types.go:IndexIPFSClusterAPIPort` |  |
| 10109 | `core/pkg/namespace/types.go:IndexNtfyPort` |  |
| 20000 | `core/pkg/namespace/types.go:SFUMediaPortRangeStart` | SFU media port range: 20000-29999 Each namespace gets a 500-port sub-range for RTP media |
| 29999 | `core/pkg/namespace/types.go:SFUMediaPortRangeEnd` |  |
| 30000 | `core/pkg/namespace/types.go:SFUSignalingPortRangeStart` | SFU signaling ports: 30000-30099 Each namespace gets 1 signaling port per node |
| 30099 | `core/pkg/namespace/types.go:SFUSignalingPortRangeEnd` |  |
| 49152 | `core/pkg/namespace/types.go:TURNRelayPortRangeStart` | TURN relay port range: 49152-65535 Each namespace gets an 800-port sub-range for TURN relay |
| 51820 | `core/pkg/namespace/types.go:IndexWireGuardPort` | Host-stack edge / singleton ports. Not in 10100. |
| 65535 | `core/pkg/namespace/types.go:TURNRelayPortRangeEnd` |  |
