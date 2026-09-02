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
- **Join flow:** New nodes authenticate via HTTPS (443) with TOFU certificate pinning, establish WireGuard tunnel, then join all services over the encrypted mesh

### Service Authentication

- **RQLite:** credentials are generated at genesis; `rqlited` is **not** started with `-auth` today. Overlay + firewall keep the HTTP API off the public internet
- **Olric:** Memberlist gossip encrypted with a shared 32-byte key
- **IPFS Cluster:** TrustedPeers restricted to known cluster peer IDs (not `*`). The systemd unit is not written if `CLUSTER_SECRET` is missing or empty
- **Internal endpoints:** `/v1/internal/wg/peers` and `/v1/internal/wg/peer/remove` require cluster secret
- **Vault:** V1 push/pull endpoints require session token authentication when guardian is configured
- **WebSockets:** Origin header validated against the node's configured domain
- **Tenant SQLite:** opened with `SQLITE_LIMIT_ATTACHED=0`; `ATTACH`/`DETACH` and multi-statement queries are rejected
- **WASM egress:** `http_fetch` / `anyone_fetch` deny loopback, private, link-local, unspecified, and multicast URLs
- **WASM memory:** wazero `WithMemoryLimitPages` from `MaxMemoryLimitMB` (default 256 MB). Modules without a memory max still cannot grow past that
- **WASM concurrency:** process-wide semaphore plus a per-namespace cap (`maxConcurrent/2`, min 1)

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
- **TURN Server** — Pion TURN relay that provides NAT traversal. Runs on 2 of 3 nodes for redundancy. Public-facing (UDP 3478, 443, relay range 49152-65535).

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
