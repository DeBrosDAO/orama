# WebRTC Integration

Real-time voice, video, and data channels for Orama Network namespaces.

## Architecture

```
Client A                                     Client B
   │                                            │
   │  1. Get TURN credentials (REST)            │
   │  2. Connect WebSocket (signaling)          │
   │  3. Exchange SDP/ICE via SFU               │
   │                                            │
   ▼                                            ▼
┌──────────┐     UDP relay      ┌──────────┐
│   TURN   │◄──────────────────►│   TURN   │
│  Server  │   (public IPs)     │  Server  │
│  Node 1  │                    │  Node 2  │
└────┬─────┘                    └────┬─────┘
     │ WireGuard                     │ WireGuard
     ▼                               ▼
┌──────────────────────────────────────────┐
│              SFU Servers (3 nodes)        │
│  - WebSocket signaling (WireGuard only)  │
│  - Pion WebRTC (RTP forwarding)          │
│  - Room management                       │
│  - Track publish/subscribe               │
└──────────────────────────────────────────┘
```

**Key design decisions:**
- **TURN-shielded**: SFU binds only to WireGuard IPs. All client media flows through TURN relay.
- **`iceTransportPolicy: relay`** enforced server-side — no direct peer connections.
- **Opt-in per namespace** via `orama namespace enable webrtc`.
- **SFU on all 3 nodes**, **TURN on 2 of 3 nodes** (redundancy without over-provisioning).
- **Separate port allocation** from existing namespace services.

## Prerequisites

- Namespace must be provisioned with a ready cluster (RQLite + Olric + Gateway running).
- Command must be run on a cluster node (uses internal gateway endpoint).

## Enable / Disable

```bash
# Enable WebRTC for a namespace
orama namespace enable webrtc --namespace myapp

# Check status
orama namespace webrtc-status --namespace myapp

# Disable WebRTC (stops services, deallocates ports, removes DNS)
orama namespace disable webrtc --namespace myapp
```

### What happens on enable:
1. Generates a per-namespace TURN shared secret (32 bytes, crypto/rand)
2. Inserts `namespace_webrtc_config` DB record
3. Allocates WebRTC port blocks on each node (SFU signaling + media range, TURN relay range)
4. Spawns TURN on 2 nodes (selected by capacity)
5. Spawns SFU on all 3 nodes
6. Creates DNS A records pointing to TURN node public IPs: `turn.ns-{name}.{baseDomain}` (plain UDP/TCP TURN) and `turn-{name}.{baseDomain}` (single-label TLS host for TURNS, covered by the `*.{baseDomain}` wildcard cert)
7. Updates cluster state on all nodes (for cold-boot restoration)

### What happens on disable:
1. Stops SFU on all 3 nodes
2. Stops TURN on 2 nodes
3. Deallocates all WebRTC ports
4. Deletes TURN DNS records
5. Cleans up DB records (`namespace_webrtc_config`, `webrtc_rooms`)
6. Updates cluster state

## Client Integration (JavaScript)

### Authentication

All WebRTC endpoints require authentication. Use one of:

```
# Option A: API Key via header (recommended)
X-API-Key: <your-namespace-api-key>

# Option B: API Key via Authorization header
Authorization: ApiKey <your-namespace-api-key>

# Option C: JWT Bearer token
Authorization: Bearer <jwt>
```

### 1. Get TURN Credentials

```javascript
const response = await fetch('https://ns-myapp.orama-devnet.network/v1/webrtc/turn/credentials', {
  method: 'POST',
  headers: { 'X-API-Key': apiKey }
});

const { uris, username, password, ttl } = await response.json();
// uris: [
//   "turn:turn.ns-myapp.orama-devnet.network:3478?transport=udp",
//   "turn:turn.ns-myapp.orama-devnet.network:3478?transport=tcp",
//   "turns:turn-myapp.orama-devnet.network:5349"
// ]
// NOTE: plain UDP/TCP TURN uses the two-label host turn.ns-<ns>.<base>; TURNS
// (TLS) uses the SINGLE-label host turn-<ns>.<base>. Only a single-label host
// is covered by the *.<base> wildcard cert, so only it validates in browsers —
// the two-label host can present a self-signed cert only, which browsers reject.
// Both round-robin to the same TURN nodes.
// username: "{expiry_unix}:{namespace}"
// password: HMAC-SHA1 derived (base64)
// ttl: 86400 (seconds — 24h; the one-shot REST/host-fn credential is not
//            refreshed mid-call, so it must outlast any call, bugboard #155)
```

### 2. Create PeerConnection

```javascript
const pc = new RTCPeerConnection({
  iceServers: [{ urls: uris, username, credential: password }],
  iceTransportPolicy: 'relay'  // enforced by SFU
});
```

### 3. Connect Signaling WebSocket

```javascript
const ws = new WebSocket(
  `wss://ns-myapp.orama-devnet.network/v1/webrtc/signal?room=${roomId}&api_key=${apiKey}`
);

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  switch (msg.type) {
    case 'offer':     handleOffer(msg);     break;
    case 'answer':    handleAnswer(msg);    break;
    case 'ice-candidate': handleICE(msg);   break;
    case 'peer-joined':   handleJoin(msg);  break;
    case 'peer-left':     handleLeave(msg); break;
    case 'turn-credentials':
    case 'refresh-credentials':
      updateTURN(msg);  // SFU sends refreshed creds at 80% TTL
      break;
    case 'server-draining':
      reconnect();  // SFU shutting down, reconnect to another node
      break;
  }
};
```

### 4. Room Management (REST)

```javascript
const headers = { 'X-API-Key': apiKey, 'Content-Type': 'application/json' };

// Create room
await fetch('/v1/webrtc/rooms', {
  method: 'POST',
  headers,
  body: JSON.stringify({ room_id: 'my-room' })
});

// List rooms
const rooms = await fetch('/v1/webrtc/rooms', { headers });

// Close room
await fetch('/v1/webrtc/rooms?room_id=my-room', {
  method: 'DELETE',
  headers
});
```

## API Reference

### REST Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/webrtc/turn/credentials` | JWT/API key | Get TURN relay credentials |
| GET/WS | `/v1/webrtc/signal` | JWT/API key | WebSocket signaling |
| GET | `/v1/webrtc/rooms` | JWT/API key | List rooms |
| POST | `/v1/webrtc/rooms` | JWT/API key (owner) | Create room |
| DELETE | `/v1/webrtc/rooms` | JWT/API key (owner) | Close room |

### Signaling Messages

| Type | Direction | Description |
|------|-----------|-------------|
| `join` | Client → SFU | Join room |
| `offer` | Client ↔ SFU | SDP offer |
| `answer` | Client ↔ SFU | SDP answer |
| `ice-candidate` | Client ↔ SFU | ICE candidate |
| `leave` | Client → SFU | Leave room |
| `peer-joined` | SFU → Client | New peer notification |
| `peer-left` | SFU → Client | Peer departure |
| `turn-credentials` | SFU → Client | Initial TURN credentials |
| `refresh-credentials` | SFU → Client | Refreshed credentials (at 80% TTL) |
| `server-draining` | SFU → Client | SFU shutting down |

## Port Allocation

WebRTC uses a **separate port allocation system** from the core namespace ports:

| Service | Port Range | Protocol | Per Namespace |
|---------|-----------|----------|---------------|
| SFU signaling | 30000-30099 | TCP (WireGuard only) | 1 port |
| SFU media (RTP) | 20000-29999 | UDP (WireGuard only) | 500 ports |
| TURN listen | 3478 | UDP + TCP | shared per host |
| TURNS (TLS) | 5349 | TCP | shared per host |
| TURN relay | 49152-65535 | UDP | shared per host |

## TURN Topology

One TURN server runs per **host** (`orama-turn.service`), serving every namespace
allocated TURN on that node. TURN binds the well-known ports 3478/5349, which are
exclusive per host, so a process per namespace would mean only one namespace could
have TURN on a given node — the second crash-looped on bind.

The alternative, giving each namespace its own ports, was rejected: it puts TURN on
arbitrary high ports, which restrictive networks routinely block, and those are
exactly the networks whose users most need a relay.

Isolation is the per-tenant secret. The credential already carries the namespace, so
the shared server resolves each one to its own HMAC secret; a namespace it does not
serve is rejected rather than falling back to any default.

The tenant list lives in `/opt/orama/.orama/configs/turn.yaml` (mode 0600 — it holds
every tenant's HMAC secret) and is re-read by the running process (~15s). Namespaces
are added and removed without a restart, because restarting drops every tenant's
active relays on that host.

Relay allocations come from the host-wide range 49152-65535, which is also what the
firewall opens. Each namespace still gets its own 800-port block recorded in
`webrtc_port_allocations`; that block is the record of which namespaces hold TURN on
which node, not a per-tenant relay range — one process has one range.

`orama-turn.service` runs as the unprivileged `orama` user with
`CAP_NET_BIND_SERVICE` for ports 3478/5349, and `ProtectSystem=strict`. It reads its
config and Caddy's wildcard certificate and writes nothing.

## TURN Credential Protocol

- Credentials use HMAC-SHA1 with a per-namespace shared secret
- Username format: `{expiry_unix}:{namespace}`
- Password: `base64(HMAC-SHA1(shared_secret, username))`
- One-shot REST / `turn_credentials` host-fn TTL: 24h (`turn.DefaultCredentialTTL`).
  These paths mint once at call setup and are never refreshed, so the credential
  must outlast the whole call — a short TTL tore down relay-only media at expiry
  (bugboard #155).
- SFU signaling path TTL: per-namespace `turn_credential_ttl` (default 600s). The
  SFU proactively sends `refresh-credentials` over the signaling WebSocket at 80%
  of TTL, so a short TTL is safe there.
- Clients should update ICE servers on receiving refresh

## TURNS TLS Certificate

TURNS (port 5349) uses TLS and the client connects to the single-label host
`turn-{name}.{baseDomain}`. Certificate provisioning, in order:

1. **Wildcard reuse (primary)**: TURN presents Caddy's existing `*.{baseDomain}`
   wildcard cert (already provisioned for HTTPS). The single-label TLS host is
   covered by it, so no per-namespace ACME provisioning is needed and browsers
   validate the cert. The `orama-node` service reads the wildcard from Caddy's
   storage; the cert reloader hot-reloads renewals.
2. **Per-domain Let's Encrypt (fallback)**: If the wildcard is unavailable, TURN
   tries to provision a per-domain cert by appending to the Caddyfile. This path
   fails on nodes where `orama-node` runs `ProtectSystem=strict` (can't write
   `/etc/caddy`), so it is best-effort only.
3. **Self-signed (last resort)**: If neither works, a self-signed cert is
   generated with the node's public IP as SAN. Browsers reject it — TURNS is
   effectively unavailable until a valid cert is in place. The two-label host
   `turn.ns-{name}.{baseDomain}` can only reach this state (the wildcard doesn't
   cover it), which is why TURNS moved to the single-label host.

Caddy auto-renews Let's Encrypt certs at ~60 days. TURN serves the cert through a hot-reloading `GetCertificate` callback that polls the cert file every 60 seconds, so renewed certs are picked up in-process without a restart (a restart would drop every active relay).

## Role Reconciliation

TURN and SFU roles are recorded in `webrtc_port_allocations` — that table, not any
local file, is the authority for which node runs what. Every node runs a 60s
reconciler that keeps reality matching it:

| Step | What it does |
|---|---|
| Prune | Before anything else reads membership, removes `namespace_cluster_nodes` rows for members that are permanently gone (dns_nodes non-active and silent for 15+ minutes). Not WebRTC-specific and runs unconditionally for every locally-resident cluster, not just WebRTC-enabled ones. |
| Reallocate | One node per sweep (the lowest-sorted live member, elected deterministically with no lock) drops roles held by nodes that are no longer viable members and assigns them to current ones. Requires a strict majority of viable members to act, so a partitioned minority can never reshape roles. |
| Start | Starts TURN/SFU this node holds an allocation for but is not running. Backs off for 10 minutes after a failed start, so a crash-looping unit is not restarted every tick. |
| Stop | Stops TURN/SFU this node no longer holds — but only on a **clean** allocator read that returns nothing. An unreadable database means do nothing, never stop. |
| Advertise | Re-adds this node's TURN DNS records, but only when it both holds the allocation **and** is actually serving. |

Several properties are deliberate:

- **Revocation follows viable membership, not a raw heartbeat.** A node that
  misses a single heartbeat keeps its roles; a 120-second heartbeat gap must
  never move a relay. "Viable" means recorded in `namespace_cluster_nodes` AND
  (currently active OR last seen within the last 10 minutes) — a node down
  longer than that is excluded from role-holding even if its
  `namespace_cluster_nodes` row is still there. Both the viable set and its
  live subset are read from a **single** query (`webrtcViableMemberSQL`) so
  live is structurally guaranteed to be a subset of viable — an earlier
  version read them as two separate queries, so a node's status flipping
  between the two reads could land it in "live" without being in "viable",
  which the quorum math assumed could never happen (bugboard #170).
- **A stale row is eventually removed outright, not just excluded from a
  sweep.** Node replacement and cluster repair are both supposed to remove a
  departed node's `namespace_cluster_nodes` row, but a #161/#173 postmortem
  found rows left behind indefinitely on live devnet: cluster repair only
  ever *added* members, and the row's only other removal path
  (`removeClusterNodeAssignment`, reachable through `ReplaceClusterNode`)
  only fires when the ring-based dead-node health monitor confirms a node
  dead by quorum — which a genuinely-dead node can permanently evade. The
  DNS heartbeat loop (`startDNSHeartbeat`, 30s tick) flips a silent node's
  `dns_nodes.status` to `inactive` after just 120s
  (`cleanupStaleNodeRecords`), and the ring monitor's neighbor discovery
  only considers `status = 'active'` nodes as probe targets — so a node that
  flips inactive drops out of every observer's neighbor set before the
  monitor's own 12-miss (~120s) dead threshold is reached, its accumulated
  miss count is discarded on the next prune, and it can never again reach
  quorum-confirmed death. On devnet this produced an unbreakable 50/50 split
  (2 of 4 recorded members permanently dead) that the old raw-membership
  quorum check could never pass. The reconciler's Prune step above now
  removes such a row directly from `dns_nodes` staleness (15-minute
  horizon, deliberately looser than the 10-minute role-viability grace so
  removing the row gets extra margin over merely excluding a role), and
  `RepairCluster` does the same before counting how many nodes are missing —
  independent of whether the ring monitor ever confirms death.
- **A lone survivor after a mass outage does not self-elect.** Once live and
  viable are both derived from the same signal, a single node always
  satisfies the plain majority check (`live*2 > viable`, since a lone viable
  node trivially outnumbers itself). A second, independent check requires the
  viable set to still represent a majority of every *raw* recorded member
  (`viable >= (raw+1)/2`) — so a cluster that goes quiet for the reconciler's
  10-minute grace window and then has one node report back in first does not
  treat that node as the entire cluster and strip the others' roles the
  moment they're a minute late (bugboard #171). A newly-restarted node is
  also held out of coordination for a 5-minute startup grace, so its very
  first read — before peers have had a chance to report back in — can't look
  like a mass outage either.
- **Stopping requires positive evidence.** Starting a service is backed off and
  can fail; stopping is immediate. A reconciler whose stop path is more capable
  than its start path can only ever reduce capacity, so the stop path is the
  conservative one.
- **A skipped sweep is always logged.** "No viable members", "no quorum",
  "majority of recorded membership is not viable", "startup grace", and "not
  the elected coordinator" each log their reason (namespace, and whatever
  counts drove the decision) — the original #161 fix had two silent
  early-returns here, which is exactly why the deadlock above went unnoticed
  for weeks.

Without this, replacing a node left its TURN/SFU roles behind: the namespace kept
two TURN allocations where one belonged to a machine that no longer existed, and
the replacement node held no role at all (bugboard #161). If viable members can
never reach the desired TURN/SFU count (fewer viable nodes than the namespace's
configured `turn_node_count`), the reconciler allocates to every viable member it
has instead of doing nothing, and logs the shortfall at Info (an expected steady
state for small clusters, not a per-sweep warning).

## Monitoring

```bash
# Check WebRTC status
orama namespace webrtc-status --namespace myapp

# Monitor report includes SFU/TURN status
orama monitor report --env devnet

# Inspector checks WebRTC health
orama inspector --env devnet
```

The monitoring report includes per-namespace `sfu_up` and `turn_up` fields. The inspector runs cross-node checks to verify SFU coverage (3 nodes) and TURN redundancy (2 nodes).

`turn_up` is true when this host's shared TURN server is running **and** lists the
namespace as a tenant — TURN is host-level, so the unit being up does not by itself
mean it relays for a given namespace.

## Debugging

```bash
# SFU logs
sudo orama node logs orama-namespace-sfu@myapp -f

# TURN logs — one shared server per host, serving every namespace on it
sudo orama node logs turn -f

# The node's units, with their state
sudo orama node status

# The shared TURN unit is host-level, so it is not in that list yet
systemctl status orama-turn
```

## Security Model

- **Forced relay**: `iceTransportPolicy: relay` enforced server-side. Clients cannot bypass TURN.
- **HMAC credentials**: Per-namespace TURN shared secret. REST/host-fn credentials expire after 24h (long enough to outlast any call, since they are not refreshed mid-call); SFU-signaled credentials use the shorter per-namespace TTL and are refreshed over the signaling channel.
- **Namespace isolation**: Each namespace has its own TURN secret, port ranges, and rooms.
- **Authentication required**: All WebRTC endpoints require API key or JWT (`X-API-Key` header, `Authorization: ApiKey`, or `Authorization: Bearer`).
- **Room management**: Creating/closing rooms requires namespace ownership.
- **SFU on WireGuard only**: SFU binds to 10.0.0.x, never 0.0.0.0. Only reachable via TURN relay.
- **Permissions-Policy**: `camera=(self), microphone=(self)` — only same-origin can access media devices.

## Firewall

When WebRTC is enabled, the following ports are opened via UFW on TURN nodes:

| Port | Protocol | Purpose |
|------|----------|---------|
| 3478 | UDP | TURN standard |
| 3478 | TCP | TURN TCP fallback (for clients behind UDP-blocking firewalls) |
| 5349 | TCP | TURNS — TURN over TLS (encrypted, works through strict firewalls/DPI) |
| 49152-65535 | UDP | TURN relay range (allocated per namespace) |

SFU ports are NOT opened in the firewall — they are WireGuard-internal only.

## Database Tables

| Table | Purpose |
|-------|---------|
| `namespace_webrtc_config` | Per-namespace WebRTC config (enabled, TURN secret, node counts) |
| `webrtc_rooms` | Room-to-SFU-node affinity |
| `webrtc_port_allocations` | SFU/TURN port tracking |

## Cold Boot Recovery

On node restart, the cluster state file (`cluster_state.json`) includes `has_sfu`, `has_turn`, and port allocation data. The restore process:

1. Core services restore first: RQLite → Olric → Gateway
2. If `has_turn` is set: fetches TURN shared secret from DB, spawns TURN
3. If `has_sfu` is set: fetches WebRTC config from DB, spawns SFU with TURN server list

If the DB is unavailable during restore, SFU/TURN restoration is skipped with a warning log. They will be restored on the next successful DB connection.
