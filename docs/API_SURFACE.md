# Gateway API surface

Every route the gateway registers, and which client owns it. The point is that
the TypeScript SDK's coverage is a decision rather than an accident: it reaches
35 of 136 routes, and the other 101 are here with a reason.

`core/pkg/gateway/api_surface_test.go` keeps this document honest in both
directions. A route registered in the gateway and missing here fails the Go
build; so does a route documented here that is no longer registered. Adding a
route therefore means deciding who calls it.

| Owner | Meaning | Count |
|-------|---------|-------|
| `SDK` | `@debros/orama` calls it | 35 |
| `CLI` | The `orama` CLI calls it. An application has no reason to: deploying, minting keys and managing nodes are operator actions. | 59 |
| `direct` | Reachable by a client, but not through the SDK by design. The reason is in the row. | 22 |
| `internal` | Node-to-node over the WireGuard overlay. Never reachable by a client. | 20 |

The request and response shapes of the `SDK` routes are pinned by the fixtures
in [`contracts/`](../contracts), which both a Go handler test and a TypeScript
unit test read, so a shape change on either side fails without a cluster.

---

### Health and version

| Route | Owner | Notes |
|-------|-------|-------|
| `/.well-known/jwks.json` | direct | JWKS for verifying gateway-issued JWTs. Read by other services, not by an application. |
| `/health` | SDK | `network.health()` |
| `/status` | direct | Gateway process status. `network.status()` uses `/v1/network/status`. |
| `/v1/health` | direct | Same as `/health`, kept for older callers. |
| `/v1/schema-status` | CLI | Migration state, polled during provisioning. |
| `/v1/status` | direct | Same as `/status`, kept for older callers. |
| `/v1/version` | CLI | Build version. `orama version` and the upgrade checks read it. |

### Authentication

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/auth/api-key` | SDK | `auth.getApiKey()` |
| `/v1/auth/challenge` | SDK | `auth.challenge()` |
| `/v1/auth/jwks` | direct | JWKS. See `/.well-known/jwks.json`. |
| `/v1/auth/logout` | SDK | `auth.logout()` |
| `/v1/auth/phantom/complete` | direct | Phantom wallet deep-link completion. |
| `/v1/auth/phantom/session` | direct | Phantom wallet deep-link session. Browser redirect flow, not an SDK call. |
| `/v1/auth/phantom/session/` | direct | Phantom wallet deep-link session by id. |
| `/v1/auth/refresh` | SDK | Session renewal, called by the client on a 401. |
| `/v1/auth/simple-key` | direct | Development key issuance. Not for production code. |
| `/v1/auth/token` | direct | Exchange an API key for a JWT. A server-side concern; the SDK sends the key itself. |
| `/v1/auth/verify` | SDK | `auth.verify()` |
| `/v1/auth/whoami` | SDK | `auth.whoami()` |

### Database (RQLite)

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/rqlite/create-table` | SDK | `db.createTable()` |
| `/v1/rqlite/drop-table` | SDK | `db.dropTable()` |
| `/v1/rqlite/exec` | SDK | `db.exec()` |
| `/v1/rqlite/export` | CLI | Native RQLite backup. `orama db backup`. |
| `/v1/rqlite/find` | SDK | `db.find()`, `Repository.find()` |
| `/v1/rqlite/find-one` | SDK | `db.findOne()`, `Repository.findOne()` |
| `/v1/rqlite/import` | CLI | Native RQLite restore. |
| `/v1/rqlite/query` | SDK | `db.query()` |
| `/v1/rqlite/schema` | SDK | `db.getSchema()` |
| `/v1/rqlite/select` | SDK | `QueryBuilder.getMany()` / `getOne()` |
| `/v1/rqlite/transaction` | SDK | `db.transaction()` |

### Cache

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/cache/delete` | SDK | `cache.delete()` |
| `/v1/cache/get` | SDK | `cache.get()` |
| `/v1/cache/health` | SDK | `cache.health()` |
| `/v1/cache/mget` | SDK | `cache.multiGet()` |
| `/v1/cache/put` | SDK | `cache.put()` |
| `/v1/cache/scan` | SDK | `cache.scan()` |

### Pub/sub

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/pubsub/presence` | SDK | `pubsub.getPresence()` |
| `/v1/pubsub/publish` | SDK | `pubsub.publish()` |
| `/v1/pubsub/publish-batch` | direct | Publishing many messages in one request. Worth adding to the SDK when an application needs it; nothing does today. |
| `/v1/pubsub/topics` | SDK | `pubsub.topics()` |
| `/v1/pubsub/ws` | SDK | `pubsub.subscribe()` |

### Storage

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/storage/get/` | SDK | `storage.get()`, `storage.getBinary()` |
| `/v1/storage/pin` | SDK | `storage.pin()` |
| `/v1/storage/status/` | SDK | `storage.status()` |
| `/v1/storage/unpin/` | SDK | `storage.unpin()` |
| `/v1/storage/upload` | SDK | `storage.upload()` |

### Functions

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/functions` | CLI | Deploy and list functions. `orama function deploy` / `list`. |
| `/v1/functions/` | CLI | Per-function management: info, delete, versions, logs, secrets, triggers, and the streaming invoke socket. `orama function …`. |
| `/v1/invoke/` | SDK | `functions.invoke()` |
| `/v1/serverless/ws/connections` | internal | WebSocket connection registry for streaming invokes. |
| `/v1/serverless/ws/connections/` | internal | One WebSocket connection by id. |

### Network and proxy

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/network/connect` | CLI | Topology mutation, admin-scoped. |
| `/v1/network/disconnect` | CLI | Topology mutation, admin-scoped. |
| `/v1/network/peers` | SDK | `network.peers()` |
| `/v1/network/status` | SDK | `network.status()` |
| `/v1/proxy/anon` | SDK | `network.proxyAnon()` |
| `/v1/proxy/tunnel` | direct | Raw CONNECT-style tunnelling through the anonymity proxy. Not a JSON call; the SDK has nothing to wrap. |

### Vault

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/vault/health` | direct | Aggregate guardian health. |
| `/v1/vault/pull` | direct | Retrieve a secret. Ed25519-signed per request. |
| `/v1/vault/push` | direct | Store a secret. Ed25519-signed per request; see docs/vault/SECURITY_MODEL.md. Deliberately not in `@debros/orama` — see chg-343. |
| `/v1/vault/status` | direct | Guardian count and threshold. |

### WebRTC

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/webrtc/rooms` | direct | Room listing for the SFU. |
| `/v1/webrtc/signal` | direct | SFU signalling. |
| `/v1/webrtc/turn/credentials` | direct | Short-lived TURN credentials. Consumed by a WebRTC stack, not by this SDK; the SDK would only pass them through. |

### Push notifications

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/namespace/push-credentials` | CLI | Push credential management. |
| `/v1/namespace/push-credentials/` | CLI | One push credential. |
| `/v1/push/config` | CLI | Per-namespace push credentials. `orama namespace push-credentials`. |
| `/v1/push/devices` | direct | Register a device for push. A mobile client concern; a native SDK owns it, not this one. |
| `/v1/push/devices/` | direct | One registered device. |
| `/v1/push/send` | CLI | Server-side send. Admin-scoped; a function or a backend calls it directly. |

### Namespace management

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/namespace/delete` | CLI | Destroy a namespace. |
| `/v1/namespace/keys` | CLI | Mint and list scoped API keys. `orama namespace keys`. |
| `/v1/namespace/keys/` | CLI | Revoke a key. |
| `/v1/namespace/list` | CLI | Namespaces owned by the calling wallet. |
| `/v1/namespace/rate-limit` | CLI | Per-namespace rate limit. |
| `/v1/namespace/status` | CLI | Provisioning progress, polled by `orama namespace create`. |
| `/v1/namespace/webrtc/disable` | CLI | `orama namespace webrtc disable`. |
| `/v1/namespace/webrtc/enable` | CLI | `orama namespace webrtc enable`. |
| `/v1/namespace/webrtc/status` | CLI | `orama namespace webrtc status`. |
| `/v1/namespace/webrtc/stealth/disable` | CLI | Stealth TURN. |
| `/v1/namespace/webrtc/stealth/enable` | CLI | Stealth TURN. See docs/STEALTH_TURN.md. |

### Deployments

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/deployments/delete` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/domains/add` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/domains/list` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/domains/remove` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/domains/verify` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/env` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/env/set` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/events` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/get` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/go/update` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/go/upload` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/list` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/logs` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/nextjs/update` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/nextjs/upload` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/nodejs/update` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/nodejs/upload` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/rollback` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/static/update` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/static/upload` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/stats` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |
| `/v1/deployments/versions` | CLI | Application deployment. `orama app deploy` and friends; an application does not deploy itself. |

### Application databases

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/db/sqlite/backup` | CLI | Per-application SQLite databases. `orama db …`. |
| `/v1/db/sqlite/backups` | CLI | Per-application SQLite databases. `orama db …`. |
| `/v1/db/sqlite/create` | CLI | Per-application SQLite databases. `orama db …`. |
| `/v1/db/sqlite/delete` | CLI | Per-application SQLite databases. `orama db …`. |
| `/v1/db/sqlite/list` | CLI | Per-application SQLite databases. `orama db …`. |
| `/v1/db/sqlite/query` | CLI | Per-application SQLite databases. `orama db …`. |

### Node and operator

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/node/command` | CLI | Operator command on a node. |
| `/v1/node/enroll` | CLI | A node joins with an invite token. `orama node install`. |
| `/v1/node/leave` | CLI | `orama node remove`. |
| `/v1/node/logs` | CLI | `orama node logs`. |
| `/v1/node/status` | CLI | `orama node status`. |
| `/v1/operator/invite` | CLI | Mint a node invite. `orama invite`. |
| `/v1/operator/node/register` | CLI | Record a node in the inventory. |
| `/v1/operator/nodes` | CLI | Fleet inventory. |

### Internal (node to node)

| Route | Owner | Notes |
|-------|-------|-------|
| `/v1/internal/acme/cleanup` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/acme/present` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/deployments/replica/rollback` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/deployments/replica/setup` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/deployments/replica/teardown` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/deployments/replica/update` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/join` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/namespace/repair` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/namespace/spawn` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/namespace/webrtc/disable` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/namespace/webrtc/enable` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/namespace/webrtc/status` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/ping` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/storage/evict` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/tls/check` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/wg/peer` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/wg/peer/remove` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
| `/v1/internal/wg/peers` | internal | Node-to-node over the WireGuard overlay. Never reachable by a client. |
