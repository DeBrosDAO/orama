# Glossary

> **At a glance.**
>
> - **Hand-written** from the vocabulary the chapters define. Each entry says what the word means in Orama and names the chapter that defines it; the chapter is the authority, and where an entry and a chapter disagree the chapter, which is checked against the code, wins.

Terms are in alphabetical order. Names of code elements, commands and tables appear in code spans and are explained where the chapter uses them.

| Term | Meaning | Chapter |
|---|---|---|
| Access token | A 15-minute JWT signed with EdDSA by a gateway, carrying the wallet or key subject, the namespace and optional device and session claims. | [13. Identity](../vol1/13-identity.md) |
| Admission | A row a namespace's own function writes to let a user into a WebRTC room for a bounded time; the gateway checks it before signing a join ticket. | [23. WebRTC](../vol1/23-webrtc.md) |
| Alert | A derived finding in the cluster snapshot, with severity, subsystem and node. Alerts are computed from reports and never stored. | [32. Observability](../vol1/32-observability.md) |
| Anchor (rootfs) | The root-owned directory a `rootfs.Root` walks down from, for example `/opt/orama` during install and upgrade. | [5. Privilege and filesystem trust](../vol1/05-privilege-and-filesystem-trust.md) |
| Anchor (shielded pool) | The root of the note-commitment tree against which a spender proves that a note exists. | [43. The shielded pool](../vol2/43-the-shielded-pool.md) |
| Archive (build) | The gzip tar `orama build` produces: binaries, systemd templates, a manifest listing the SHA-256 of every file, and the wallet signature over the manifest. | [29. Build, signing and release](../vol1/29-build-signing-and-release.md), [30. Install and upgrade](../vol1/30-install-and-upgrade.md) |
| Archived range | A decided range of blocks with at least three attesting archivers and three live ARCHIVE deals; the flag is permanent. | [42. Archive and indexer](../vol2/42-archive-and-indexer.md) |
| Archiver | A node with an active ARCHIVER role bond whose hot key attests, replicates and records block bundles for history. | [42. Archive and indexer](../vol2/42-archive-and-indexer.md) |
| Assignment | The registry's statement that a node belongs to a namespace cluster, recorded in the membership and port-allocation tables. The reconciler converges a node's units to it. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Audience | The libp2p peer id of the node a coordination request is meant for; the verifier uses its own id, so a stamp for one node is useless on another. | [15. Inter-node trust](../vol1/15-inter-node-trust.md) |
| Base domain | The one DNS name a cluster is authoritative for. Every public name is under it: the apex, `ns-NAME` hosts, deployment hosts and TURN hosts. | [24. DNS and nameservers](../vol1/24-dns-and-nameservers.md) |
| Binding (chain) | A signature by a node's service key naming an operator, proving the key's holder accepts that operator. | [37. Global nodes](../vol2/37-global-nodes.md) |
| Binding (contract) | A JSON object in a CosmWasm custom message or query that reaches one Orama module; exactly one module field is set. | [44. Governance and contracts](../vol2/44-governance-and-contracts.md) |
| Blueprint | The declarative description of a cluster of services: which services, how many members, which ports. The tenant blueprint has three members and five ports each. | [9. Namespaces](../vol1/09-namespaces.md) |
| Bootstrap and join | A node with no Raft state and no membership record bootstraps a new cluster; with a record or a join address it joins the existing one. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md), [7. Cluster state](../vol1/07-cluster-state.md) |
| Bucket | One rate limiter with its own rate and burst, keyed by a client network, wallet, namespace or route. | [27. Rate limits and egress controls](../vol1/27-rate-limits-and-egress-controls.md) |
| Bundle (archive) | The file an archiver writes for a range: the blocks and their header hashes. | [42. Archive and indexer](../vol2/42-archive-and-indexer.md) |
| Bundle (shielded) | The Orchard bundle of a shielded transaction: a list of actions, each pairing one spend with one output. | [43. The shielded pool](../vol2/43-the-shielded-pool.md) |
| Caller (privhelper) | A request's sender, reduced to a uid and, for non-root, the systemd unit it runs in. | [5. Privilege and filesystem trust](../vol1/05-privilege-and-filesystem-trust.md) |
| Challenge (auth) | The text a wallet signs to sign in, containing a 256-bit nonce that is spent once. | [13. Identity](../vol1/13-identity.md) |
| Challenge (storage) | For one epoch, node, deal and slot, the leaf index the node must prove it still holds. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Class (deal) | The kind of storage deal: private, public pin or archive, which decides how many piece commitments a deal has and who may read it. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Client network | The identity a rate-limit bucket is charged to: the peer address, or the last forwarded address when the peer is the local reverse proxy; an IPv6 client is its /64. | [27. Rate limits and egress controls](../vol1/27-rate-limits-and-egress-controls.md) |
| Cluster | A set of nodes joined by one WireGuard mesh and one index RQLite. A cluster belongs to its operator. | [1. What Orama is](../vol1/01-what-orama-is.md), [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md), [7. Cluster state](../vol1/07-cluster-state.md) |
| Cluster gateway | Another name for the index gateway: the process that holds the registry connection and monitors the cluster. | [12. Gateway architecture](../vol1/12-gateway-architecture.md), [32. Observability](../vol1/32-observability.md) |
| Cluster registry | See registry. | [7. Cluster state](../vol1/07-cluster-state.md) |
| Cluster secret | One 32-byte random value shared by every node of a cluster, from which MAC keys are derived. It proves membership, never which node. | [15. Inter-node trust](../vol1/15-inter-node-trust.md), [16. Secrets and keys](../vol1/16-secrets-and-keys.md) |
| Co-located node | A machine with role `both`: a cluster node plus a global node whose services run in the separate network namespace `orama-global`. | [37. Global nodes](../vol2/37-global-nodes.md) |
| Collector | A function that fills one section of a node report; all run in parallel and a failing one is recorded without stopping the rest. | [32. Observability](../vol1/32-observability.md) |
| Component (boot) | One unit of the supervisor's start-up graph: a name, dependencies, a reconcile function and an optional health check. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md) |
| Component (health) | One service's health across the cluster (gateway, database, cache, storage, vault, DNS, mesh, chain), with a state from operational to outage. | [32. Observability](../vol1/32-observability.md) |
| Concurrency cap | A counter of things in flight, such as tunnels or streams, as opposed to a bucket that limits a rate. | [27. Rate limits and egress controls](../vol1/27-rate-limits-and-egress-controls.md) |
| Contract fixture | A JSON file under `contracts/` holding one route's request, response and the SDK call that produces it, read by Go, TypeScript and the fleet. | [34. Testing](../vol1/34-testing.md) |
| Coordination stamp | A timestamp and MAC attached to an internal request, covering method, path and body, keyed from the cluster secret. | [15. Inter-node trust](../vol1/15-inter-node-trust.md) |
| Coordinator | The lowest-sorted live member of a namespace, computed independently on each node, which alone performs a namespace-wide repair step. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Credential | What a request carries to identify itself: a wallet JWT, an API key, the JWT exchanged from a key, or a workload token. | [13. Identity](../vol1/13-identity.md), [14. Authorization](../vol1/14-authorization.md) |
| Deal | A paid commitment to store a piece of data for a number of epochs, with a class, replicas, a price and an escrow. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Deployment | One application on the platform, identified by namespace and name: a static site, a Next.js server, a Node.js app or a Go binary. | [11. App deployments](../vol1/11-app-deployments.md) |
| Device | A key pair one installation holds; sessions can be bound to it, and its id is the RFC 7638 thumbprint of the public key. | [13. Identity](../vol1/13-identity.md) |
| DMap | Olric's named map. Orama keeps a namespace's whole cache in one DMap and folds the tenant's dmap name into each key. | [18. Cache](../vol1/18-cache.md) |
| Dynamic route policy | A route policy that depends on the request, for a pattern whose handler dispatches on the rest of the path. | [12. Gateway architecture](../vol1/12-gateway-architecture.md), [14. Authorization](../vol1/14-authorization.md) |
| Encryption root | The input keying material from which every stored-ciphertext key is derived; it can be rotated without touching the mesh or IPFS Cluster. | [16. Secrets and keys](../vol1/16-secrets-and-keys.md) |
| End user | A wallet with no grant that signed in to a namespace whose owner opened sign-in; it gets a session and a small default permission set. | [14. Authorization](../vol1/14-authorization.md) |
| Epoch | A span of the chain closed by the emission module after a minimum time and block count; storage, rewards and settlement run per epoch. | [40. Economics](../vol2/40-economics.md), [41. Storage deals](../vol2/41-storage-deals.md) |
| Exempt traffic | Overlay and same-machine requests that do not draw from the public rate-limit buckets. | [27. Rate limits and egress controls](../vol1/27-rate-limits-and-egress-controls.md) |
| Fetch capability | A bearer token that lets its holder read one CID of one namespace until a deadline, with no account behind it. | [19. Storage](../vol1/19-storage.md) |
| Fleet (e2e) | The three servers one end-to-end run creates, installs and destroys. | [34. Testing](../vol1/34-testing.md) |
| Folded key | The Olric key of a tenant cache entry: the length of the tenant's dmap name, a colon, the name and the tenant's key. | [18. Cache](../vol1/18-cache.md) |
| Gate (book, test) | A command that exits non-zero on a violation, such as `make whitepaper-check` or `make e2e-coverage`. | [34. Testing](../vol1/34-testing.md), [29. Build, signing and release](../vol1/29-build-signing-and-release.md) |
| Gate (rolling upgrade) | The readiness check between two steps of a rolling upgrade, a pure predicate over one observation of the node just upgraded. | [31. Rolling upgrades](../vol1/31-rolling-upgrades.md) |
| Genesis node | The first node of a cluster: it generates every shared secret and takes overlay address `10.0.0.1`. | [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md), [30. Install and upgrade](../vol1/30-install-and-upgrade.md) |
| Global layer | The set of machines and services that run the public chain, storage market and Tor network, separate from any private cluster. | [1. What Orama is](../vol1/01-what-orama-is.md), [37. Global nodes](../vol2/37-global-nodes.md) |
| Global node | A machine with role `global` (or `both`) that runs the chain and the services beside it, with no WireGuard mesh or RQLite. | [37. Global nodes](../vol2/37-global-nodes.md) |
| Glue | The A record that gives a nameserver slot its public address. | [24. DNS and nameservers](../vol1/24-dns-and-nameservers.md) |
| Grant | What a principal may do in one namespace: a role, an optional resource selector, an optional expiry. | [14. Authorization](../vol1/14-authorization.md) |
| Guarded dial | A connection made through a dialer that checks the resolved address against the reserved-address list at connect time. | [27. Rate limits and egress controls](../vol1/27-rate-limits-and-egress-controls.md) |
| Guardian | The `vault-guardian` daemon, one per node, which stores one Shamir share per secret and nothing else. | [28. Vault](../vol1/28-vault.md) |
| Heartbeat (node) | A signed request each node sends to its own gateway every 30 s to refresh its `dns_nodes` row. | [8. Membership and failure detection](../vol1/08-membership-and-failure-detection.md) |
| Home node | The node whose index gateway received a deployment's create; every change to the deployment is serialized through it. | [11. App deployments](../vol1/11-app-deployments.md) |
| Hop | A request the index gateway has authenticated and forwards to a namespace gateway with the verified identity in headers and a MAC over them. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Hot key | A secp256k1 account whose key lives on a node and signs that node's routine chain transactions. | [37. Global nodes](../vol2/37-global-nodes.md), [41. Storage deals](../vol2/41-storage-deals.md) |
| Incarnation | One creation of a namespace name; a namespace deleted and created again gets a new cluster id so stale work cannot touch it. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Index gateway | The gateway process `orama-namespace-gateway@index` that runs on every node, serves the control plane and proxies tenant traffic. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Index plane | The node's own services, run on every cluster node under the instance name `index`. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md), [9. Namespaces](../vol1/09-namespaces.md) |
| Index RQLite | The cluster registry: one Raft group per cluster holding every node, namespace, key, grant, DNS record and deployment row. | [7. Cluster state](../vol1/07-cluster-state.md) |
| Invite | A single-use token, prefixed `orama1_`, wrapped with the URL and certificate fingerprint of the node that minted it, used to join a cluster. | [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md), [30. Install and upgrade](../vol1/30-install-and-upgrade.md) |
| Join ticket | A short-lived signed statement from a namespace gateway to an SFU saying who a socket is and which room it may join. | [23. WebRTC](../vol1/23-webrtc.md) |
| Lifecycle state | A node's own state: joining, active, degraded, draining or maintenance. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md) |
| Lobby | The namespace `default`, which belongs to nobody and issues no keys; a session there reaches only routes that need no permission. | [13. Identity](../vol1/13-identity.md), [14. Authorization](../vol1/14-authorization.md) |
| Local pin | A pin held by one Kubo daemon directly, not managed by IPFS Cluster. | [19. Storage](../vol1/19-storage.md) |
| Lock (TLS) | A named lease with a holder, taken around a certificate order so two nodes do not order the same one. | [25. TLS and certificates](../vol1/25-tls-and-certificates.md) |
| MainGateway (route) | A route policy flag that keeps a route on the index gateway even when the request names an `ns-NAME` host. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Manifest | `manifest.json` in a build archive: version, commit, architecture and the SHA-256 of every file; its signature is what nodes verify. | [29. Build, signing and release](../vol1/29-build-signing-and-release.md) |
| Member | A node that has a membership row for a namespace cluster; a live member is also active in the registry. | [9. Namespaces](../vol1/09-namespaces.md), [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Mesh | The WireGuard network `10.0.0.0/24` that carries all node-to-node traffic. | [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md) |
| Mixed-version window | The interval during which two releases run in one cluster, opened at push and closed when the last node restarts. | [31. Rolling upgrades](../vol1/31-rolling-upgrades.md) |
| Namespace | The unit of tenancy: a private cluster of an RQLite, an Olric and a gateway on three nodes, plus services a tenant enables. | [9. Namespaces](../vol1/09-namespaces.md) |
| Namespace gateway | The gateway process `orama-namespace-gateway@NAME` of one namespace on one member node; it trusts only signed hops from an index gateway. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Namespaced topic | The string `namespace.topic` used for every pub/sub operation, which keeps tenants' topics apart. | [20. Pub/sub](../vol1/20-pubsub.md) |
| Node | One machine running `orama-node`, identified everywhere by its libp2p peer id. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md), [8. Membership and failure detection](../vol1/08-membership-and-failure-detection.md) |
| Node key | A second per-node Ed25519 key, generated on first use, whose public half the cluster records; it proves which node signed a request. | [15. Inter-node trust](../vol1/15-inter-node-trust.md) |
| Node report | The JSON document describing one node at one moment, filled by 17 collectors. | [32. Observability](../vol1/32-observability.md) |
| Nullifier | A 32-byte tag a shielded spend reveals; a second appearance is a double spend. | [43. The shielded pool](../vol2/43-the-shielded-pool.md) |
| Operator (chain) | An account registered in the nodes module that signs every message touching its nodes with its own wallet key. | [37. Global nodes](../vol2/37-global-nodes.md) |
| Operator (cluster) | A wallet on the cluster's operators list, checked in the handlers of operator routes and the raw-database routes. | [14. Authorization](../vol1/14-authorization.md) |
| Orphan | A namespace with state on a node that the registry does not assign to it; the node's sweep tears it down. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Overlay | The address space of the WireGuard mesh; each node owns one `/32` in `10.0.0.0/24`. | [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md) |
| Owed teardown | A stop or teardown a node did not confirm, recorded so it is replayed until it succeeds. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Passthrough | The short list of paths a gateway answers even when it is not ready. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Peer exchange | A libp2p stream protocol over which nodes share the peers and RQLite metadata they know. | [6. The WireGuard mesh](../vol1/06-the-wireguard-mesh.md) |
| Permission | `domain:action:resource`, the unit of authority; a credential holds a permission set computed once per request. | [14. Authorization](../vol1/14-authorization.md) |
| Piece commitment | The Merkle root of a stored piece with its real and padded leaf counts and byte length. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Pin | An entry in the IPFS Cluster pinset naming a CID with equal minimum and maximum replication. | [19. Storage](../vol1/19-storage.md) |
| Plane | One of the three groups of units a machine can run: index, tenant, global. | [1. What Orama is](../vol1/01-what-orama-is.md), [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md) |
| Policy (route) | What a route requires of its caller, declared once in a table; a route without a declared policy cannot be registered. | [12. Gateway architecture](../vol1/12-gateway-architecture.md), [14. Authorization](../vol1/14-authorization.md) |
| Principal | Who holds a grant: a wallet, an API key or an app, stored as a type and identifier. | [14. Authorization](../vol1/14-authorization.md) |
| Privilege helper | `orama-privhelper`, the socket-activated root service that parses each request against an allow-list and authorizes it by the caller's unit. | [5. Privilege and filesystem trust](../vol1/05-privilege-and-filesystem-trust.md) |
| Protocol deal | A storage deal opened by the chain itself with no client and no escrow, always with three replicas, used for archives. | [41. Storage deals](../vol2/41-storage-deals.md), [42. Archive and indexer](../vol2/42-archive-and-indexer.md) |
| Provider (push) | A backend that delivers a notification: APNs, Expo or ntfy. | [22. Push notifications](../vol1/22-push-notifications.md) |
| Provider (storage) | The storage side of a node: a process beside `oramad` that serves pieces and signs proofs with the node's hot key. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Reconciler | A loop that compares what the registry says should run with what runs and repairs the difference, level-triggered rather than event-driven. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md), [8. Membership and failure detection](../vol1/08-membership-and-failure-detection.md) |
| Refresh token | A 32-byte random token valid 30 days that rotates on every use and starts or continues a session. | [13. Identity](../vol1/13-identity.md) |
| Registry | The index RQLite as a reader sees it: the authority on nodes, namespaces, keys, grants and placement. | [7. Cluster state](../vol1/07-cluster-state.md), [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Replica (deployment) | A node that also runs a deployment; the default is one replica beside the home node. | [11. App deployments](../vol1/11-app-deployments.md) |
| Revocation list | The in-memory copy of the table of revoked tokens, sessions and devices, reloaded by each gateway and failing closed. | [13. Identity](../vol1/13-identity.md) |
| Ring (membership) | The nodes whose `last_seen` is recent, sorted by id; each node probes the next three in that order. | [8. Membership and failure detection](../vol1/08-membership-and-failure-detection.md) |
| Ring (Olric) | The set of Olric members that gossip with each other; every namespace has its own and the fleet shares one more. | [18. Cache](../vol1/18-cache.md) |
| Role (authorization) | A named permission set a grant carries, from weakest to the owner. | [14. Authorization](../vol1/14-authorization.md) |
| Role (chain) | `VALIDATOR`, `STORAGE`, `RELAY`, `EXIT`, `DIRAUTH` or `ARCHIVER`, active while the node is active and the role's bond is posted. | [37. Global nodes](../vol2/37-global-nodes.md) |
| Role (node) | `cluster`, `global` or `both`, which picks the supervisor's boot graph. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md), [37. Global nodes](../vol2/37-global-nodes.md) |
| Rolling upgrade | Upgrading a fleet one node at a time, followers first and the Raft leader last, with a readiness gate between steps. | [31. Rolling upgrades](../vol1/31-rolling-upgrades.md) |
| Room | A set of peers in one SFU process, named by a room id of 1 to 128 printable characters. | [23. WebRTC](../vol1/23-webrtc.md) |
| Round robin (DNS) | A name with several A rows, one per node serving it; the plugin returns all active rows. | [24. DNS and nameservers](../vol1/24-dns-and-nameservers.md) |
| Scope word | The legacy storage form of a key's authority, a comma-separated list of words in `api_keys.scopes`, translated to permissions. | [14. Authorization](../vol1/14-authorization.md) |
| Selector | A string on a grant that narrows it to part of a namespace, such as `cache:key=sessions/*`. | [14. Authorization](../vol1/14-authorization.md) |
| SFU | The selective forwarding unit, `orama-namespace-sfu@NAME`, a pion process that forwards media for the rooms it owns. | [23. WebRTC](../vol1/23-webrtc.md) |
| Share (Shamir) | What one guardian stores: a one-byte x coordinate followed by one y byte per envelope byte. | [28. Vault](../vol1/28-vault.md) |
| Signing key (gateway) | An Ed25519 key a gateway signs tokens with, published to the registry so other gateways can verify them. | [13. Identity](../vol1/13-identity.md) |
| Slot (deal) | One replica of a deal, with the piece commitment it must satisfy and the node currently bound to it. | [41. Storage deals](../vol2/41-storage-deals.md) |
| Slot (nameserver) | A row `ns1` to `ns13` that is a nameserver's stable identity, with its holder, address and domain. | [24. DNS and nameservers](../vol1/24-dns-and-nameservers.md) |
| SNI router | `orama-sni-router`, an opt-in TCP router that reads only the server name of a TLS ClientHello and forwards the encrypted stream to Caddy or TURN. | [26. SNI routing and stealth TURN](../vol1/26-sni-routing-and-stealth-turn.md) |
| Stamp | A proof attached to an internal request in headers: a timestamp plus a MAC or a signature over method, path and audience. | [15. Inter-node trust](../vol1/15-inter-node-trust.md) |
| Stealth host | The neutral name `cdn-HASH.BASE` under which a namespace's TURN relay is reached through the SNI router. | [26. SNI routing and stealth TURN](../vol1/26-sni-routing-and-stealth-turn.md) |
| Supervisor | The `boot.Supervisor` inside `orama-node`, which retries an ordered graph of components with backoff until each converges. | [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md) |
| Sweep | One pass of the 60-second tenant reconciler: eight steps in a fixed order, each step's error logged and the next run. | [10. Reconciliation and recovery](../vol1/10-reconciliation-and-recovery.md) |
| Target (proxy) | One live gateway of a namespace: a WireGuard address and a port read from the registry. | [12. Gateway architecture](../vol1/12-gateway-architecture.md) |
| Tenant plane | The per-namespace services: an RQLite, an Olric and a gateway on three nodes, plus any the tenant enables. | [9. Namespaces](../vol1/09-namespaces.md) |
| Tombstone | A row recording that a Raft member was removed on purpose, so reconcilers treat the missing machine as gone for good. | [8. Membership and failure detection](../vol1/08-membership-and-failure-detection.md), [33. Recovery](../vol1/33-recovery.md) |
| Trigger | What starts a function without a caller: a cron row or a pub/sub pattern. | [21. Serverless functions](../vol1/21-serverless.md) |
| Trust anchor | The root-owned list of wallet addresses (`/etc/orama/archive-signers`) whose signature a node requires on every build. | [29. Build, signing and release](../vol1/29-build-signing-and-release.md), [30. Install and upgrade](../vol1/30-install-and-upgrade.md) |
| TURN credential | A username of the form EXPIRY colon NAMESPACE and an HMAC password under the namespace's secret. | [23. WebRTC](../vol1/23-webrtc.md) |
| Tx gate | A small HTTP server between a validator's onion service and the chain's REST API that forwards three calls and refuses the rest. | [38. Anonymity and Tor](../vol2/38-anonymity-and-tor.md) |
| Unit | A systemd unit. Cluster services are template units named `orama-namespace-SERVICE@INSTANCE`. | [2. System shape](../vol1/02-system-shape.md), [4. The node as a supervisor](../vol1/04-the-node-as-a-supervisor.md) |
| Vault | The distributed store of small encrypted secrets built on Shamir sharing over GF(2^8), with one guardian per node. | [28. Vault](../vol1/28-vault.md) |
| Verdict | The one-line answer to whether everything is fine: the worst component state, raised to degraded by any critical alert. | [32. Observability](../vol1/32-observability.md) |
| Wallet | The identity of a person or operator: an EVM or Solana key whose signature signs in, signs builds and unlocks SSH through the RootWallet agent. | [13. Identity](../vol1/13-identity.md), [35. The CLI](../vol1/35-the-cli.md) |
| Workload credential | A one-hour JWT with subject `app:NAMESPACE/NAME`, minted for a deployment so it calls its namespace as itself. | [11. App deployments](../vol1/11-app-deployments.md), [14. Authorization](../vol1/14-authorization.md) |
