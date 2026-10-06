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

# Disable WebRTC (tears down the SFU, drops the namespace from the shared TURN, deallocates ports, removes DNS)
orama namespace disable webrtc --namespace myapp
```

### What happens on enable:
1. Generates a per-namespace TURN shared secret (32 bytes, crypto/rand)
2. Inserts `namespace_webrtc_config` DB record
3. Allocates WebRTC port blocks on each node (SFU signaling + media range, TURN relay range)
4. Spawns TURN on 2 nodes (selected by capacity)
5. Spawns SFU on all 3 nodes
6. Asks every other TURN host to apply its tenant set now (`reconcile-host-turn`) and creates DNS A records pointing to the public IPs of the TURN nodes whose RUNNING server confirmed it serves the namespace, this node included: a host answers only once `orama-turn` is active and the server's published tenant set (`served-tenants.json`, below) matches the config it wrote and lists the namespace, waiting up to 6s for the server's ~2s reload (a node that did not confirm is advertised by its own sweep once it serves; enabling fails if none confirmed). If enabling fails after that, every TURN host that was asked is told to drop the namespace at once (`reconcile-host-turn` with `release`, under a fresh 30s context so a cancelled enable cannot skip it), so none keeps the namespace in its shared TURN config until the next sweep; hosts that cannot be reached are logged and drop it on their next sweep: `turn.ns-{name}.{baseDomain}` (plain UDP/TCP TURN) and `turn-{name}.{baseDomain}` (single-label TLS host for TURNS, covered by the `*.{baseDomain}` wildcard cert)
7. Updates cluster state on all nodes (for cold-boot restoration)

### What happens on disable:
0. Marks the namespace's `namespace_webrtc_config` row disabled (`enabled = 0`) **before** any unit is stopped. Every node's reconciler starts the units of a namespace whose config says enabled, and the SFU takes up to 45s to stop (it drains for 30s; `TimeoutStopSec=45s`): a start issued meanwhile cancelled the stop job and the teardown failed with "Job canceled". If this write fails nothing has been stopped and the disable is refused. A reconciler already past its config read is held off by the namespace's lock (`SystemdSpawner.LockNamespace`), which `TeardownSFU` and `TeardownTURN` hold from the stop to the removal of the config.
1. Tears down the SFU on every cluster node, **all nodes at once** (`teardownWebRTCConcurrently`; the legacy per-namespace TURN unit of step 2 is torn down in the same pass), and restarts the namespace gateways concurrently too. The request lasts as long as the slowest drain, not the sum of them. Run one node after another it took 117s on three nodes (30s drain each plus the gateway restarts), past the 120s `WriteTimeout` of the gateway's HTTP server, which dropped the connection with no response: Caddy logged `EOF` and the CLI printed `HTTP 502: null`. The worst case now is one stop (up to `stopWaitDeadline`, 90s) plus the gateway restarts, still inside 120s for the normal 30s drain. Every unit is attempted, and a failure never cancels the others. On each node: the unit is stopped **and disabled**, and its env file and `sfu-<node>.yaml` config (which holds the TURN secret) are removed. Stopping alone left the unit enabled with its env file, and `orama node upgrade` restarts every unit it finds, so a namespace that had turned WebRTC off got its SFU back. Locally this is `SystemdSpawner.TeardownSFU`; on a remote node the `teardown-sfu` spawn action (`stop-sfu` keeps its restart meaning). A stop whose command returns while the unit is still `deactivating` is waited for (polling systemd's state, up to `stopWaitDeadline`, 90s, above the SFU's 45s `TimeoutStopSec`) rather than reported failed; only a unit still deactivating at the deadline, or active again, is a failed stop.
2. Does **not** stop TURN. TURN is one shared server per host (`orama-turn.service`) used by every namespace on that host, so disabling WebRTC for one namespace must not touch it. The namespace leaves the shared server's tenant list (its credentials, realm and stealth host) when `ReconcileHostTURN` rewrites the shared config after the allocation and WebRTC config are deleted; the running process re-reads the list without a restart, and the host TURN stops only when its last tenant leaves. The disabling node does this immediately; every other node on its next WebRTC reconcile sweep (60s). The only per-namespace TURN unit that can still exist is the pre-shared `orama-namespace-turn@<ns>`; the `teardown-turn` spawn action retires it (stop, disable, env file removed). `stop-turn` only ever addressed that legacy unit.
3. Deallocates the WebRTC ports of every unit that is gone. A unit whose teardown failed (node unreachable, or the unit still deactivating when `stopWaitDeadline` ran out) **keeps its allocation row**: the unit still holds those ports, and freeing the row let the next namespace be handed the same ones, whose SFU then crash-looped on "address already in use". Running the disable again finishes such a namespace (it is accepted while allocations remain, even though the config is already disabled), and a teardown replayed later from `namespace_pending_cleanup` frees the row it was owed for. The same rule holds for a rolled-back enablement and for deleting the namespace.
4. Deletes TURN DNS records
5. Cleans up DB records (`namespace_webrtc_config`, `webrtc_rooms`)
6. Updates cluster state

Steps 3 to 5 are not fire-and-forget: a port deallocation, a DNS deletion or a DB delete that fails is returned with the other cleanup failures, because a config row that survives leaves WebRTC looking enabled.

A node that cannot be reached, or that is still on a release without `teardown-sfu`/`teardown-turn` (it answers "unknown action"), is not skipped silently: the failed teardown is recorded in `namespace_pending_cleanup` with the cluster id it was owed for and replayed by the tenant reconciler, and `DisableWebRTC` returns the failures after completing the rest. The replay is dropped, not sent, when the registry shows another cluster of that namespace on that node (a namespace deleted and created again), and allocating SFU or TURN ports on a node deletes the `teardown-sfu`/`teardown-turn` still owed there, so a late replay cannot remove the SFU that was just started. A failed enablement is rolled back the same way, so a half-enabled namespace is not resurrected by an upgrade either.

## Client Integration (JavaScript)

### Authentication

Every WebRTC endpoint requires a **signed-in user's token**, not an API key:

```
Authorization: Bearer <access token>
```

An API key on its own is refused. WebRTC is a layer-1 route — it needs a genuine
logged-in user, or a deployed app holding its own workload token (docs/AUTH.md
"A workload's identity") — which is what makes a runtime key extracted from an app
bundle worthless against it. The SDK gets that token by exchanging your key, or from
`auth.verify()` after a wallet signs in; `X-API-Key` and `Authorization: ApiKey`
are the deprecated spellings and are going away.

### 1. Get TURN Credentials

```javascript
const response = await fetch('https://ns-myapp.orama-devnet.network/v1/webrtc/turn/credentials', {
  method: 'POST',
  headers: { Authorization: `Bearer ${accessToken}` }
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
// the two-label host is not covered by it, so TLS to it fails in browsers.
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
  // A browser cannot set a header on a WebSocket upgrade, so the credential
  // goes in the query string — and it is a short-lived token, never a key: a
  // query string ends up in the access log, the Referer of the next request
  // the page makes, and history.
  `wss://ns-myapp.orama-devnet.network/v1/webrtc/signal?room=${roomId}&jwt=${encodeURIComponent(accessToken)}`
);
// `room` is optional but recommended. With it the gateway routes the upgrade
// straight to the room's SFU; without it the gateway reads your first (join)
// frame and routes by its roomId (see Room Placement). When both are present
// they must match: a join for a different room is refused with `room_mismatch`.

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  switch (msg.type) {
    case 'offer':     handleOffer(msg);     break;
    case 'answer':    handleAnswer(msg);    break;
    case 'ice-candidate': handleICE(msg);   break;
    case 'peer-joined':   handleJoin(msg);  break;
    case 'peer-left':     handleLeave(msg); break;
    case 'turn-credentials':      // sent once, on join
      setTURN(msg);
      break;
    case 'refresh-credentials':   // sent by the SFU at 80% of the credential TTL
      updateTURN(msg);
      break;
    case 'server-draining':
      reconnect();  // SFU shutting down, reconnect to another node
      break;
  }
};
```

### 4. Room Management (REST)

```javascript
const headers = { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' };

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
| `turn-credentials` | SFU → Client | Initial TURN credentials, sent once after `welcome` |
| `refresh-credentials` | SFU → Client | Replacement credentials, sent at 80% of the TTL (same `{ username, password, ttl, uris }` payload). Never sent as `turn-credentials` |
| `server-draining` | SFU → Client | SFU shutting down |

## Room Placement

A room lives in exactly one SFU process (rooms are in that process's memory), so
every peer of a call must reach the same SFU. Clients resolve the namespace host
round-robin, so peers of one room arrive at different nodes' gateways; each
gateway therefore routes the socket to the room's SFU, not to its own node's.

The gateway learns the room one of two ways. With `?room=<roomId>` on the URL
it decides at the upgrade and pipes the socket through untouched. Without it
(existing clients) the gateway accepts the upgrade itself, waits up to 5 s for
the first frame, requires it to be a text `join` frame of at most 4096 bytes
with a `userId` and a valid `roomId`, then dials the owning SFU (naming the room
in the URL, so the SFU's check applies), replays that frame verbatim and copies
frames both ways until either side closes. A first frame that is late,
oversized, not JSON, not a join, or names an invalid room gets an `error` frame
(`invalid_join`) and a close; nothing reaches an SFU. If no SFU can take the
room the client gets `no_sfu` (or `sfu_unreachable`) and a close. Either way the
owner is chosen as follows:

1. List the namespace's SFU nodes: the `sfu` rows of `webrtc_port_allocations`
   on nodes `dns_nodes` still counts as active (cluster registry, cached 10 s).
2. Rank them by rendezvous (highest-random-weight) hash of
   `namespace|room|nodeID`. Every gateway computes the same order.
3. Probe each in parallel with `GET /health?room=<roomId>` (1.5 s timeout). An
   SFU that does not answer, or answers 503 (draining), is skipped. A healthy
   one reports `hasRoom`: whether the room has participants on it.
4. The owner is the first healthy node in rank that already hosts the room;
   with no live room, the top-ranked healthy node.
5. Proxy the socket to `<owner WireGuard IP>:<signalling port>`.

Why computed and not recorded: nothing is written on the join path, so there is
no lease to expire, no stale row naming a dead node, and no registry write per
join. The cost is O(SFU nodes) small health requests over WireGuard per join
(three by design; SFU count is bounded by the namespace's node count, not by the
number of rooms or peers), so joins scale with peers, not with cluster state.
The alternative (a conditional insert into the namespace's `webrtc_rooms`) puts a
Raft write on every first join and needs heartbeats to expire ownership when the
owner dies; it was rejected for that.

Behaviour on change:

| Event | Result |
|---|---|
| Two gateways, same room, at once | Both rank identically and both find no live room, so both pick the same node |
| Owner SFU stops or drains | It fails its probe. `server-draining` (or the closed socket) sends peers back; each reconnect, through any gateway, lands on the same next-ranked healthy node |
| Owner comes back while the call lives elsewhere | Not taken back: the node hosting the room wins over rank, so a running call is never split by a recovery |
| SFU added or removed | Only rooms whose top rank changed can move; a live room stays on the node hosting it (rule 4), so adding a node never splits a running call. Rooms hosted on a removed node re-home as in the row above |
| No SFU registered / none healthy / registry unreadable | 503 with the reason. There is no fallback to the local SFU, which would split the call |

Limits: a network partition between one gateway and the owner makes that
gateway rank the owner down and pick the next node, splitting the room for the
duration; peers reconverge on reconnect once the probe views agree. During a
rolling upgrade an old gateway still proxies to its own node's SFU; a new
gateway finds any room already live on an SFU, so calls started by old gateways
are joined, not duplicated (an old SFU ignores `?room=` and reports no room, so
new gateways then rely on rank alone).

Room ids are 1 to 128 printable ASCII characters (no whitespace or control
characters); the gateway and the SFU apply the same rule
(`core/pkg/sfu/roomid`). A bad `?room=` is a 400.

Joins are limited per signed-in identity (the wallet subject), 60 a minute with a
burst of 20; over it the gateway answers 429 with `Retry-After`. One call is one
socket, and the burst covers a reconnect after an SFU drains.

`GET /v1/webrtc/rooms` still reports the health of the SFU on the gateway's own
node only.

## Port Allocation

WebRTC uses a **separate port allocation system** from the core namespace ports:

| Service | Port Range | Protocol | Per Namespace |
|---------|-----------|----------|---------------|
| SFU signaling | 30000-30099 | TCP (WireGuard only) | 1 port |
| SFU media (RTP) | 20000-29999 | UDP (WireGuard only) | 500 ports |
| TURN listen | 3478 | UDP + TCP | shared per host |
| TURNS (TLS) | 5349 | TCP | shared per host |
| TURN relay | 49152-65535 | UDP | shared per host |

Two allocations on one node can never hold the same port: `webrtc_port_allocations` carries unique indexes on `(node_id, sfu_signaling_port)` and `(node_id, sfu_media_port_start)` for SFU rows and `(node_id, turn_relay_port_start)` for TURN rows (migration 067). The allocator picks the next free value from the rows it reads; an insert that loses a race for the same value hits the index, and the allocator retries against a fresh read. Any other writer gets the constraint error.

The registry is the single place ports are decided, so a unit that is running must have a row. A node's WebRTC reconciler records the ports of its SFU config (`namespaces/<ns>/configs/sfu-<node>.yaml`) for a namespace the registry says has WebRTC enabled and that lists this node as a member, when no row exists for it (`backfillSFUAllocation`). A config that is not one of the allocator's 500-port blocks, a row that names other ports, or ports another cluster's row already holds are logged as errors and the unit is left alone: stopping it would stop the namespace that legitimately holds the ports. A running SFU of a namespace that has WebRTC disabled, or no WebRTC config at all, gets no row: the reconciler stops it on the same clean-read evidence as any unallocated unit (enabling writes the config row and the allocation before it spawns, so a unit mid-enablement is never taken for one). A namespace that no longer exists has no cluster for its state file to match, so its leftover units are removed on the node.

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

The tenant list lives in `/opt/orama/.orama/data/turn/turn.yaml` (mode 0600 — it holds
every tenant's HMAC secret) and is re-read by the running process (~2s). Namespaces
are added and removed without a restart, because restarting drops every tenant's
active relays on that host. The process reads and hashes the file each tick and rebuilds only when the content
changed, and logs a failed reload once per distinct error.

After every successful load the process writes `/run/orama-turn/served-tenants.json`
(its `RuntimeDirectory`, mode 0750, removed when the unit stops; namespaces, the
SHA-256 of the config it loaded; no secrets). A starting
process deletes any leftover before its first load. That file, not the
config, is what `reconcile-host-turn` waits on: a written config only means the server
will serve a namespace on its next tick.

The per-namespace `orama-namespace-turn@<ns>` units this replaced are retired on
every WebRTC sweep: a unit that still has its env file, or is in any state but
inactive (running, starting, restarting or failed), is stopped, disabled and its env
file removed. A legacy unit whose config the migration deleted crash-loops and does
not read as active, so the active state alone missed exactly those units; without the
env file's removal every boot and every `orama node upgrade` would start the unit
again. A cluster state whose namespace name is not valid is skipped.

Relay allocations come from the host-wide range 49152-65535, which is also what the
firewall opens. Each namespace still gets its own 800-port block recorded in
`webrtc_port_allocations`; that block is the record of which namespaces hold TURN on
which node, not a per-tenant relay range — one process has one range.

`orama-turn.service` runs as the unprivileged `orama` user with
`CAP_NET_BIND_SERVICE` for ports 3478/5349, and `ProtectSystem=strict`. It reads its
config and Caddy's wildcard certificate, and writes only `served-tenants.json`, into its `RuntimeDirectory` (`/run/orama-turn`).

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
  of TTL (the initial credential arrives as `turn-credentials`), so a short TTL is
  safe there.
- Clients should update ICE servers on receiving `refresh-credentials`

## TURNS TLS Certificate

TURNS (port 5349) uses TLS and the client connects to the single-label host
`turn-{name}.{baseDomain}`. The shared TURN server presents Caddy's existing
`*.{baseDomain}` wildcard cert (already provisioned for HTTPS), which covers
every tenant's single-label TURNS host and stealth host, so browsers validate
it. `orama-node` reads the wildcard from Caddy's storage
(`/var/lib/caddy/caddy/certificates/<issuer>/wildcard_.{baseDomain}/`).

There is no other source. If the wildcard is not on disk, TURNS stays off and
the node logs why; clients keep plain TURN on 3478. A self-signed cert is never
served — browsers reject it, and for a stealth host a rejected cert is
indistinguishable from being blocked. The two-label host
`turn.ns-{name}.{baseDomain}` is not covered by the wildcard, which is why
TURNS uses the single-label host.

Caddy auto-renews Let's Encrypt certs at ~60 days. TURN serves the cert through a hot-reloading `GetCertificate` callback that polls the cert file every 60 seconds, so renewed certs are picked up in-process without a restart (a restart would drop every active relay).

## Role Reconciliation

TURN and SFU roles are recorded in `webrtc_port_allocations` — that table, not any
local file, is the authority for which node runs what. Every node runs a 60s
reconciler that keeps reality matching it:

| Step | What it does |
|---|---|
| Prune | Before anything else reads membership, removes `namespace_cluster_nodes` rows for members that are permanently gone (dns_nodes non-active and silent for 15+ minutes). Not WebRTC-specific and runs unconditionally for every locally-resident cluster, not just WebRTC-enabled ones. |
| Reallocate | One node per sweep (the lowest-sorted live member, elected deterministically with no lock) drops roles held by nodes that are no longer viable members and assigns them to current ones. Requires a strict majority of viable members to act, so a partitioned minority can never reshape roles. |
| Record | Writes the allocation row for an SFU this node runs from a config but has no row for (see Port Allocation). |
| Start | Starts the SFU this node holds an allocation for when its unit is stopped (`inactive` or `failed`). A unit that is `activating`, `deactivating` or `reloading` is left alone, and so is one whose state cannot be read: a stop in flight is a teardown being carried out, and a start would cancel it. Runs under the namespace's lock and re-reads the WebRTC config under it. Backs off for 10 minutes after a failed start, so a crash-looping unit is not restarted every tick. |
| Stop | Retires the SFU this node no longer holds: stopped, disabled, env file and `sfu-<node>.yaml` removed (`SystemdSpawner.retireSFU`, the same as `TeardownSFU`). That covers an SFU left behind after its WebRTC was disabled or rolled back. It applies to a unit in any state but inactive (a crash-looping or failed unit too: systemd restarts an enabled `Restart=always` unit), and to an inactive one whose env file is still there. `orama node status` and `orama node upgrade` take a service to be provisioned by its env file, so a stopped, disabled SFU with one was listed as an inactive service (`orama node status` printed "32 of 33 running") and would be enabled and started by the next upgrade. A unit or env file that cannot be read is logged and left alone. It acts only on a **clean** allocator read that returns nothing: an unreadable database means do nothing, never stop. The retirement is taken under the namespace's lock (`SystemdSpawner.LockNamespace`) and the allocation is read again under it. `SpawnSFU` (an enable here, or the `spawn-sfu` request from the node that coordinates it) takes the same lock around its config, env file and start, so an enable is either finished before the sweep's locked read, which then sees the allocation and leaves the unit alone, or begins after the retirement and writes everything afresh. |
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
orama inspect --env devnet
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
- **A logged-in user, not a key**: every WebRTC endpoint requires a wallet token, or a deployed app's own workload token (`Authorization: Bearer`). An API key alone is refused, which is what makes a runtime key extracted from an app bundle worthless here. On the signalling WebSocket the token goes in `?jwt=`, because a browser cannot set a header on an upgrade (`?token=` is read as an API key, which these endpoints refuse).
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
| 443 | TCP | Stealth TURNS: the SNI router hands TURN-over-TLS clients to the namespace's TURNS listener ([STEALTH_TURN.md](STEALTH_TURN.md)); open already for HTTPS |
| 49152-65535 | UDP | TURN relay range (allocated per namespace) |

SFU ports are NOT opened in the firewall — they are WireGuard-internal only.

## Database Tables

| Table | Purpose |
|-------|---------|
| `namespace_webrtc_config` | Per-namespace WebRTC config (enabled, TURN secret, node counts) |
| `webrtc_rooms` | Not used: room placement is computed (see Room Placement), never recorded. Emptied when WebRTC is disabled |
| `webrtc_port_allocations` | SFU/TURN port tracking |

## Cold Boot Recovery

On node restart, the cluster state file (`cluster_state.json`) includes `has_sfu`, `has_turn`, and port allocation data. The restore process:

1. Core services restore first: RQLite → Olric → Gateway
2. If `has_turn` is set: fetches TURN shared secret from DB, spawns TURN
3. If `has_sfu` is set: fetches WebRTC config from DB, spawns SFU with TURN server list

If the DB is unavailable during restore, SFU/TURN restoration is skipped with a warning log. They will be restored on the next successful DB connection.
