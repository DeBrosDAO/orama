# Orama Network Architecture

## Overview

Orama Network is a high-performance API Gateway and Reverse Proxy designed for a decentralized ecosystem. It serves as a unified entry point that orchestrates traffic between clients and various backend services.

## Architecture Pattern

**Modular Gateway / Edge Proxy Architecture**

The system follows a clean, layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────┐
│                        Clients                               │
│              (Web, Mobile, CLI, SDKs)                        │
└────────────────────────┬────────────────────────────────────┘
                         │
                         │ HTTPS/WSS
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                   API Gateway (Port 443)                     │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Handlers Layer (HTTP/WebSocket)                     │   │
│  │  - Auth handlers    - Storage handlers               │   │
│  │  - Cache handlers   - PubSub handlers                │   │
│  │  - Serverless       - Database handlers              │   │
│  └──────────────────────┬───────────────────────────────┘   │
│                         │                                    │
│  ┌──────────────────────▼───────────────────────────────┐   │
│  │  Middleware (Security, Auth, Logging)                │   │
│  └──────────────────────┬───────────────────────────────┘   │
│                         │                                    │
│  ┌──────────────────────▼───────────────────────────────┐   │
│  │  Service Coordination (Gateway Core)                 │   │
│  └──────────────────────┬───────────────────────────────┘   │
└─────────────────────────┼────────────────────────────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        │                 │                 │
        ▼                 ▼                 ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   RQLite     │  │    Olric     │  │     IPFS     │
│  (Database)  │  │   (Cache)    │  │  (Storage)   │
│              │  │              │  │              │
│  Port 10100  │  │  Port 10102  │  │  Port 10107  │
└──────────────┘  └──────────────┘  └──────────────┘

        ┌─────────────────┐         ┌──────────────┐
        │  IPFS Cluster   │         │  Serverless  │
        │   (Pinning)     │         │    (WASM)    │
        │                 │         │              │
        │  Port 10108     │         │   In-Process │
        └─────────────────┘         └──────────────┘

        ┌─────────────────┐
        │     Anyone      │
        │  (Anonymity)    │
        │                 │
        │  Port 9050      │
        └─────────────────┘
```

## Node process model

Install enables **only** `orama-node.service`. That process is a supervisor: it does not embed the HTTP gateway or exec `rqlited`. On start it brings up host and control-plane units as `orama-namespace-<driver>@index` (and CoreDNS as `orama-namespace-coredns@nameserver` when the node was installed with `--nameserver`).

| Plane | Membership | Units | Ports |
|---|---|---|---|
| **index** | every node | `orama-namespace-{wireguard,ipfs,ipfs-cluster,ipfs-gc,rqlite,olric,pubsub,gateway,vault,caddy,ntfy,anyone-client}@index`; optional `sni-router@index` | internals `10100–10109`; edge `80`/`443`/`51820`/`9050` |
| **nameserver** | this node, if `--nameserver` | `orama-namespace-coredns@nameserver` | `:53` |
| **tenant** | N members chosen at provision | `orama-namespace-{rqlite,olric,gateway}@<name>` (+ `sfu`/`turn` if WebRTC) | `10000–10099` |

Reserved namespace names: **`index`** and **`nameserver`**. They are not tenant-provisionable.

Drive nodes through the `orama` CLI (`orama node …`). Do not `systemctl start` leftover host units (`orama-ipfs`, `orama-olric`, `caddy.service`, `coredns.service`, `wg-quick@wg0`). Those files may still exist on disk for rollback; they are disabled. Inter-node traffic uses the WireGuard overlay (`10.0.0.x`). Rolling upgrades never restart multiple index RQLite voters at once.

**The overlay is not a child of the supervisor.** Install enables two units:
`orama-node.service` and `orama-namespace-wireguard@index.service`. The mesh
comes up at boot on its own, so a node whose supervisor cannot start — a bad
`node.yaml`, a failed config validation, a missing binary — is still reachable
on `10.0.0.x` for diagnosis. The WireGuard unit is deliberately **not**
`PartOf=orama-node.service` and its `ExecStop` is a no-op: `PartOf` propagates
restart, so `orama node restart` used to tear `wg0` down and sever every
namespace raft and Olric memberlist on the node. Bring the interface down
explicitly (`wg-quick down wg0`) when that is the actual intent.

Every unit that binds or reaches across the overlay — `rqlite@`, `olric@`,
`gateway@`, `pubsub@`, `sfu@`, `turn@`, `ipfs@`, `vault@` — is ordered
`After=orama-namespace-wireguard@index.service`, so a cold boot cannot start
Olric or the SFU against an address that does not exist yet.

**Unit dependencies express ordering, not lifecycle.** `Requires=` propagates
stop *and* restart, so it is reserved for the cases where one unit is genuinely
useless without another. Two qualify: `ipfs-cluster@` and `ipfs-gc@` on
`ipfs@` — a controller with no daemon has nothing to control, and `ipfs repo gc`
works through the running daemon's API.

Every other unit `orama-node` manages uses `Wants=` + `After=`. In particular
`gateway@` no longer
declares `Requires=` on `rqlite@` and `olric@`: a
`systemctl restart orama-namespace-rqlite@<ns>` — which the split-brain recovery
path issues by itself — used to bounce the gateway with it, restarting the
leader wait, the health monitor, the cluster manager, every reconciler and the
tenant restore, for a database restart the gateway is built to ride out. The
gateway waits for rqlite itself (90s, then it exits and systemd restarts it —
until the gateway starting-state work lands) and reconnects to Olric
indefinitely in the background (`initializeOlricClientWithRetry`, then
`startOlricReconnectLoop`) with its cache endpoints returning 503 meanwhile. So
ordering is all either backend owes it. `olric@` dropped its rqlite dependency entirely: it is an
in-memory cache with its own memberlist and never reads the database, so the
only thing that coupling ever did was throw away the node's cache whenever
rqlite restarted.

**Gateway readiness.** A gateway reports its own start-up state on `/health`
and `/v1/health`, separately from the health of the things it talks to:

| State | Meaning | HTTP |
|---|---|---|
| `starting` | Listening, but the database schema is not yet at the version this binary requires — almost always because the local rqlite has no leader. Retried with backoff for as long as the process lives. | 503 |
| `ready` | Schema is at the required version; the gateway serves. | 200 |
| `blocked` | A leader answered and the schema is genuinely *below* what the binary requires. Retrying cannot fix it: migrate the database or roll the binary back. | 503 |

While not `ready` the gateway refuses every request except a short passthrough
list — `/health`, `/v1/health`, `/status`, `/v1/status`, `/v1/version`,
`/v1/internal/ping`, `/v1/internal/tls/check` and the ACME challenge path — so a
caller gets "gateway is starting, waiting for rqlite leader" instead of a
cryptic SQL error from a handler talking to a leaderless database. That list is
deliberately not the same as the "no API key needed" list: most of *those*
endpoints (`/v1/auth/verify`, `/v1/invoke`, `/v1/vault/*`) write to the
database, and letting them through would defeat the point of `blocked`.

The refusal is issued inside the CORS middleware, so a browser client can read
the reason rather than seeing an opaque network error. Background work that
touches the database — the health checker, the health monitor, the namespace
health loop, peer discovery — waits for `ready` before its first tick.

This replaces a start-up path where the leader wait, the migrations and the
schema contract were one call whose error the caller logged as a warning and
carried on from. A namespace whose raft had no leader therefore ended up with
every gateway serving on an unmigrated database. The two failures are now told
apart: the transient one is retried, the permanent one refuses to serve.

The per-attempt budgets are `gateway.Config.RQLiteReadyTimeout` (default 20s)
and `SchemaApplyTimeout` (default 30s). They bound one attempt, not the
gateway's patience — shortening them makes it notice a recovered leader sooner,
not give up on one earlier.

Only a genuine version mismatch is `blocked`. A failure to *read* the migration
tracker — a leader lost mid-check, a context deadline — stays retryable, or a
200ms blip would latch a namespace out of service until someone restarted it.

The node's namespace-health probe asks each local gateway's `/v1/health` rather
than dialling its port, so a gateway that is up but `starting` is withdrawn from
the `ns-<name>` DNS round-robin instead of being sent traffic it cannot serve.

**Unit restart policy.** Every long-running unit orama-node manages uses
`Restart=always`, `RestartSec=5s` and `StartLimitIntervalSec=0`. Always, because
`on-failure` leaves a daemon down after a clean exit that was not asked for. No
start limit, because the boot supervisor reconciles these units and `systemctl
start` on a rate-limited unit fails with "Start request repeated too quickly"
until someone runs `reset-failed` — the default limit would turn a transient
failure into one that needs a human. Retrying forever was already the effective
behaviour for anything that takes a moment to fail (`RestartSec=5` never trips
the default five-starts-in-ten-seconds window); it is now deliberate rather than
accidental, and it also covers the unit that fails *instantly* — a missing
binary, an unparseable config — which used to reach `failed` within ten seconds.
Since nothing parks those any more, `orama monitor report` reads the restart
counter instead: `restartLoopRisk` treats a unit that has restarted repeatedly
and never reached active as a crash loop, and raises the same critical alert
`failed` state used to.

### Boot: components, not a sequence

`orama-node` converges its services; it does not start them in a line. Every
piece of start-up is a **component** with declared dependencies
(`pkg/node/boot`), and a supervisor runs each one whose dependencies are ready,
retrying failures with exponential backoff (1s → 60s) instead of exiting.

Components come in two tiers:

| Tier | Components | Needs |
|---|---|---|
| **local** | `data-dir`, `wireguard`, `libp2p`, `peer-info`, `monitoring`, `pubsub`, `ipfs-cluster-config`, `storage`, `cluster-discovery`, `rqlite-local`, `nameserver`, `gateway`, `edge-serving`, `edge-aux`, `wireguard-sync`, `ipfs-swarm-sync` | this machine only |
| **cluster** | `rqlite-cluster`, `dns-registration` | a raft quorum |

`edge-serving` is vault, the optional SNI router and Caddy; `edge-aux` is ntfy
and the anyone-client. They are separate because `dns-registration` depends on
the first and not the second: a `dns_nodes` row saying `active` is a promise
that this node terminates TLS and proxies tenants, so a node whose Caddy never
started must not advertise itself — while a broken ntfy, which serves no
traffic, must not take a healthy node out of DNS.

Two components carry health checks, polled every 30s:

- `rqlite-local` — `LocalHealthy`: the local `rqlited` answers `/status` and the
  connection handle is open. It does *not* require a leader. A failure restarts
  the unit and reopens the handle, and blocks everything that reads the local
  replica until it is back.
- `rqlite-cluster` — `LeaderReachable`: a leader-routed read still succeeds.
  This is the single component that waits for consensus; it runs
  `WaitForRaftReady` and the read under short per-attempt budgets, so losing
  quorum puts it — and only it and its dependents — back into retry.

Because components are reconciled repeatedly, spawning is idempotent: a port
held by the unit a spawn is about to start is not a conflict (`ensurePortsFree`
short-circuits on an already-active unit), so a retry after a transient failure
does not report a port conflict against itself.

That split is what the tiers buy: a node that boots with every peer down still
brings up WireGuard, IPFS, the local rqlite replica, CoreDNS, the index gateway,
Caddy, ntfy and its tenants. It announces itself as **degraded** rather than
active, and returns to active on its own when quorum comes back — with no
restart, because nothing exited.

The mesh and swarm syncs are deliberately in the local tier even though they
read cluster tables. Raft runs *over* the mesh, so `loadDesiredWireGuardPeers`
falls back to this node's own replica and applies peers additively; gating that
repair on a quorum would make fixing the transport depend on the transport.

`orama-node.service` carries `StartLimitIntervalSec=0` and `TimeoutStopSec=60`.
The process no longer exits because the cluster is unreachable, so a restart now
means a real crash and systemd should keep restarting rather than park the unit
in `failed`; the stop timeout leaves room for the shutdown sequence (announce
maintenance → wait up to 10s for the supervisor → tear down → hand raft
leadership over). `orama upgrade` rewrites the unit in Phase 5, so an existing
fleet picks both settings up on the next upgrade, after `daemon-reload` and a
node restart.

**Lifecycle states** (`pkg/node/lifecycle`): `joining` → `active` ⇄ `degraded`,
with `draining` and `maintenance` driven by operators and never overridden by
the supervisor. `degraded` is a *serving* state, so a degraded node is not taken
out of rotation; the leader's health monitor deliberately does not short-circuit
it and verifies the claim with an HTTP probe instead. A node leaves `joining`
once its **serving core** — `rqlite-local` and `gateway` — is up, so one local
component that can never converge cannot pin it out of `IsAvailable` and stop it
announcing maintenance on shutdown.

The state is published in discovery metadata. `orama monitor report` does not
render it today; read it from the node's own log
(`journalctl -u orama-node | grep "Node lifecycle state changed"`), which also
lists the components that have not converged.

**One writer for membership.** A node's existence is recorded in five places —
`dns_nodes`, `wireguard_peers`, the index raft configuration, ipfs-cluster's
peer list and IPFS's peering config — each with its own liveness definition and
its own timer, and until now with no single writer. A machine deleted without
ceremony left a different residue in each: an `inactive` `dns_nodes` row that
was never deleted, a `wireguard_peers` row re-applied to the interface every 60
seconds for ever, a raft voter still counted toward quorum.

`pkg/node/membership` computes what the membership should be and diffs it
against what the stores hold. It runs on the raft leader only, every 60s, as the
`membership` boot component. Removal needs positive evidence of departure — a
raft eviction tombstone — and *any* sign of life vetoes it: discovery seeing the
peer, or a heartbeat inside the 30-minute liveness grace. A node that merely
stopped answering is missing, not gone; turning the first into the second is the
raft eviction path's job, and it writes the tombstone when it does. `dns_nodes`
rows survive a further 6-hour tombstone grace so an operator looking at the
table right after a node disappears still sees what was removed.

`wireguard_peers` rows are matched to nodes on the **overlay address**, not on
`node_id`. A joining node now sends its libp2p peer id in the join request and
the row carries it, but rows predating that carry a synthetic `node-<wgip>`
(migration 038 backfills what it can from `dns_nodes`), so the overlay address
remains the reliable join between the two tables.

A row whose overlay address matches no node is resolved by `confirmed_at`, which
is never cleared once set. There are two writers, and both only ever set it:
each node sets it on its own row when it self-registers — a node writing its own
row is the strongest evidence there is that it came up, and the self-register
upsert keeps any existing value with `COALESCE` — and the reconciler sets it for
any row whose node it can see in `dns_nodes`. An unmatched row is then read as:

- **never confirmed, older than the 30-minute join grace** — a join that did not
  finish. Dropped. Before `confirmed_at` existed this row was indistinguishable
  from a live peer and every survivor re-applied it to `wg0` every 60 seconds
  indefinitely.
- **never confirmed, recent** — a join still in flight. Left alone: a node gets
  its WireGuard row before its `dns_nodes` row, so absence is not departure.
- **never confirmed, no usable `created_at`** — nothing distinguishes it from
  either of the above. Kept, and reported, so it is not invisible.
- **confirmed** — a node that came up and then vanished from `dns_nodes`.
  Reported only. Deleting the mesh entry of a machine that may still be running
  would sever it.

The delete carries `AND confirmed_at IS NULL`, so a node that came up between
the evidence read and the write is never cut off by a stale plan.

**Dead voters are evicted.** A voter that is gone for good used to stay in the
raft configuration for ever — quorum arithmetic kept counting a machine that no
longer existed, so on a three-voter cluster the second such event was permanent
quorum loss with `recover-raft` the only way out. The leader now removes one,
but only when three separate sources agree, because the leader's own view of
reachability has a failure mode where a healthy node looks dead — a route lost,
a firewall change, a WireGuard key rotation:

1. **raft**, sustained: the member is an unreachable *voter* on 10 consecutive
   2-minute ticks. (A non-voter costs the cluster nothing, so there is no
   availability argument for touching one.)
2. **libp2p discovery** no longer knows the peer at all, which takes at least
   the 2-hour inactivity window.
3. **other nodes**: at least two *different* nodes recorded it `dead` in
   `node_health_events` within the last 30 minutes, with no later `recovered`.

Only the third is independent of this node. It is also the one that has to
cross an identifier boundary: a raft node id is the raft advertise address,
while the health monitor keys on libp2p peer ids, so the candidate is resolved
through `dns_nodes.internal_ip` before the corroboration query. Comparing the
two id spaces directly matches nothing, silently.

The removal is refused unless the reachable voters still meet quorum afterwards,
the raft term has been stable for three ticks, and at most one member changes
per tick.

An eviction writes a tombstone to `raft_evicted_nodes`. Without one,
`recoverOrphanedNodes` re-added the node within five minutes — it re-adds every
discovery peer absent from the raft configuration, so the eviction was undone
automatically. (Removals made by hand still get no tombstone and are still
undone; wiring the CLI and the decommission path to write one is chg-303.)

Tombstones expire after 24 hours, and that expiry is load-bearing rather than
housekeeping: an evicted node is the one node that *cannot* clear its own
tombstone, because it is outside the raft configuration, so its local rqlite has
no leader and it cannot write to the cluster at all. Without expiry, a node
evicted after a long partition would be permanently removed with no automatic
way back. The TTL is far longer than the 2-hour discovery window on purpose — a
node that is genuinely gone has dropped out of discovery long before its
tombstone lifts, so nothing is offering it for re-adding by then.

Discovery itself now forgets an unanswering peer after 2 hours rather than 24.
The same constant governs `waitForMinClusterSizeBeforeStart`, so a node
restarting during an outage that has already lasted 2 hours sees fewer peers and
waits out its (bounded) minimum-cluster-size window before continuing.

Voter demotion is an in-place `POST /join` with `voter:false`. It used to be
remove-then-rejoin, which left the node outside the configuration for up to 59
seconds while it retried with backoff; a leader change inside that window
orphaned it, and the rollback path could fail too.

**RQLite identity is still the raft address.** rqlited is not given a
`-node-id`, so its raft node id defaults to the raft advertise address. That
makes identity and routing the same value: change a node's WireGuard IP and the
same machine becomes a second member, while the old entry stays and keeps
counting toward quorum. Decoupling the two needs a rolling re-join per node and
something able to retire the stale entry, so it is sequenced after the
membership work (bugboard chg-302, bug-301, chg-303).

Two consequences are already handled. Advertised addresses are always rewritten
to a WireGuard address — selection used to prefer a *public* IP and fall back to
the overlay, which replaced a reachable raft endpoint with one UFW blocks; with
no overlay candidate it now refuses to rewrite rather than substituting a public
IP. And a node that finds itself in the raft configuration under a different id
logs it at Error on every start instead of discarding the result.

RQLiteManager is a **client** of `orama-namespace-rqlite@index` (data dir `~/.orama/data/rqlite`, adopted in place). App GossipSub is `orama-namespace-pubsub@index` (`127.0.0.1:10105`); gateways call that HTTP API. Caddy reverse_proxies to `localhost:10104`. CoreDNS reads index RQLite `dns_records` at `localhost:10100`. Olric v0.7.0 is in-memory only (`olric-server` is not given a data directory); a cold disk snapshot of the cache dir yields nothing.

## Core Components

### 1. API Gateway (`pkg/gateway/`)

The gateway is the main entry point for all client requests. It coordinates between various backend services.

**Key Files:**
- `gateway.go` - Core gateway struct and routing
- `dependencies.go` - Service initialization and dependency injection
- `lifecycle.go` - Start/stop/health lifecycle management
- `middleware.go` - Authentication, logging, error handling
- `routes.go` - HTTP route registration

**Handler Packages:**
- `handlers/auth/` - Authentication (JWT, API keys, wallet signatures)
- `handlers/storage/` - IPFS storage operations
- `handlers/cache/` - Distributed cache operations
- `handlers/pubsub/` - Pub/sub messaging
- `handlers/serverless/` - Serverless function deployment and execution

### 2. Client SDK (`pkg/client/`)

Provides a clean Go SDK for interacting with the Orama Network.

**Architecture:**
```go
// Main client interface
type NetworkClient interface {
    Database() DatabaseClient
    PubSub() PubSubClient
    Network() NetworkInfo
    Storage() StorageClient

    Connect() error
    Disconnect() error
    Health() (*HealthStatus, error)
    Config() *ClientConfig
    Host() host.Host
}
```

Cache and serverless operations are not exposed through the SDK; use the gateway HTTP API (`/v1/cache/*`, `/v1/functions/*`) directly.

**Key Files:**
- `client.go` - Main client orchestration
- `interface.go` - `NetworkClient` and sub-client interfaces
- `config.go` - Client configuration
- `storage_client.go` - IPFS storage client
- `database_client.go` - RQLite database client
- `pubsub_bridge.go` - Pub/sub messaging client
- `network_client.go` - Network/peer information client
- `transport.go` - HTTP transport layer
- `errors.go` - Client-specific errors

**Usage Example:**
```go
import "github.com/DeBrosOfficial/network/pkg/client"

// Create client
cfg := client.DefaultClientConfig("my-app")
cfg.GatewayURL = "https://api.orama.network"
cfg.APIKey = "your-api-key"

c, err := client.NewClient(cfg)
if err != nil {
    log.Fatal(err)
}
if err := c.Connect(); err != nil {
    log.Fatal(err)
}
defer c.Disconnect()

// Use storage (reader-based upload; result carries the CID)
resp, err := c.Storage().Upload(ctx, bytes.NewReader(data), "file.txt")
fmt.Println(resp.Cid)

// Query database
result, err := c.Database().Query(ctx, "SELECT * FROM users")

// Publish message
err = c.PubSub().Publish(ctx, "chat", []byte("hello"))

// Subscribe to a topic
err = c.PubSub().Subscribe(ctx, "chat", func(topic string, data []byte) error {
    fmt.Printf("%s: %s\n", topic, data)
    return nil
})
```

### 3. Database Layer (`pkg/rqlite/`)

ORM-like interface over RQLite distributed SQL database.

**Key Files:**
- `client.go` - Main ORM client
- `orm_types.go` - Interfaces (Client, Tx, Repository[T])
- `query_builder.go` - Fluent query builder
- `repository.go` - Generic repository pattern
- `scanner.go` - Reflection-based row scanning
- `transaction.go` - Transaction support

**Features:**
- Fluent query builder
- Generic repository pattern with type safety
- Automatic struct mapping
- Transaction support
- Connection pooling with retry

**Example:**
```go
type User struct {
    ID    int    `db:"id"`
    Name  string `db:"name"`
    Email string `db:"email"`
}

// Query builder (scans into dest, returns only error)
var users []User
err := client.CreateQueryBuilder("users").
    Select("id", "name", "email").
    Where("age > ?", 18).
    OrderBy("name ASC").
    Limit(10).
    GetMany(ctx, &users)

// Save an entity (insert or update by primary key)
user := &User{Name: "Alice", Email: "alice@example.com"}
err = client.Save(ctx, user)
```

Note: `Client.Repository(table)` returns an untyped `any` — assert it to the
generic `Repository[T]` interface before use.

### 4. Serverless Engine (`pkg/serverless/`)

WebAssembly (WASM) function execution engine with host functions.

**Architecture:**
```
pkg/serverless/
├── engine.go              - Core WASM engine
├── execution/             - Function execution
│   ├── executor.go
│   └── lifecycle.go
├── cache/                 - Module caching
│   └── module_cache.go
├── registry/              - Function metadata
│   ├── registry.go
│   ├── function_store.go
│   ├── ipfs_store.go
│   └── invocation_logger.go
└── hostfunctions/         - Host functions by domain
    ├── cache.go           - Cache operations
    ├── storage.go         - Storage operations
    ├── database.go        - Database queries
    ├── pubsub.go          - Messaging
    ├── http.go            - HTTP requests
    └── logging.go         - Logging
```

**Features:**
- Secure WASM execution sandbox
- Memory and CPU limits
- Host function injection (cache, storage, DB, HTTP)
- Function versioning
- Invocation logging
- Hot module reloading

### 5. Configuration System (`pkg/config/`)

Domain-specific configuration with validation.

**Structure:**
```
pkg/config/
├── config.go              - Main config aggregator
├── yaml.go                - YAML loading
├── node_config.go         - Node settings
├── database_config.go     - Database settings
├── gateway_config.go      - Gateway settings
└── validate/              - Validation
    ├── validators.go
    ├── node.go
    ├── database.go
    ├── discovery.go
    ├── logging.go
    └── security.go
```

### 6. Anyone Integration (`pkg/anyoneproxy/`)

Integration with the Anyone Protocol for anonymous routing.

Every node runs Anyone as a **client** only: a local SOCKS5 proxy on `127.0.0.1:9050` used by `/v1/proxy/anon` and the anonymity tunnel. Relay/ORPort operator mode is not installed.

**Key Files:**
- `pkg/anyoneproxy/socks.go` - SOCKS5 proxy client interface
- `pkg/gateway/anon_proxy_handler.go` - Anonymous request proxy endpoint
- `pkg/gateway/anon_tunnel_handler.go` - Authenticated tunnelling proxy (bugboard #168)
- `pkg/environments/production/installers/anyone_installer.go` - Client binary + anonrc

**Features:**
- Smart routing (bypasses proxy for local/private addresses)

**API Endpoints:**
- `POST /v1/proxy/anon` - Route a single HTTP request through the Anyone network.
  The gateway performs the request, so it necessarily sees the URL, headers and
  body in cleartext.
- `GET /v1/proxy/tunnel` (WebSocket) - Carry an opaque TCP stream to
  `?host=&port=` through the Anyone network. TLS is negotiated end-to-end
  between the client and the destination *through* the tunnel, so the gateway
  relays ciphertext and sees only the destination host and port. See
  "Anonymity Tunnel" in `docs/CLIENT_SDK.md`.

Both require the `proxy` grant **and** a genuine end-user (SIWE wallet) JWT — an
app-runtime API key alone is refused.

### 7. Shared Utilities

**HTTP Utilities (`pkg/httputil/`):**
- Request parsing and validation
- JSON response writers
- Error handling
- Authentication extraction

**Error Handling (`pkg/errors/`):**
- Typed errors (ValidationError, NotFoundError, etc.)
- HTTP status code mapping
- Error wrapping with context
- Stack traces

**Contracts (`pkg/contracts/`):**
- Interface definitions for all services
- Enables dependency injection
- Clean abstractions

## Data Flow

### 1. HTTP Request Flow

```
Client Request
    ↓
[HTTPS Termination]
    ↓
[Authentication Middleware]
    ↓
[Route Handler]
    ↓
[Service Layer]
    ↓
[Backend Service] (RQLite/Olric/IPFS)
    ↓
[Response Formatting]
    ↓
Client Response
```

### 2. WebSocket Flow (Pub/Sub)

```
Client WebSocket Connect
    ↓
[Upgrade to WebSocket]
    ↓
[Authentication]
    ↓
[Subscribe to Topic]
    ↓
[LibP2P PubSub] ←→ [Local Subscribers]
    ↓
[Message Broadcasting]
    ↓
Client Receives Messages
```

### 3. Serverless Invocation Flow

```
Function Deployment:
    Upload WASM → Store in IPFS → Save Metadata (RQLite) → Compile Module

Function Invocation:
    Request → Load Metadata → Get WASM from IPFS →
    Execute in Sandbox → Return Result → Log Invocation
```

## Security Architecture

### Authentication Methods

1. **Wallet Signatures** (Ethereum-style)
   - Challenge/response flow
   - Nonce-based to prevent replay attacks
   - Issues JWT tokens after verification

2. **API Keys**
   - Long-lived credentials
   - Stored in RQLite
   - Namespace-scoped
   - Carry a grant set (`invoke`, `storage`, `push`, `webrtc`, `proxy`, `pubsub`, `cache`, or `admin`). HTTP `/v1/invoke` is a public path; private functions still require the `invoke` grant (or a SIWE wallet). Node command/logs/leave and network connect/disconnect require `admin`.

3. **JWT Tokens**
   - Short-lived (15 min default)
   - Refresh token support
   - Claims-based authorization

### Network Security (WireGuard Mesh)

All inter-node communication is encrypted via a WireGuard VPN mesh:

- **WireGuard IPs:** Each node gets a private IP (10.0.0.x/24) used for all cluster traffic
- **UFW Firewall:** Only public ports are exposed: 22 (SSH; Ubuntu/sandbox only — OramaOS has no SSH), 53 (DNS, nameservers only), 80/443 (HTTP/HTTPS), 51820 (WireGuard UDP)
- **IPv6 disabled:** System-wide via sysctl to prevent bypass of IPv4 firewall rules
- **Internal services** (RQLite 10100/10101, IPFS swarm 4001 + API 10107, Olric 10102/10103, Gateway 10104) are only accessible via WireGuard or localhost
- **Invite tokens:** Single-use, time-limited tokens for secure node joining. No shared secrets on the CLI
- **Join flow:** New nodes authenticate via HTTPS (443) with TOFU certificate pinning, establish WireGuard tunnel, then join all services over the encrypted mesh. The joining node establishes its libp2p identity before it asks to join, so the request carries the peer id the cluster will key it by

**Join ordering.** `/v1/internal/join` does everything that can fail without
touching cluster state first — validate every field, check the invite token is
live (without consuming it), refuse the request if any identity in it is already
registered, read the secrets, read the local WireGuard identity, build the peer
list — and only then burns the token and writes the `wireguard_peers` row.

That ordering is what makes the token safe to release on failure. `public_ip` is
a string the caller chooses and nothing checks against the source address, and
the pre-join cleanup deletes rows by it, so releasing the token would otherwise
let one invite evict any node in the fleet, repeatedly: name its IP, collide
deliberately so the registration fails, get the token back.

Three rules close that, and each is load-bearing:

- **The refusal set is the complement of the cleanup set.** The check rejects
  every row matching the submitted IP, key or peer id *except* the ones the
  cleanup is about to delete. Restricting it to live rows was not enough — an
  unconfirmed row at a different public IP is invisible to both, yet still
  collides with the `INSERT`.
- **Liveness is `confirmed_at IS NOT NULL` OR a `dns_nodes` row at the same
  overlay address**, and it needs both halves. A node still on the old binary
  nulls its own `confirmed_at` every 60s, so during the rolling upgrade of this
  change `confirmed_at` alone would read every un-upgraded node as free to
  displace. `dns_nodes` has no such hole; and a node mid-join has no `dns_nodes`
  row yet, so neither signal suffices alone.
- **A uniqueness conflict does not release the token.** It means the request
  named a bad identity — the caller's problem. Only a genuine cluster fault
  releases, so nothing an attacker controls makes the token replayable.

The token-liveness gate exists because the refusal check answers whether a given
IP, key or peer id belongs to a live node. Reachable without a token, that is a
fleet-enumeration oracle, so the 409 body also names no field. The order used to be the reverse, so any failure
after the write (an unreadable `swarm.key`, a joining node that died mid-install)
cost the operator a token they could not reuse *and* left a ghost peer row. If
the write itself fails the token is released.

**Overlay address allocation** (`pkg/overlay`) is the single path every
allocating writer uses — the join handler, `/v1/internal/wg/peer` and the
OramaOS enrolment handler. A node's own WireGuard self-registration stays
outside it because it allocates nothing: it re-asserts the row it was already
given, keyed by its own node id, and so upserts (`ON CONFLICT(node_id) DO
UPDATE`) rather than replacing. `INSERT OR REPLACE` there was deleting and
re-inserting the row every 60 seconds, silently resetting every column it did
not name — `operator_wallet` among them. It allocates the **lowest free** address in `10.0.0.2-254` with a plain
`INSERT`, retrying only on a `UNIQUE` violation naming `wg_ip` — a conflict on
the public key or the node id means the peer is already registered under another
address, which no retry can fix. The client-supplied peer id is parsed before it
is stored, since it becomes the row's primary key. The two
previous implementations each read the table and wrote it in separate statements
and wrote with `INSERT OR REPLACE`, so the loser of a race silently deleted the
winner's row and took its address, cutting a node that had just joined out of
the mesh. Allocating the lowest free address rather than `max+1` also keeps a
cluster that has churned through nodes from rolling past `10.0.0.254` into
`10.0.1.x`, which is outside the `/24` that the `wg0` PostUp rule and the
internal-auth check both accept.

### Service Authentication

- **RQLite:** credentials are generated at genesis; `rqlited` is **not** started with `-auth` today. Overlay + firewall keep the HTTP API off the public internet
- **Olric:** memberlist binds the WireGuard address. Olric v0.7.0 YAML has no `encryptionKey`; overlay is the control
- **IPFS Cluster:** TrustedPeers restricted to known cluster peer IDs (not `*`). The systemd unit is not written if `CLUSTER_SECRET` is missing or empty
- **Internal endpoints:** every `/v1/internal/wg/*` endpoint requires the caller to be on the WireGuard overlay **and** to present the cluster secret. A gateway with no cluster secret configured refuses them outright rather than serving them unauthenticated
- **Vault:** V1 push/pull endpoints require session token authentication when guardian is configured
- **WebSockets:** Origin header validated against the node's configured domain
- **Tenant SQLite:** opened with `SQLITE_LIMIT_ATTACHED=0`; `ATTACH`/`DETACH` and multi-statement queries are rejected
- **WASM egress:** `http_fetch` / `anyone_fetch` deny loopback, private, link-local, unspecified, and multicast URLs
- **WASM memory:** wazero `WithMemoryLimitPages` from `MaxMemoryLimitMB` (default 256 MB). Modules without a memory max still cannot grow past that
- **WASM concurrency:** process-wide semaphore plus a per-namespace cap (`maxConcurrent/2`, min 1)
- **Process uid:** namespace gateway/rqlite/olric/sfu/turn/pubsub run as `User=orama` (not root). `/opt/orama/bin` is `root:orama` 0750. CoreDNS/Caddy `ReadWritePaths` do not include `secrets/`
- **TLS:** internet-facing TLS is 1.2+ (`TCPSNIGateway`, CLI, tlsutil)
- **wg0.conf:** written 0600 (chmod after WriteFile/tee; umask is not trusted)

### Token & Key Security

- **Refresh tokens:** Stored as SHA-256 hashes (never plaintext)
- **API keys:** Stored as HMAC-SHA256 hashes with a server-side secret
- **TURN secrets:** Encrypted at rest with AES-256-GCM (key derived from cluster secret)
- **Binary signing:** Build archives signed with rootwallet EVM signature, verified on install

### Process Isolation

- **Dedicated user:** Most units run as `orama`. WireGuard (`wg-quick`) runs as root. Anyone client runs as `debian-anon`. ntfy runs as `ntfy`.
- **systemd hardening:** `ProtectSystem=strict`, `NoNewPrivileges=yes`, `PrivateDevices=yes`, etc. `orama-node` omits `NoNewPrivileges` so it can `sudo systemctl` the `@` units.
- **Capabilities:** Caddy, CoreDNS, and the SNI router get `CAP_NET_BIND_SERVICE` for privileged ports.

See [SECURITY.md](SECURITY.md) for the full security hardening reference.

### TLS/HTTPS

- Automatic ACME (Let's Encrypt) certificate management via Caddy
- TLS 1.3 support
- HTTP/2 enabled
- On-demand TLS for deployment custom domains

### Middleware Stack

Order matches `Gateway.withMiddleware` (outermost first). Rate limiting runs **before** authentication so the auth path itself is capped.

1. **Logger** — request/response logging
2. **Security headers**
3. **Rate limiting** — per-client, before auth
4. **CORS**
5. **Domain routing**
6. **Authentication** — JWT / API key
7. **Authorization** — namespace access control
8. **Scope gate** — tightens an already-authorized request
9. **Namespace rate limiting**
10. Handler (errors are returned as HTTP status, not a separate middleware)

## Scalability

### Horizontal Scaling

- **Gateway:** Stateless, can run multiple instances behind load balancer
- **RQLite:** Multi-node cluster with Raft consensus
- **IPFS:** Distributed storage across nodes
- **Olric:** Distributed cache with consistent hashing

### Caching Strategy

1. **WASM Module Cache** - Compiled modules cached in memory
2. **Olric Distributed Cache** - Shared cache across nodes
3. **Local Cache** - Per-gateway request caching

### High Availability

- **Database:** RQLite cluster with automatic leader election
- **Storage:** IPFS replication factor configurable
- **Cache:** Olric replication and eventual consistency
- **Gateway:** Stateless, multiple replicas supported

## Monitoring & Observability

### Health Checks

- `/health` - Liveness probe
- `/v1/status` - Detailed status with service checks

### Metrics

There is no Prometheus-compatible metrics endpoint yet. Observability today comes
from the health/status endpoints above, structured logs, and the `orama monitor`
and `orama inspect` CLI commands.

### Logging

- Structured logging (JSON format)
- Log levels: DEBUG, INFO, WARN, ERROR
- Correlation IDs for request tracing

## Development Patterns

### SOLID Principles

- **Single Responsibility:** Each handler/service has one focus
- **Open/Closed:** Interface-based design for extensibility
- **Liskov Substitution:** All implementations conform to contracts
- **Interface Segregation:** Small, focused interfaces
- **Dependency Inversion:** Depend on abstractions, not implementations

### Code Organization

- **Average file size:** ~150 lines
- **Package structure:** Domain-driven, feature-focused
- **Testing:** Unit tests for logic, E2E tests for integration
- **Documentation:** Godoc comments on all public APIs

## Deployment

### Building & Testing

```bash
make build     # Build all binaries
make test      # Run unit tests
make test-e2e  # Run E2E tests
```

### Production

```bash
# First node (genesis — creates cluster)
# Nameserver nodes use the base domain as --domain
sudo orama node install --vps-ip <IP> --domain example.com --base-domain example.com --nameserver

# On the genesis node, generate an invite for a new node
orama node invite
# Outputs the join command with the token for the new node

# Additional nameserver nodes (join via invite token over HTTPS)
sudo orama node install --join https://example.com --token <TOKEN> \
    --vps-ip <IP> --domain example.com --base-domain example.com --nameserver
```

**Security:** Nodes join via single-use invite tokens over HTTPS. A WireGuard VPN tunnel
is established before any cluster services start. All inter-node traffic (RQLite, IPFS,
Olric, LibP2P) flows over the encrypted WireGuard mesh — no cluster ports are exposed
publicly. **Never use `http://<ip>:10104`** for joining — the index gateway is internal-only and
blocked by UFW. Use the domain (`https://node1.example.com`) or, if DNS is not yet
configured, use the IP over HTTP port 80 (`http://<ip>`) which goes through Caddy.

### Docker (Future)

Planned containerization with Docker Compose and Kubernetes support.

## WebRTC (Voice/Video/Data)

Namespaces can opt in to WebRTC support for real-time voice, video, and data channels.

### Components

- **SFU (Selective Forwarding Unit)** — Pion WebRTC server that handles signaling (WebSocket), SDP negotiation, and RTP forwarding. Runs on all 3 cluster nodes, binds only to WireGuard IPs.
- **TURN Server** — Pion TURN relay that provides NAT traversal. One shared server per host (`orama-turn.service`) serves every namespace allocated TURN there, each authenticated against its own secret; typically 2 of 3 nodes for redundancy. Public-facing (UDP 3478, 443, relay range 49152-65535).

### Security Model

- **TURN-shielded**: SFU binds only to WireGuard (10.0.0.x), never 0.0.0.0. All client media flows through TURN relay.
- **Forced relay**: `iceTransportPolicy: relay` enforced server-side — no direct peer connections.
- **HMAC credentials**: Per-namespace TURN shared secret with 10-minute TTL.
- **Namespace isolation**: Each namespace has its own TURN secret, port ranges, and rooms.

### Port Allocation

WebRTC uses a separate port allocation system from core namespace services:

| Service | Port Range |
|---------|-----------|
| SFU signaling | 30000-30099 |
| SFU media (RTP) | 20000-29999 |
| TURN listen | 3478/udp (standard) |
| TURN TLS | 443/udp |
| TURN relay | 49152-65535/udp |

See [docs/WEBRTC.md](WEBRTC.md) for full details including client integration, API reference, and debugging.

## OramaOS

For mainnet, devnet, and testnet environments, nodes run **OramaOS** — a custom minimal Linux image built with Buildroot.

**Key properties:**
- No SSH, no shell — operators cannot access the filesystem
- LUKS full-disk encryption with Shamir key distribution across peers
- Read-only rootfs (SquashFS). dm-verity hashes can be built into the image; they are **not** wired into the boot path today, so rootfs integrity is not enforced at boot
- A/B partition updates with cryptographic signature verification
- Service sandboxing via Linux namespaces + seccomp (seccomp profiles exist; enforcement is not on for the Ubuntu fleet — that is OramaOS work)
- Single root process: the **orama-agent**

These OramaOS properties do **not** apply to the production Ubuntu fleet (sandbox and current operators). Ubuntu nodes have SSH and no LUKS/dm-verity.

**The orama-agent manages:**
- Boot sequence and LUKS key reconstruction
- WireGuard tunnel setup
- Service lifecycle in sandboxed namespaces
- Command reception from Gateway over WireGuard (port 9998)
- OS updates (download, verify, A/B swap, reboot with rollback)

**Node enrollment:** OramaOS nodes join via `orama node enroll` instead of `orama node install`. The enrollment flow uses a registration code + invite token + wallet verification.

See [ORAMAOS_DEPLOYMENT.md](ORAMAOS_DEPLOYMENT.md) for the full deployment guide.

Sandbox clusters remain on Ubuntu for development convenience.

## Future Enhancements

1. **GraphQL Support** - GraphQL gateway alongside REST
2. **gRPC Support** - gRPC protocol support
3. **Event Sourcing** - Event-driven architecture
4. **Kubernetes Operator** - Native K8s deployment
5. **Observability** - OpenTelemetry integration
6. **Multi-tenancy** - Enhanced namespace isolation

## Resources

- [RQLite Documentation](https://rqlite.io/docs/)
- [IPFS Documentation](https://docs.ipfs.tech/)
- [LibP2P Documentation](https://docs.libp2p.io/)
- [WebAssembly (WASM)](https://webassembly.org/)
