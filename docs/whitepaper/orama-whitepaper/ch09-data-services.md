# Data services

> **At a glance.**
>
> - **What:** four tenant-facing data services behind the namespace gateway: a per-namespace RQLite plus single-node SQLite files, an Olric cache, IPFS object storage and GossipSub pub/sub. None is shared between tenants at the data layer. Isolation is enforced by the gateway: the credential's namespace, a guard on SQL, an ownership row per object and a namespace prefix on every topic and key.
> - **Key numbers:** three RQLite voters per namespace, reads at `level=weak` (leader); ORM body 4 MiB, 100 operations per transaction, 69 reserved table names; cache key 255 bytes, entry under 1 MiB, 256 MiB LRU per member, 10 s client deadline, no replicas; IPFS replication factor 3, 20 s download budget, pin sweep every 15 min; pub/sub mesh round 15 s, publish confirmed within 10 s, at-most-once.
> - **Code:** `core/pkg/sqlguard/`, `core/pkg/nsbackup/`, `core/pkg/olric/`, `core/pkg/ipfs/`, `core/pkg/gateway/handlers/storage/`, `core/pkg/pubsub/`.

![The tenant database service: two database kinds, one guard, one sealed backup path](../technical-reference/diagrams/ch17-overview.svg)

## One rule behind four services

A namespace owns its processes, so the data services are per-namespace by construction. Two facts complicate that. The namespace RQLite also holds the platform's own per-namespace rows, so tenant SQL and platform rows share one schema. And IPFS is one private swarm per cluster, so blobs from different tenants share a pinset. Most of the work in this chapter is closing those two gaps without a second database or a second swarm.

Every service also refuses honestly rather than degrades: a cache that is down answers 503, an unpin that cannot decide does not unpin, and a publish the service did not confirm says it may not have been delivered.

## Databases

**The namespace RQLite** is a three-voter Raft group, one voter per member node. Tenant requests reach it through nine ORM routes under `/v1/rqlite/`. Reads use `level=weak`, which goes to the leader: with `level=none` a function that inserted and then selected could read the pre-write snapshot.

Transactions are the usual trap. The driver's `Begin` and `Commit` are no-ops, so `transaction` posts all writes in one request to RQLite's `/db/execute?transaction`, at most 100 operations. Writes run first as one atomic batch; any `query` operations run afterwards as ordinary reads. A query inside a transaction therefore never sees the transaction's state and cannot feed a later write. A rolled-back batch answers 409 with the failing index; a lost leader or expired deadline answers 503, because whether the writes landed is then unknown.

**The SQL guard.** Tenant SQL runs with the rights of the connection that reads the platform's tables, so the statement is the only enforcement point. `sqlguard.Check` does not parse SQL. It tokenizes, discards comments, and refuses `CREATE TRIGGER`, a second statement, `ATTACH`, `DETACH`, `PRAGMA` and `VACUUM` as first words, and any of 69 reserved names in any role: as a table, column, alias, parameter or string literal. The guard does not track roles because every attempt to track which positions SQLite reads as a table name missed one. A test uses SQLite itself as the oracle, generating every separator byte against six statement templates and failing if SQLite accepts anything the guard lets through.

**Tenant SQLite files.** An application that wants an embedded file gets one SQLite database on one node, the home node recorded in `namespace_sqlite_databases`. A request landing elsewhere is forwarded over the overlay once (a forwarded request is never forwarded again, so a wrong registry row cannot loop), and a lookup that fails reads as 421 or 502, never as a fallback to the local disk, which would be a different database. The engine refuses `ATTACH` by setting its attached-database limit to 0, and `PRAGMA` is an allowlist of 16 read-only forms, because a denylist missed `hard_heap_limit`, which lets one tenant fail every other tenant's queries on the gateway. The limit: the file exists on one disk, and nothing reassigns it if the home node is lost.

**Backup is sealed to a key the owner holds.** A namespace backup is the RQLite snapshot, the pin list and the namespace's secrets in plaintext, sealed with `nacl/box` `SealAnonymous` to the owner's X25519 public key. The cluster holds only the public key and cannot open what it wrote. Because the destination's encryption root differs, restore runs in two halves: on the owner's machine the secrets are re-sealed to a per-namespace restore key the destination derives from its root, each bound to its own row; on the gateway, every check that can refuse, including one of the image for triggers and views over platform tables, runs before the first write.

## Cache

The cache is Olric v0.7.4 as independent rings. Each namespace has a private three-member ring on its three nodes; one more ring spans the fleet for platform use. Nothing is replicated and nothing touches disk, so a restarted member comes back empty and a cache user sees misses.

Two decisions came from incidents. First, all of a tenant's cache dmaps fold into one Olric DMap per namespace, with the dmap name as a length-prefixed key prefix. Olric bounds memory per DMap and tenants choose names, so a DMap per name left a ring with no bound: eight dmaps at the 256 MiB LRU limit already reach the unit's 2 GB ceiling, and the kernel kills the process with every key in it. The price is that the dmap name shares the 255-byte key limit.

Second, the socket deadline is the only timeout that works. The library does not apply a request context to the connection, so against a member that accepts and never answers, a call waited out the read timeout twice. The client sets a 10 s read and write deadline and no retries. A supervisor probes with a real write-read-delete every 10 s and drops the client after three failures, so handlers answer a clean 503 instead of transport errors behind a green health check.

On trust: the gateway-to-Olric hop has no authentication, and memberlist gossip is neither encrypted nor authenticated by Olric. The YAML loader has no secret-key field, so the protection is the WireGuard overlay. A process with an overlay source address can read any namespace's cache.

## Storage

Every node runs a Kubo daemon on a private swarm and an IPFS Cluster peer with CRDT consensus; gateways talk to the pair on their own node. Content is replicated three ways. IPFS alone fails a multi-tenant platform in five ways, and the gateway closes each.

**One pin per CID.** If tenants A and B hold the same bytes and A unpins, the pin vanishes for B. The reference count therefore lives in the cluster registry, written only by gateway handlers, since a namespace's own database sees only its own rows. A release deletes its row first and counts second. The registry is linearizable, so of any set of concurrent releases the last delete counts zero and no release can count zero while another reference is recorded. Only a release that counts zero may remove the shared pin, after which the gateway recounts, and restores the pin if a holder appeared in between. An unpin that cannot decide does not unpin: a leaked pin is recoverable, a deleted blob is not.

**No tenants in IPFS.** Anyone who knows a CID can read the block, so an ownership row per CID and namespace is checked before IPFS is asked anything, and status for a foreign CID answers exactly like a CID nobody pinned. Private blobs are sealed with AES-256-GCM under a cluster-wide key before reaching Kubo, which protects the repository on disk but does not separate tenants.

**Local APIs are full control of the node.** Kubo's RPC takes a bearer token and the cluster REST API basic auth, both derived from the cluster secret. IPFS Cluster cannot send the bearer, so a proxy on loopback adds it, and asks the kernel through netlink which uid owns each dialling socket and refuses any but its own. A tenant deployment may open loopback connections, so loopback alone is not a lock.

**Unpinning does not delete,** so `?immediate=true` fans out a verified eviction to every node. **Nothing re-allocates,** so a pin sweep every 15 minutes re-issues pins whose peers fall short.

An upload imports locally into one Kubo, records ownership and takes the registry reference, answers, and only then pins in the background. An earlier version pinned everywhere first and narrowed afterwards, which made every peer start fetching content and then cancel it, holding the cluster's pin slots. Downloads try local blocks first, check the pinset so an unpinned CID is an immediate 404, then fetch within a 20 s budget below the proxy's 30 s so a failure is classified rather than cut off.

A relayed fetch is worth having only if the serving node never learns who asked. A fetch capability is an HMAC token naming one CID of one namespace until a deadline, with the revocation tag sealed per token so two tokens of one device cannot be joined. It requires a device-bound session to mint and refuses any credential beside it.

## Pub/sub

Each node runs one GossipSub service shared by all tenants on it. Gateways reach it over a unix socket whose peers are checked with `SO_PEERCRED`, and it listens for libp2p only on the node's WireGuard address. The namespace is the first segment of the wire topic (`namespace.topic`); namespace names contain no dot, so the first dot always ends the namespace.

No configuration names another node's service. Each index gateway registers its service in the registry every 15 s and reads the registered peers, sorted by peer id. It dials the peers after it on the id ring and allows connections only from those before it. The two-list design makes any dial succeed, since if A dials B then B is among A's first 256 successors and A among B's first 256 predecessors. A gate admits only overlay addresses and peer ids named in the latest round.

Publishing waits for nothing from the mesh. An earlier version polled for topic peers for up to 2 s before each publish and charged every publish that cost. Flood publish sends to every connected peer known to be subscribed, so the gateway answers 200 once the local service holds the message; the answer means local acceptance, not delivery. Delivery is at most once with no replay, and a subscription must propagate before a remote publisher reaches it.

Trust is coarser than at the gateway. A peer that passes the gate is a cluster node, and the `namespace.` prefix is a naming convention, not an access control, so a node in the mesh can publish to any tenant's topic.

## The main limit

The most important limit sits in pub/sub: when the service restarts, the gateway's event streams end and are not re-established for handlers already attached, so subscriber sockets and trigger subscriptions on that node stay silent until recreated.
