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

### 4. Rooms (REST)

Rooms are not created or closed over REST. A room exists once the first peer joins it
over the signalling WebSocket, and an empty room is cleaned up by the SFU after 60
seconds (`emptyRoomTTL`). The only room endpoint is a read-only health read:

```javascript
const headers = { Authorization: `Bearer ${accessToken}` };

// SFU health: status and room count (see "Room Placement" for which SFU answers)
const health = await fetch('/v1/webrtc/rooms', { headers });
```

## API Reference

### REST Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/webrtc/turn/credentials` | JWT/API key | Get TURN relay credentials |
| GET/WS | `/v1/webrtc/signal` | JWT/API key | WebSocket signaling |
| GET, PUT | `/v1/webrtc/config` | Namespace settings grant (owner, admin) | The namespace's WebRTC policy: `{"require_admission": bool}` (see "Admission") |
| GET | `/v1/webrtc/rooms` | Wallet token or app workload token | The SFU's health JSON (status, room count); any other method is a 405 |

### Signaling Messages

| Type | Direction | Description |
|------|-----------|-------------|
| `join` | Client → SFU | Join room |
| `offer` | Client ↔ SFU | SDP offer (see "Negotiation: offer glare") |
| `answer` | Client ↔ SFU | SDP answer |
| `ice-candidate` | Client ↔ SFU | ICE candidate |
| `leave` | Client → SFU | Leave room |
| `audio-state`, `video-state` | Client → SFU | The client turned its audio or video on or off: `{"enabled": bool}` (see "Audio and video state") |
| `participant-state` | SFU → Client | Another participant's audio or video state, or a mute the namespace applied |
| `kicked` | SFU → Client | The SFU removed you from the room; the socket closes right after. `data.code` is `removed` (the namespace kicked you) or `admission_expired` (the admission you joined on ended) |
| `peer-joined` | SFU → Client | New peer notification |
| `peer-left` | SFU → Client | Peer departure |
| `turn-credentials` | SFU → Client | Initial TURN credentials, sent once after `welcome` |
| `refresh-credentials` | SFU → Client | Replacement credentials, sent at 80% of the TTL (same `{ username, password, ttl, uris }` payload). Never sent as `turn-credentials` |
| `server-draining` | SFU → Client | SFU shutting down |

### Negotiation: offer glare and the polite SFU

The SFU offers whenever it has something to tell a client (a track to subscribe to, a track removed, an
ICE restart) and the client offers when it publishes, so both can offer at once ("glare"). The SFU is
the **polite** peer, so a client does not have to implement rollback (iOS WebRTC's breaks audio): if a
client `offer` arrives while the SFU's own offer is outstanding, the SFU withdraws its own, answers the
client's, and offers again once that is done, with everything the withdrawn offer carried. The client
treats it as normal perfect negotiation with itself impolite: ignore an SFU `offer` that arrives while
its own is outstanding, and apply the SFU's next one after the SFU's `answer`. No `offer_failed` is
sent for a glare.

Details a client can observe:

- Every SDP exchange of one peer is serialized in the SFU, so an offer and an answer never interleave.
- The SFU gives its own m-lines mids of the form `sfu<N>` (`sfu1`, `sfu2`, ...), never numeric ones, so
  a mid the client picks for a new m-line of its own can never collide with one the SFU picked in an
  offer the client has not seen. Do not assume numeric mids or contiguous mids.
- pion (the SFU's WebRTC stack, v4.2.22) has no rollback: its signaling state machine allows leaving
  `have-local-offer` only through an answer. The SFU therefore yields by answering its own outstanding
  offer with a stand-in answer that accepts it unchanged and carries the client's ICE credentials and
  DTLS fingerprint (`pkg/sfu/glare.go`), then applies the client's offer. When pion grows rollback,
  `yieldToClientOfferLocked` becomes a `SetLocalDescription(rollback)`.
- The stand-in answer also fixes the DTLS role when glare hits before any answer did (the very first
  SFU offer). The role is derived, not fixed: once a negotiation completed the stand-in keeps the
  established role; on the first one it follows the client's crossing offer: `a=setup:actpass` or
  `passive` makes the SFU the DTLS client (stand-in `passive`), `a=setup:active` makes the SFU the DTLS
  server (stand-in `active`), so the SFU's later answer to that offer agrees (`standInSetup`).

### Signaling rate limit

Each peer has a signaling allowance (`pkg/sfu/ratelimit.go`), because an `offer` or an `ice-candidate`
makes the SFU do work and a client that offers without ever answering makes it build a throwaway
PeerConnection per glare. Offers and ICE candidates share one token bucket (burst 40, refilled at 10
per second), and a peer may make the SFU yield its own offer to glare at most 6 times per minute. A peer
that exceeds either gets an `error` frame with code `rate_limited` and its socket is closed; it can
rejoin. Ordinary negotiation, including trickle ICE on connect, stays well inside the allowance.

### Keyframes (PLI/FIR)

A subscriber that needs a keyframe (it joined mid-stream, or lost packets) sends PLI or FIR on the
`RTPSender` of the track it receives. The SFU reads that RTCP for every subscribed track and relays a
PLI to the publisher of the track. PLIs for one track are at least 500 ms apart
(`keyframeMinInterval`): a request inside the interval is deferred to its end, and requests that arrive
while one is deferred share it, so N subscribers cost the publisher one keyframe per interval, not N.
After a subscriber joins, the SFU also asks the publishers of the video tracks it was given for a
keyframe once its negotiation has settled (300 ms). Reading the senders' RTCP also drains their RTCP
buffers, which the NACK responder needs.

### Signaling socket failover

`/v1/webrtc/signal` reaches the namespace through the cluster gateway, which picks one of the
namespace's gateways. If the dial to that member fails (its gateway is restarting, its node is down)
nothing was sent, so the cluster gateway tries the next member whose circuit breaker allows it, exactly
as for an HTTP request. Any namespace gateway can serve the upgrade: each one routes the room to the SFU
that owns it (see Room Placement). A tunnel that was established and then ended is not retried.

When no member can be dialed the client gets HTTP `503` with the typed envelope

```json
{"ok": false, "error": {"code": "NAMESPACE_GATEWAY_UNAVAILABLE", "message": "...", "retryable": true}}
```

and should reconnect with backoff. A WebSocket route that has only one backend (not a namespace
gateway) answers a failed dial with `SERVICE_UNAVAILABLE`, also `retryable`.

### Peer lifecycle

A peer ends exactly once, whichever side ends it: the signaling socket closing, `leave`, ICE failing,
ICE not recovering within 15 s of `disconnected`, or a signaling write failing. Ending it releases the
PeerConnection (and with it the TURN allocation), the socket, and the goroutines tied to the peer.
Signaling writes have a 5 s deadline (`wsWriteTimeout`) and are made outside the room's lock, so a
client that stopped reading is disconnected after 5 s and does not stall the room. One subscriber's
failing RTP write never stops the forwarding of a track to the others.

## Identity, admission and moderation

A room's participants are the users the namespace's gateway authenticated, a namespace can decide who may
join, and its functions can remove or mute a participant. None of it is in the client's hands: the SFU
takes every one of these decisions from a ticket the namespace's gateway signs, never from a frame the
client sends.

### Identity

The peer's identity in a room is the **subject of the token the gateway authenticated** (a wallet, or a
deployed app's workload) and, when the session is bound to a device, the **device id** (the `did` claim).
They are what `welcome`, `participant-joined`, `track-added` and the membership events name
(`userId`, `deviceId`).

The `userId` in the client's `join` frame is **ignored** (and no longer required): a client cannot say who
it is. A client that kept sending one sees the authenticated id come back instead.

How the SFU can trust it: the SFU listens on the WireGuard overlay, where every namespace's services are, so
being on the overlay proves nothing. The namespace gateway signs a **join ticket** (`pkg/sfu/ctrlauth`) and
sends it to the SFU in the `X-Orama-SFU-Ticket` header of the signalling upgrade: the namespace, the room,
the user, the device, whether the user is muted, the gateway's event address, an issue time and an expiry
30 seconds out, under an HMAC-SHA256 key. The key is HKDF-derived (purpose `webrtc-sfu-control`) from the
namespace's own TURN secret, which the gateway and the SFU both already hold and no end user does, so one
namespace's SFU can neither accept another's ticket nor report into it. The gateway deletes whatever the
client sent under that header before it sets its own. The SFU refuses an upgrade with no ticket, a bad MAC,
an expired ticket, or a ticket for another namespace (HTTP `401`/`403`), and a `join` for any room but the
ticket's (`room_mismatch`).

**Rolling upgrade.** An upgraded SFU refuses a ticketless upgrade, and placement does not know versions: a
gateway that has not been upgraded yet can route a room (by rendezvous rank, or because the room already
lives there) to an upgraded node's SFU, and that join gets `401` with no ticket to present. So until every
node of the namespace is upgraded, **some joins fail**, not only the joins that land on a node not yet
upgraded: any join whose room is placed on an upgraded SFU through an old gateway does. The failure is a
clean refusal and the client's retry lands once the gateway is upgraded; there is no state to repair. Upgrade
the namespace's nodes one at a time, as the rolling-upgrade procedure does, and do not judge the namespace
healthy mid-upgrade by joins alone. The same holds for the control calls (kick, mute) and the membership
events: an old SFU does not serve `/admin/kick` or `/admin/mute`, so a kick made from an upgraded gateway
fails for that SFU (the revocation stands) until it is upgraded too.

### Admission

By default every signed-in user with the `webrtc` grant may join any room. A namespace can instead **require
admission**:

```bash
# the namespace's settings credential (an owner or admin session)
curl -X PUT https://ns-myapp.orama-devnet.network/v1/webrtc/config \
  -H "Authorization: Bearer $ADMIN_TOKEN" -d '{"require_admission": true}'
curl https://ns-myapp.orama-devnet.network/v1/webrtc/config -H "Authorization: Bearer $ADMIN_TOKEN"
# {"require_admission":true}
```

With it on, a join succeeds only if the namespace's functions admitted that user to that room, and the
admission is still good. A function admits with the `webrtc_admit` host function
([SERVERLESS.md](SERVERLESS.md#host-functions-api)):

```
webrtc_admit(room, user, device, ttl_seconds) -> {"room","user_id","device_id","expires_at"}
```

- `user` is the authenticated subject the user will join as (the `subject` of their session).
- `device` is empty to admit the user from any device, or a device id to admit that device only. A session
  bound to no device is matched only by an admission with no device.
- `ttl_seconds` is 1 to 86400 (24 hours). Admitting again extends the admission and lifts a revocation, and
  keeps a mute on record: an expired admission that carries a mute is kept for 7 days after it expires, so
  admitting the user again within that time does not unmute them. After 7 it is purged, which bounds the table,
  and a user admitted again then starts unmuted; a function that needs a longer mute calls `webrtc_mute` again.

**The admission's end holds for a live session.** The join ticket carries the unix second the admission it
was issued on ends, and the SFU removes the peer then: it is sent `kicked` with `data.code`
`admission_expired`, its socket and PeerConnection are closed, and the leave event's `reason` is `expired`.
Admitting again before then does **not** lengthen a session already open (it is bound by the ticket it joined
with); the client rejoins to pick up the new end, so a function that extends admissions should do it with
enough margin for the client to reconnect. A namespace that does not require admission has no end to hold.

**What each setting changes, and when.** Turning `require_admission` on or off changes **new joins** only:
a user already in a room stays in it (turning it on does not remove users who were never admitted; kick them),
and one who joined while it was off has no admission end to be held to. A gateway reads the policy on every
join, so the change is seen by all gateways at once.

**Clocks.** The gateway and the SFU compare clocks: the ticket's expiry (30 seconds) is judged by the SFU's
clock, so a gateway whose clock is **more than 30 seconds behind** the SFU's issues tickets the SFU finds
already expired, and every join through it is refused (`401`). Keep the nodes' clocks synchronised; a request
MAC (kick, mute, membership event) tolerates 60 seconds either way. See "Kick and mute" for how kicks cope
with a skew of up to 10 seconds between gateways.

A join that is not admitted is refused before it reaches an SFU, with a typed error that names why:

| Why | `?room=` upgrade (HTTP 403) `error.code` | Join frame without `?room=` (error frame `code`) |
|-----|---------|---------|
| Never admitted to this room, or as this user or device | `WEBRTC_ADMISSION_REQUIRED` | `admission_required` |
| The admission has expired | `WEBRTC_ADMISSION_EXPIRED` | `admission_expired` |
| A function revoked it (`webrtc_kick`) | `WEBRTC_ADMISSION_REVOKED` | `admission_revoked` |
| The admission records cannot be read | `SERVICE_UNAVAILABLE` (503, retryable) | `admission_unavailable` |
| The admission tables are not the ones migrations 073 and 075 make | `INTERNAL` (500, not retryable) | `admission_unavailable` |

A gateway that cannot read the records refuses the join; it never admits on a guess.

**A table that was there first.** Migration 073 creates `webrtc_settings` and `webrtc_admissions` with
`IF NOT EXISTS`, so a namespace whose own database already had a table of either name (its own, from before
the platform used the name) keeps it. The gateway therefore checks both tables the first time it uses them
(and again after a failure): every column must exist and the primary key must be exactly the migration's. A
table that does not match is refused with an error naming the table, the missing columns or key, and the
fix (rename or drop the tenant's table and re-apply the migration), on the join path, in `webrtc_admit`,
`webrtc_kick`, `webrtc_mute` and the settings route alike. Nothing reads or writes a table that is not the
admission table. A table that does not exist at all (the migration has not reached the database yet) is the
retryable `admission_unavailable`. The platform's own `webrtc_admissions` that lacks only `generation` is
reported as migration 075 not applied yet, not as a foreign table: the namespace gateway applies 075 when it
starts, and the next join checks again.

Where it lives: the policy (`webrtc_settings`) and the admissions (`webrtc_admissions`) are tables of the
**namespace's own database** (migrations 073 and 075), checked by whichever namespace gateway takes the join. They
are the tenant's own policy and grants, so no cluster-wide write sits on the join path; no SFU holds any of
it, so it survives an SFU restart and is the same whichever node owns the room. A namespace that never turns
the policy on keeps today's behaviour; the identity rule above applies either way.

### Membership events

The SFU reports every join and leave to the namespace, which publishes it on the pubsub topic
**`_orama/webrtc/<room>`**. Functions (a pubsub trigger on `_orama/webrtc/*`) and clients (a subscription to
the topic) read it like any topic:

```json
{"_orama":"webrtc.join","room":"standup","user_id":"0xabc...","device_id":"<did or absent>","peer_id":"<uuid>","at":"2026-10-06T10:00:00.123Z"}
{"_orama":"webrtc.leave","room":"standup","user_id":"0xabc...","peer_id":"<uuid>","reason":"left","at":"..."}
```

`reason` on a leave is `left` (the client left, or its connection failed), `kicked`, `expired` (the
admission the peer joined on ended), or `closed` (the SFU shut the room down). A user connected from two devices has two peers and two events. The `_orama` key is the
platform's: the pubsub publish routes refuse a payload carrying it (`PUBSUB_RESERVED_KEY`), so a subscriber that
sees it knows the platform wrote the message and not an end user. Trust `_orama` on these topics, not the
topic name.

**The topic is reserved.** Everything under `_orama/` is the platform's. No publish route accepts it, from any
caller: `POST /v1/pubsub/publish`, `publish-batch` and a frame on a pubsub WebSocket are all refused
(`403`, `PUBSUB_RESERVED_TOPIC`; a WebSocket on the topic is read-only and `presence` on it is refused). The
platform publishes these in-process. **Subscribing** needs a grant: a signed-in user who holds no grant in
the namespace (the end user of an application) can read pub/sub topics in general, but not these, because
they say who is in rooms that user was not admitted to; they are refused (`403`, `PUBSUB_RESERVED_TOPIC`) and
left out of `GET /v1/pubsub/topics`. A caller holding a grant (an API key of role `runtime` or above, or a
wallet with a pubsub grant covering the topic) may subscribe. The exact set is the credentials that can
**write** pub/sub on the topic: a wallet with a pubsub write grant that covers `_orama/webrtc/<room>` (a grant
narrowed to other topics does not), an API key of role `runtime` or any higher role (`developer`, `admin`,
`owner`). **Runtime keys are shipped inside applications**, so anyone who has the application has one: an
application that needs membership to stay private from its users keeps it behind functions and does not ship a
runtime key. An application that wants its end users to see a room's participants republishes what it chooses
from a function on a topic of its own. Functions themselves subscribe to the topic through a pubsub trigger
and the `ws_pubsub_bridge` host function, which run as the namespace and are not narrowed. **Bridging the
membership topic to end users is the application's decision:** a function that passes user input as the
`ws_pubsub_bridge` topic exposes the membership of every room to that user, so a function that bridges for a
user fixes the topic itself (`_orama/webrtc/` plus a room that user was admitted to) and never takes it from
the request. See [AUTH.md](AUTH.md#reserved-pubsub-topics).

How they travel: each ticket names the issuing gateway's overlay address; the SFU posts the event to it
(`POST /v1/internal/webrtc/events`, a MAC under the same namespace key) and the gateway publishes. The SFU
keeps the gateways it has recently seen tickets from and delivers to the most recently seen one that
answers, because the gateway a socket came through dies together with the socket and could not receive its
own peers' leaves. Events from one SFU are delivered in order. A gateway that answers and **refuses** an event
(a bad MAC) is not routed around: that is a misconfiguration, and the SFU logs an error naming it. Delivery
is at most once: when no gateway answers, the event is dropped and the SFU's log says so; a subscriber can
resynchronise from the `participants` list of a `welcome`.

### Kick and mute

Two host functions act on a participant ([SERVERLESS.md](SERVERLESS.md#host-functions-api)):

- `webrtc_kick(room, user)` **revokes** every admission of `user` to `room` and removes their connection:
  every device they are connected from is sent `kicked` and its socket and PeerConnection are closed
  (a `leave` event with reason `kicked` follows). The rejoin with the revoked admission is refused
  (`WEBRTC_ADMISSION_REVOKED`) until a function admits them again. A join whose ticket was issued before the
  kick but arrives after it is refused too, including one whose join frame the client had not yet sent when
  the kick landed (the SFU checks the kick log again once the peer is in the room, and removes it if it is
  refused). Every admission takes the next **generation** of its user in its room (1 for the first, one more on every admit, across devices: it only
  grows), the join ticket carries the generation of the admission it was issued on, and the kick carries the
  newest generation it revoked. The SFU refuses a ticket whose generation is **not newer than the kick's**, and
  no clock is involved: a user re-admitted right after a kick holds a ticket of a newer generation and joins at
  once, and a ticket of the revoked admission stays out whatever its gateway's clock says. The SFU remembers a
  kick for a ticket's life plus a 10 second margin. Where either side has no generation (a namespace that does
  not require admission, an admission made before generations existed, a kick or ticket from a gateway that has
  not been upgraded) it falls back to the clocks: any ticket for that user and room that reaches this SFU inside
  that window is refused, unless its issue time is more than the margin later than the kick's, so there a user
  re-admitted within about 10 seconds of a kick may be refused once and rejoins. An ended admission is kept
  for a minute so the count survives it. Without
  `require_admission` there is no admission to revoke: the kick closes the live connection and the user may
  rejoin.
- `webrtc_mute(room, user, muted)` makes the SFU stop forwarding `user`'s **audio** (every audio track they
  publish, checked per packet on the server, so a modified client that keeps sending is still silent), and
  tells the room with a `participant-state` frame carrying `"forced": true`. It is recorded on the user's
  admissions, so the user is still muted when they rejoin; `muted = false` resumes it. With no admission on
  record (a namespace that does not require admission, a user it never admitted) the mute lasts as long as
  that connection, plus the window below.

**A mute is logged on the SFU like a kick.** A join ticket carries the mute state the gateway read when it
issued it, a snapshot of at most 30 seconds. Each SFU therefore keeps the latest mute or unmute of a user in a
room (the request carries the gateway's clock, `at_ms`) for a ticket's life plus the 10 second margin, and a
join whose ticket was issued at or before that change (plus the margin) takes the logged state instead of
the ticket's: a user holding an unmuted ticket minted before the mute is muted when they reconnect, a join in
flight when the mute lands ends muted (the SFU applies the log once the peer is in the room), and a user
unmuted after a muted ticket was minted is unmuted. A ticket issued after the change is authoritative. A late
request older than the one on record is ignored. Unlike a kick this is judged by the gateways' clocks alone,
with no generation: a mute is a state, not a revocation, and a re-admission keeps it, so a ticket issued just
after a mute and corrected to the logged state still ends in the state the namespace last set. The peer is told with the same forced `participant-state`
frame, after its `welcome`.

Both are sent to **every SFU of the namespace**, not only the room's owner: placement is decided by health
probes, so an SFU that missed one while it hosted the room would be passed over and the call would land on one
that has no such peer, and the kick would silently do nothing. Every SFU keeps its own kick log, so every SFU
is told, over the overlay, with a request MAC (the target's address, method, path, timestamp, a random nonce
and the body hash: a captured request replays onto nothing else, not onto another SFU or gateway of the
namespace even though they share the key, and the SFU, like the gateway's events route, remembers the MACs it
served for the length of their validity and refuses a second use, `401`). The target is the receiver's
signalling address (`host:port`, the SFU's `listen_addr`) for a control request and the gateway's event address
(the one its tickets carry) for a membership event; each receiver verifies against its own, so a stamp made for
another is refused. The MAC changed in this release (`orama-sfu-control-v2`): an SFU and a gateway on
different releases refuse each other's kicks, mutes and events, so upgrade the namespace's nodes together. The generation (migration 075, a column the namespace's gateways add on start) crosses releases safely: an older SFU ignores the extra field of a ticket or kick and judges by the clocks, and an upgraded SFU takes a ticket or kick without one the same way. The database change comes first: if
any SFU cannot be reached or refuses, the host function fails, names each such SFU, and says the revocation
(or mute) is recorded but not every connection was closed; the SFUs that could be reached did act, and
repeating it is safe. Both act on the gateway's own namespace and on no other: a call naming another
namespace is refused before anything is recorded or sent.

### Audio and video state

A client tells the room its microphone or camera went on or off:

```json
{"type":"audio-state","data":{"enabled":false}}
{"type":"video-state","data":{"enabled":true}}
```

The SFU relays it to the **other** participants as
`{"type":"participant-state","data":{"peerId","userId","kind":"audio"|"video","enabled":bool}}`. The state is
for the others' interfaces: the SFU does not act on it (the media shows whether a track is live), and it is
not kept for late joiners, who learn state from the frames that follow their join. A frame without a boolean
`enabled` is answered `invalid_state`. (Both used to be answered `unknown_message`.)

## Room Placement

A room lives in exactly one SFU process (rooms are in that process's memory), so
every peer of a call must reach the same SFU. Clients resolve the namespace host
round-robin, so peers of one room arrive at different nodes' gateways; each
gateway therefore routes the socket to the room's SFU, not to its own node's.

The gateway learns the room one of two ways. With `?room=<roomId>` on the URL
it decides at the upgrade and pipes the socket through untouched. Without it
(existing clients) the gateway accepts the upgrade itself, waits up to 5 s for
the first frame, requires it to be a text `join` frame of at most 4096 bytes
with a valid `roomId` (a `userId` is ignored), then dials the owning SFU (naming the room
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
- SFU signaling path TTL (credentials the SFU hands to clients): per-namespace
  `turn_credential_ttl` (default 600s). The SFU proactively sends `refresh-credentials`
  over the signaling WebSocket at 80% of TTL (the initial credential arrives as
  `turn-credentials`), so a short TTL is safe there.
- The SFU's own PeerConnection (relay-only) authenticates to TURN with a credential
  of its own that never leaves the SFU: 24h (`turn.DefaultCredentialTTL`), independent
  of `turn_credential_ttl`. pion's TURN client refreshes its allocation with the
  credential it was created with and the TURN server rejects an expired one, and
  `SetConfiguration` only affects the next ICE gathering, never an existing
  allocation. So at 80% of that lifetime (19.2h, `sfuTURNRefreshInterval`) the SFU
  swaps in a fresh credential (`SetConfiguration`) and sends the client an offer with
  an ICE restart, which gathers a new allocation authenticated with it. A session
  therefore never outlives its relay credential. If the swap fails the peer is
  disconnected so the client rejoins on a fresh credential, instead of a call whose
  media silently stops.
- Clients should update ICE servers on receiving `refresh-credentials`

## TURNS TLS Certificate

TURNS (port 5349) uses TLS and the client connects to the single-label host
`turn-{name}.{baseDomain}`. The shared TURN server presents the cluster's
`*.{baseDomain}` wildcard cert (the one Caddy serves for HTTPS), which covers
every tenant's single-label TURNS host and stealth host, so browsers validate
it. Caddy keeps it in the cluster's shared certificate store, and the cluster
gateway exports it to `/opt/orama/.orama/data/tls/wildcard.{crt,key}`
([ARCHITECTURE.md](ARCHITECTURE.md#tlshttps)), where TURN reads it.

There is no other source. If the wildcard is not exported yet, TURNS stays off and
the node logs why; clients keep plain TURN on 3478. A self-signed cert is never
served — browsers reject it, and for a stealth host a rejected cert is
indistinguishable from being blocked. The two-label host
`turn.ns-{name}.{baseDomain}` is not covered by the wildcard, which is why
TURNS uses the single-label host.

Caddy renews the certificate once for the cluster, with about a third of its lifetime left; the cluster gateway exports the renewal within a minute. TURN serves the cert through a hot-reloading `GetCertificate` callback that polls the cert file every 60 seconds, so renewed certs are picked up in-process without a restart (a restart would drop every active relay).

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
- **Identity and admission**: a peer is the user the gateway authenticated, carried to the SFU in a short-lived ticket signed with a key derived from the namespace's TURN secret; a namespace may admit only the users its functions admitted (see "Identity, admission and moderation").
- **Room lifecycle**: there is no create/close API. A room is created by its first join (which needs an authenticated user and, if the namespace requires admission, an admitted one) and removed by the SFU 60 seconds after it empties.
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
| `webrtc_settings` | The namespace's own database: its WebRTC policy (`require_admission`) |
| `webrtc_admissions` | The namespace's own database: who its functions admitted to which room, until when, whether revoked or muted |

## Cold Boot Recovery

On node restart, the cluster state file (`cluster_state.json`) includes `has_sfu`, `has_turn`, and port allocation data. The restore process:

1. Core services restore first: RQLite → Olric → Gateway
2. If `has_turn` is set: fetches TURN shared secret from DB, spawns TURN
3. If `has_sfu` is set: fetches WebRTC config from DB, spawns SFU with TURN server list

If the DB is unavailable during restore, SFU/TURN restoration is skipped with a warning log. They will be restored on the next successful DB connection.
