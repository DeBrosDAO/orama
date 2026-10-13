# Glossary and ports

> **At a glance.**
>
> - **What:** the 40 terms the rest of the book relies on, in alphabetical order, and the sixteen ports that matter most.
> - **Key numbers:** cluster services live in 10000 to 19999, split into a tenant pool of 10000 to 10099 and an index block of 10100 to 10199; global (chain) services live in 31000 to 31099.
> - **Code:** the port constants are in `core/pkg/constants/` and `core/pkg/namespace/`.

## Glossary

- **Access token.** A 15-minute JWT signed with EdDSA by a gateway, carrying the wallet or key subject, the namespace and optional device and session claims.
- **Anchor.** In the shielded pool, the root of the note-commitment tree against which a spender proves that a note exists.
- **Archived range.** A decided range of 1,000 blocks with at least three attesting archivers and three live ARCHIVE deals; the flag is permanent.
- **Blueprint.** The declarative description of a cluster of services: which services, how many members, which ports. The tenant blueprint has three members and five ports each.
- **Cluster secret.** One 32-byte random value shared by every node of a cluster, from which MAC keys are derived. It proves membership, never which node.
- **Deal.** A paid commitment to store a piece of data for a number of epochs, with a class, replicas, a price and an escrow.
- **Encryption root.** The input keying material from which every stored-ciphertext key is derived; it can be rotated without touching the mesh or IPFS Cluster.
- **Epoch.** A span of the chain closed by the emission module after a minimum time and block count; storage, rewards and settlement run per epoch.
- **Global layer.** The machines and services that run the public chain, storage market and Tor network, separate from any private cluster.
- **Grant.** What a principal may do in one namespace: a role, an optional resource selector and an optional expiry.
- **Guardian.** The `vault-guardian` daemon, one per node, which stores one Shamir share per secret and nothing else.
- **Home node.** The node whose index gateway received a deployment's create; every change to the deployment is serialised through it.
- **Hop.** A request the index gateway has authenticated and forwards to a namespace gateway, with the verified identity in headers and a MAC over them.
- **Hot key.** A secp256k1 account whose key lives on a node and signs that node's routine chain transactions.
- **Index gateway.** The gateway process that runs on every node, serves the control plane and proxies tenant traffic.
- **Index plane.** The node's own services, run on every cluster node under the instance name `index`.
- **Index RQLite.** The cluster registry: one Raft group per cluster holding every node, namespace, key, grant, DNS record and deployment row.
- **Invite.** A single-use token, prefixed `orama1_`, wrapped with the URL and certificate fingerprint of the node that minted it, used to join a cluster.
- **Mesh.** The WireGuard network `10.0.0.0/24` that carries all node-to-node traffic; each node owns one `/32`.
- **Namespace.** The unit of tenancy: a private cluster of an RQLite, an Olric and a gateway on three nodes, plus services a tenant enables.
- **Node.** One machine running `orama-node`, identified everywhere by its libp2p peer id.
- **Nullifier.** A 32-byte tag a shielded spend reveals; a second appearance is a double spend.
- **Operator.** On the chain, an account that signs every message touching its nodes with its own wallet key. In a cluster, a wallet on the operators list.
- **Permission.** `domain:action:resource`, the unit of authority; a credential holds a permission set computed once per request.
- **Piece commitment.** The Merkle root of a stored piece, with its real and padded leaf counts and byte length.
- **Plane.** One of the three groups of units a machine can run: index, tenant, global.
- **Protocol deal.** A storage deal opened by the chain itself, with no client and no escrow, always with three replicas.
- **Provider.** The storage side of a node: a process beside `oramad` that serves pieces and signs proofs with the node's hot key.
- **Reconciler.** A loop that compares what the registry says should run with what runs and repairs the difference. It is level-triggered, not event-driven.
- **Refresh token.** A 32-byte random token valid for 30 days that rotates on every use and starts or continues a session.
- **Role.** On the chain, `VALIDATOR`, `STORAGE`, `RELAY`, `EXIT`, `DIRAUTH` or `ARCHIVER`, active while the node is active and the role bond is posted. For a node, `cluster`, `global` or `both`.
- **Rolling upgrade.** Upgrading a fleet one node at a time, followers first and the Raft leader last, with a readiness gate between steps.
- **Slot.** One replica of a deal, with the piece commitment it must satisfy and the node currently bound to it.
- **SNI router.** An opt-in TCP router that reads only the server name of a TLS ClientHello and forwards the encrypted stream to Caddy or TURN.
- **Stamp.** A proof attached to an internal request in headers: a timestamp plus a MAC or a signature over method, path and audience.
- **Supervisor.** The component inside `orama-node` that retries an ordered graph of components with backoff until each converges.
- **Tombstone.** A row recording that a Raft member was removed on purpose, so reconcilers treat the missing machine as gone for good.
- **Tx gate.** A small HTTP server between a validator's onion service and the chain's REST API that forwards three calls and refuses the rest.
- **Vault.** The distributed store of small encrypted secrets built on Shamir sharing over GF(2^8), with one guardian per node.
- **Workload credential.** A one-hour JWT with subject `app:NAMESPACE/NAME`, minted for a deployment so that it calls its namespace as itself.

## Ports

Index and tenant services listen on WireGuard addresses unless noted. Chain services start at 31000. [System shape and one request](ch02-system-shape-and-one-request.md) explains the layout.

| Port | Service | Notes |
|---|---|---|
| 51820 | WireGuard | UDP, public edge |
| 80, 443 | Caddy | HTTP and HTTPS edge |
| 53 | CoreDNS | on nameserver nodes |
| 4001 | node libp2p host | dialled over the overlay |
| 10100 | index RQLite HTTP | Raft is 10101 |
| 10102 | index Olric HTTP | memberlist is 10103 |
| 10104 | index gateway API | pub/sub is 10105, vault guardian 10106 |
| 10107 | node IPFS API | IPFS Cluster API is 10108 |
| 10000 to 10099 | tenant namespace block | 5 ports per namespace |
| 10200 to 19999 | deployments | first port a user process may bind is 10200 |
| 3478, 5349 | TURN and TURNS | relay ports 49152 to 65535 |
| 20000 to 29999 | SFU media | 500 ports per namespace |
| 31000 to 31004 | chain | P2P, RPC (loopback), gRPC, REST, Prometheus (loopback) |
| 31010 to 31012 | public Kubo | swarm, RPC (loopback), gateway (loopback) |
| 31013, 31015 | storage provider, chain indexer | the indexer is loopback only |
| 31020 to 31022 | Tor | relay ORPort, directory authority DirPort, validator tx gate |
