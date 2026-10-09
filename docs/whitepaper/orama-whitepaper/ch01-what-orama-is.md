# What Orama is

> **At a glance.**
>
> - **What:** a platform that gives an application the usual backend building blocks (SQL database, cache, object storage, pub/sub, serverless functions, app hosting, push, WebRTC, domains with certificates, a secret vault) on machines that an operator runs. Each tenant, called a namespace, gets its own small cluster instead of a slice of a shared one. A separate global layer runs a public chain, a storage market and a Tor network beside it.
> - **Key numbers:** version 0.3.0 (`/VERSION`); overlay `10.0.0.0/24`, so at most 254 nodes; a namespace has 3 members (1 on a one-node fleet; a two-node fleet refuses); up to 20 namespaces per node; port ranges 10000 to 10099 (tenants), 10100 to 10199 (node services), 31000 to 31099 (global layer).
> - **Code:** the repository root: `core/` (node, gateway, CLI), `chain/` (ledger and its services), `vault/` (guardian daemon), `e2e/` (fleet tests).

![Orama at a glance: clients, one cluster, the global layer](../technical-reference/diagrams/ch01-overview.svg)

## The problem

An application backend is a handful of stateful services plus the glue between them: a database that survives a machine dying, a cache, a place for files, a way to push messages between clients, somewhere to run code, a name with a certificate. A team can rent each from a cloud provider, or run them itself and take on the operations. Orama is an attempt at the second path without the second path's cost: one control layer, written in Go, that installs and converges standard open-source components onto machines you give it, isolates tenants physically rather than by table prefix, and exposes the result through one HTTPS API.

The components are not new. RQLite (SQLite replicated with Raft) is the database, Olric the cache, IPFS and IPFS Cluster the object store, libp2p GossipSub the messaging fabric, wazero the WebAssembly runtime for functions, pion the WebRTC stack, CoreDNS and Caddy the edge, WireGuard the network. What is Orama's own is the layer that decides which of them run where, wires them together, keeps them converged through restarts and upgrades, and puts a single identity and authorization model in front of all of them.

## Three commitments

**No data layer is shared between tenants.** Creating a namespace starts a private RQLite, a private Olric ring and a gateway on each of three nodes, on a block of five ports each. There is no cross-tenant query because there is no shared table to run it against. The cost is process count: three systemd units per member node per namespace, which is why the tenant port range holds 20 blocks per node.

**No internal traffic crosses the public internet.** Raft, Olric gossip, the IPFS swarm, calls between gateways and the chain's peer-to-peer all run on a WireGuard mesh. A node's public face is SSH, DNS on nameserver nodes, HTTP and HTTPS, WireGuard itself and TURN relay ports (`core/pkg/install/firewall.go`). Inside the mesh a service is still not trusted for where it sits: the index gateway forwards a tenant request to a namespace gateway with a MAC over the identity it verified, and a node proves which node it is with a key of its own.

**Identity starts at a wallet signature.** There are no passwords. A wallet signs a message the gateway issued (EIP-4361 or SIWS) and receives a short-lived token; API keys, scoped grants and an app's workload tokens are layered on that. The same wallet, held in the RootWallet agent, signs release archives and unlocks SSH to the nodes. A cluster is its operator's own: the first node's trust anchor is seeded from that wallet, every later node copies it from the node that invited it, and nodes install only builds the wallet signed. There is no vendor key.

## Three planes

Everything the software runs belongs to one of three planes.

| Plane | Runs on | Contents |
|---|---|---|
| Index | every cluster node | WireGuard, the supervisor `orama-node`, the registry RQLite, a cluster-wide Olric ring, IPFS and IPFS Cluster, pub/sub, the vault guardian, Caddy, ntfy, a Tor client, the index gateway; CoreDNS on nameserver nodes |
| Tenant | three nodes per namespace | one RQLite, one Olric, one gateway per member; an SFU and a TURN relay when WebRTC is on; app deployments as their own units |
| Global | separate machines, or a separate network namespace on a cluster node | the public chain `oramad` and the services beside it: storage provider, archiver, indexer, repair delegate, Tor directory authorities, relays, onion services |

The registry is the pivot of the first two planes. The index RQLite holds every node, namespace, key, grant, DNS record and deployment row, one Raft group per cluster. Everything else reads it and converges toward it: the supervisor on each node starts what the registry says the node should run, a reconciler sweeps every 60 seconds to repair drift, and CoreDNS serves its zone straight from a table in it.

## The software

Most of a node is one Go codebase, the core directory. `orama-node` is the supervisor that brings the rest up; the same `gateway` binary runs as the index gateway and as every namespace gateway; `orama-privhelper` is the only root process the unprivileged daemons talk to. The chain directory is a separate Go module: a Cosmos SDK chain on CometBFT and its services, not imported by the node. The vault directory is a Zig daemon (the guardian) that holds one Shamir share per secret. The caddy directory holds two Caddy modules (a DNS-01 provider and a certificate store, both backed by the cluster). The TypeScript SDKs, the JSON fixtures that Go, TypeScript and the live fleet all read, and the Go fleet suite (three fresh servers, the real CLI, every feature) complete the tree.

Two kinds of people use it. A tenant runs a namespace: deploys apps and functions, uses the database and the cache. An operator runs nodes: builds and signs releases, installs and upgrades machines, watches the fleet. Both use the `orama` command, a single binary that calls gateway routes with a short-lived bearer and reaches machines over SSH with keys held in the RootWallet agent. Programs use the TypeScript SDK, the Go client or plain HTTP. There is no dashboard and no Orama MCP server.

Every request from either arrives the same way: at a name under the cluster's base domain, resolved by the cluster's own DNS, terminated by the node's Caddy and handled by a gateway. [System shape and one request](ch02-system-shape-and-one-request.md) follows one through all of it.

## What it is not

- **Not a chain in the request path.** Hosting, databases, functions and storage do not settle on the ledger. The chain is installed separately, by `orama global install`; a tenant request never touches it.
- **Not a defense against a hostile host.** Anyone who can read the memory of a running node can read what it processes. Secrets are sealed at rest and in transit, not in use.
- **Not highly available at one or two nodes.** A one-node fleet provisions single-member namespaces for evaluation, and losing its disk loses them. A two-node fleet refuses to provision, because a two-member Raft group survives the loss of neither member.
- **Bounded at scale by its design.** One cluster is capped by its overlay at 254 addresses, and its registry is a single Raft group.

## Reading map

The book follows the order in which a machine becomes useful. Read the first two chapters in order; after that the chapters stand alone.

| If you want to | Read |
|---|---|
| see the system's shape and follow one request through it | [System shape and one request](ch02-system-shape-and-one-request.md) |
| understand how a node starts, holds privilege and joins the mesh | [The node and the mesh](ch03-the-node-and-the-mesh.md) |
| understand the registry, failure detection and recovery | [Cluster state and membership](ch04-cluster-state-and-membership.md) |
| understand tenancy and how apps run | [Namespaces and deployments](ch05-namespaces-and-deployments.md) |
| follow what a tenant request goes through | [The gateway](ch06-the-gateway.md), then [Identity, authorization and trust](ch07-identity-authorization-and-trust.md) |
| see how secrets and keys are held | [Secrets, keys and the vault](ch08-secrets-keys-and-the-vault.md) |
| use a tenant service | [Data services](ch09-data-services.md), [Compute and realtime](ch10-compute-and-realtime.md) |
| understand names, certificates and ingress | [The edge](ch11-the-edge.md) |
| operate a fleet | [Shipping and operating](ch12-shipping-and-operating.md), [The CLI and SDKs](ch13-cli-and-sdks.md) |
| understand the public layer | [Global nodes](ch14-global-nodes.md), [Anonymity and Tor](ch15-anonymity-and-tor.md), [The chain](ch16-the-chain.md), and the chapters on economics, storage, governance and relay rewards after it |
| find what is incomplete, or look up a term or port | [Known limits](ch21-known-limits.md), [Glossary and ports](ch22-glossary-and-ports.md) |

Every chapter is held to the code. Where an older document disagrees, the book follows the code.
