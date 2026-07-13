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
6. Creates DNS A records: `turn.ns-{name}.{baseDomain}` pointing to TURN node public IPs
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
//   "turns:turn.ns-myapp.orama-devnet.network:5349"
// ]
// username: "{expiry_unix}:{namespace}"
// password: HMAC-SHA1 derived (base64)
// ttl: 600 (seconds)
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
| TURN listen | 3478 | UDP + TCP | fixed |
| TURNS (TLS) | 5349 | TCP | fixed |
| TURN relay | 49152-65535 | UDP | 800 ports |

## TURN Credential Protocol

- Credentials use HMAC-SHA1 with a per-namespace shared secret
- Username format: `{expiry_unix}:{namespace}`
- Password: `base64(HMAC-SHA1(shared_secret, username))`
- Default TTL: 600 seconds (10 minutes)
- SFU proactively sends `refresh-credentials` at 80% of TTL (8 minutes)
- Clients should update ICE servers on receiving refresh

## TURNS TLS Certificate

TURNS (port 5349) uses TLS. Certificate provisioning:

1. **Let's Encrypt (primary)**: On TURN spawn, the TURN domain is added to the local Caddy instance's Caddyfile. Caddy provisions a Let's Encrypt cert via DNS-01 ACME challenge (using the orama DNS provider). TURN reads the cert from Caddy's storage.
2. **Self-signed (fallback)**: If Caddy cert provisioning fails (timeout, Caddy not running), a self-signed cert is generated with the node's public IP as SAN.

Caddy auto-renews Let's Encrypt certs at ~60 days. TURN serves the cert through a hot-reloading `GetCertificate` callback that polls the cert file every 60 seconds, so renewed certs are picked up in-process without a restart (a restart would drop every active relay).

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

## Debugging

```bash
# SFU logs
journalctl -u orama-namespace-sfu@myapp -f

# TURN logs
journalctl -u orama-namespace-turn@myapp -f

# Check service status
systemctl status orama-namespace-sfu@myapp
systemctl status orama-namespace-turn@myapp
```

## Security Model

- **Forced relay**: `iceTransportPolicy: relay` enforced server-side. Clients cannot bypass TURN.
- **HMAC credentials**: Per-namespace TURN shared secret. Credentials expire after 10 minutes.
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
