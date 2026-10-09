# System shape and one request

> **At a glance.**
>
> - **What:** the vocabulary every other part of the system shares (planes, one port plan, one overlay, nested deadlines, three error shapes), followed by a trace of one tenant request, `POST /v1/cache/put`, from an SDK on a laptop to a key in the tenant's Olric ring and back.
> - **Key numbers:** node services on 10100 to 10199, tenants on 10000 to 10099 in 5-port blocks, deployments on 10200 to 19999, global layer on 31000 to 31099; edge 53, 80, 443, 51820/udp; overlay `10.0.0.0/24`; DNS TTL 60 s; hop MAC skew 60 s; namespace proxy timeout 30 s (300 s on long routes); breaker opens after 5 failures for 30 s; deployment write deadline 390 s.
> - **Code:** `core/pkg/constants/`, `core/pkg/gateway/middleware.go:withMiddleware`, `core/pkg/gateway/handlers/cache/set_handler.go:SetHandler`.

![One request across client, DNS, Caddy, two gateways, the registry and a cache ring](../technical-reference/diagrams/ch03-request.svg)

## Why a shared foundation

A node runs about twenty kinds of systemd unit, written by one binary, read by another and probed by a third, and all must agree on which port the registry listens on. When that agreement lived in integer literals it broke silently: moving the node's own services into the 10100 block left every tool that addressed a service by literal (failure detector, quorum guard, upgrade health gate) quietly wrong, and one failed unsafe. `core/pkg/constants/no_legacy_ports_test.go:TestNoLegacyPortLiterals` is the scar: it fails the build on any of the five old ports in any non-test Go file.

So the constants package is the one place a port, path, timeout, pinned component version or unit name is defined. It imports nothing from the rest of the codebase, and tests read the other side of each agreement: the unit templates, the deploy script, the source tree.

## The shape

**Planes.** The index plane runs on every cluster node (supervisor, WireGuard, registry RQLite, a cluster Olric ring, IPFS, pub/sub, vault guardian, Caddy, the index gateway; CoreDNS on nameserver nodes). The tenant plane is one private RQLite, Olric and gateway per namespace on three nodes. The global layer is a separate set of machines (or a separate network namespace) running the public chain. Each plane has its own port range.

**Registry.** The index RQLite: one Raft group holding nodes, namespaces, keys, grants, DNS records and deployment rows, replicated to every cluster node.

**Overlay.** The WireGuard network `10.0.0.0/24`. Every node-to-node call travels on it; public addresses carry only SSH, DNS, HTTP(S) and WireGuard.

**Unit naming.** Cluster services are systemd templates named `orama-namespace-<service>@<instance>`, where the instance is the namespace name for tenant services, `index` for the node's own and `nameserver` for CoreDNS. Deployments are `orama-deploy-<runtime>@<namespace>-<name>`.

### The port plan

![Port ranges on one node](../technical-reference/diagrams/ch02-port-plan.svg)

| Range | Owner | Allocation |
|---|---|---|
| 53, 80, 443 | CoreDNS (nameserver nodes), Caddy | fixed |
| 51820/udp | WireGuard | fixed |
| 4001, 4101, 8080 | libp2p host, IPFS swarm, IPFS gateway | fixed |
| 10000 to 10099 | tenant namespaces | 5-port blocks, 20 per node |
| 10100 to 10199 | node services | fixed offsets from a base |
| 10200 to 19999 | app deployments | one port per process, 9,800 per node |
| 31000 to 31099 | global layer | fixed |

The node-services block holds RQLite HTTP and Raft on 10100 and 10101, the index gateway on 10104 and the vault on 10106. Unassigned ports in the block stay out of the deployment range, so a user process cannot bind a service port. Ports derive from a base plus an offset, and tests assert that none collide or leave their block.

### Nested deadlines

A timeout set in the CLI must outlast the handler it waits for, which must outlast the replica call inside it. Otherwise the outer layer reports "timed out" about a change that went through. `core/pkg/constants/timeouts.go` declares the chain innermost first, and a test asserts it nests. For an environment change: replica restart 20 s, home node waiting on one replica 30 s, whole change 60 s, CLI 90 s, gateway hop and server write deadline 120 s. An update or rollback fetches an artifact and waits for a health check, so its chain is longer: replica call 180 s, whole handler 330 s, gateway hop 360 s, entry gateway write deadline 390 s. Long transfers extend a request's own read and write deadlines (5 minutes), because the servers' defaults (60 s read, 120 s write) would cut off a large upload mid-stream.

### Three error shapes

A gateway response carries one of three error bodies by origin: a plain `{"error"}`; a refusal with `error`, `code` and optional `hint` (authentication failures); and an envelope `{"ok": false, "error": {code, message, retryable, ...}}` for routes whose clients switch on a code. The envelope sets `Retry-After` when it sets `retry_after`, so a client unaware of it still backs off. A fourth vocabulary, typed Go errors with a code-to-status map, is used only by the Go client.

## One request

A tenant application holds an API key for the namespace `acme` on a cluster with base domain `example.network`. It calls `client.cache.put("sessions", "user:123", value, {ttl: "1h"})`. The SDK sends `POST https://ns-acme.example.network/v1/cache/put` with a bearer token. The token is a JWT the client got by exchanging its key once, so the key never appears in an access log.

**1. DNS.** The name is not in a zone file. CoreDNS on the nameserver nodes runs a plugin that answers from the registry's `dns_records` table. Provisioning wrote one A row per node running one of the namespace's gateways, TTL 60 s, so the client gets three addresses and the namespace has no single front door. The plugin caches answers for 30 s and serves them stale for up to 24 hours if the registry is unreachable.

**2. Edge.** The client connects to port 443. Caddy holds a wildcard certificate for the base domain, obtained once by DNS-01 and served from a store kept in the registry. It terminates TLS and proxies to `localhost:10104`, the index gateway, after deleting six `X-Internal-Auth-*` request headers: the ones a gateway believes about identity. From here every public request arrives from `127.0.0.1`, so the gateway never treats the source address as evidence and takes the client address from the last `X-Forwarded-For` entry Caddy appended.

**3. Index gateway.** The process on 10104 is the index gateway, one per node. Its mux is built from a table declaring who may call each route; a route without a policy cannot be registered. For this route the table says: credential required, grant `cache:write`. The request passes the middleware chain, outermost first: removal of forged proxy headers, a hop check that deletes any `X-Internal-Auth-*` header without a valid MAC, policy lookup, logging, security headers, the general rate limit (10,000 per minute per client network), CORS, a readiness gate (503 if the schema is behind the binary), and host routing. The host is `ns-acme...` and the route is not pinned to the index gateway, so the request goes to the namespace proxy instead of the gateway's own authentication stack.

**4. The proxy authenticates once.** It verifies the JWT: Ed25519 signature against keys the gateways publish in the registry, expiry, and a revocation list each gateway reloads every 5 seconds and fails closed on. The credential's namespace must equal `acme`. Authentication happens here because only the registry knows whether a key is valid. A missing or mismatched credential is refused before any namespace member is contacted.

**5. Choosing a member.** The proxy reads the namespace's live gateways from the registry (`core/pkg/gateway/namespace_targets.go:namespaceGatewayTargetsQuery`), cached 60 s: members of a `ready` or `degraded` cluster whose row is `running` and whose node is `active`. A registry that does not answer is a retryable 503, never a 404, so a tenant is not told it does not exist. The local member goes first, then the rest ordered by a hash of namespace and credential, so one caller's WebSocket subscribe and publish stay on one member. A per-member circuit breaker opens after 5 consecutive failures for 30 s, then admits one probe.

**6. Signing the hop.** The proxy writes the verified namespace, subject, claims and key scopes into `X-Internal-Auth-*` headers and adds a MAC over method, path and those fields, keyed by HKDF of the cluster secret, with a timestamp the receiver accepts within 60 s. Without a cluster secret it answers 503 rather than send an assertion it cannot back. It posts to the member's WireGuard address with a 30 s budget (300 s for uploads, database moves and function calls). A dial failure before any body byte is read tries the next member; a request that reached a member is never retried elsewhere.

**7. Namespace gateway.** `orama-namespace-gateway@acme` is the same binary running against the tenant's own RQLite, listening on the node's overlay address in the namespace's port block. It accepts public traffic from nobody except an index gateway. The hop check now finds a valid MAC and keeps the headers; authentication rebuilds the identity from them, refusing one forwarded for another namespace; the scope layer requires `cache:write`; the namespace rate limit applies (10,000 per minute by default, tenant-configurable).

**8. Handler.** The cache set handler caps the body at 10 MiB, checks `dmap` and `key`, evaluates any key-level grant (`cache:key=sessions/*` can narrow what the caller writes), folds the key with the dmap name so all of a tenant's dmaps share one Olric DMap under one memory bound, and calls `Put` under a 10 s context. The Olric client sends it to the member owning the key's partition. Olric holds entries in memory only: nothing is replicated and nothing reaches disk. The answer is `200 {"status":"ok"}`.

**9. The way back.** The response retraces the connection. A 502, 503 or 504 from the member counts toward the breaker. The SDK retries on 408, 429, 500, 502, 503 and 504, up to three times with growing delay, honouring `Retry-After` up to 30 s.

The trace touched one registry read (cached afterwards), two gateways, the cluster secret and the tenant's Olric ring, and never the tenant's RQLite, the chain, IPFS or the supervisor.

## What keeps the path true

The request reads state that other processes keep correct, none of them in the request: the A records (written at provisioning and re-asserted by each serving node every 30 s), the wildcard certificate (renewed through the registry's store), namespace member rows (maintained by the tenant sweep, see [Cluster state and membership](ch04-cluster-state-and-membership.md)), the three tenant units (restarted by `orama-node` and the sweep, see [The node and the mesh](ch03-the-node-and-the-mesh.md)), the WireGuard mesh (synced every 60 s), and the cluster secret that keys the MAC.

Two constraints decided this shape. A tenant's request must be served by the tenant's own processes, so the public name leads to a gateway that is a different process from the one holding the cluster's keys. And only the registry can say whether a key is valid, so a process with registry access authenticates and everyone else is told, in a way that cannot be forged. The hop MAC is that telling. Other paths (deployments, WebSockets, app host names, node-to-node calls) reuse this stack with other policies; [The gateway](ch06-the-gateway.md) covers them.

The most important limit of the foundation: the port plan is the one fact that cannot grow without a migration like the one that created the 10100 block, and a 100-port tenant range caps a node at 20 namespaces.
