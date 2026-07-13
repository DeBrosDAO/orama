# Orama Vault -- Architecture

## What is Orama Vault?

Orama Vault is a distributed secrets store. It runs as a guardian daemon (`vault-guardian`) on every node in the Orama Network, similar to how IPFS nodes run on every machine. Clients can store any sensitive data -- API keys, database passwords, SSH keys, crypto seeds, wallet recovery shares, or arbitrary encrypted blobs. The client splits each secret into Shamir shares and pushes one share to each guardian. To retrieve, the client pulls shares from K guardians and reconstructs the original secret via Lagrange interpolation.

The system provides information-theoretic security: compromising fewer than K guardians reveals zero information about the original secret. This is not computational security -- it is mathematically impossible to learn anything from K-1 shares, regardless of computing power.

## How It Fits Into Orama Network

```
                         Orama Network Node
    +------------------------------------------------------------+
    |  orama-gateway (port 443)                                  |
    |    |                                                       |
    |    +-- reverse-proxy --> vault-guardian (port 7500, client) |
    |                          vault-guardian (port 7501, peer)   |
    |                                                            |
    |  RQLite (port 4001) -- cluster membership source of truth  |
    |  Olric  (port 10003) -- distributed cache                  |
    |  WireGuard (10.0.0.x) -- encrypted overlay network         |
    +------------------------------------------------------------+
```

Every Orama node runs a `vault-guardian` process alongside the gateway, RQLite, and Olric. The guardian:

- Listens on **port 7500** for client HTTP requests (V1 push/pull shares, V2 CRUD secrets).
- Reserves **port 7501** for the guardian-to-guardian binary protocol (heartbeat, verify, repair), restricted to the WireGuard overlay network. The protocol is implemented, but the peer listener is not yet started by the daemon -- nothing accepts connections on 7501 in v0.1.0.
- Will discover peers via RQLite (the cluster's membership source of truth). The RQLite fetch is currently a stub that returns an empty node list, so each guardian effectively runs single-node today.

## Data Flow

### V1 Push (Backup)

```
Client                     Guardian-1        Guardian-2        Guardian-N
  |                            |                 |                 |
  |-- POST /v1/vault/push --> [store share.bin]  |                 |
  |-- POST /v1/vault/push ------------------> [store share.bin]   |
  |-- POST /v1/vault/push -------------------------------------> [store]
  |                            |                 |                 |
  |<--- {"status":"stored"} ---|                 |                 |
```

1. Client generates encrypted key material (DEK-wrapped secret, KEK1/KEK2 wrapped DEKs).
2. Client runs Shamir split locally: secret -> N shares with threshold K.
3. Client pushes each share to a different guardian via `POST /v1/vault/push` (session token + Ed25519 ownership proof required).
4. Each guardian stores the share atomically to disk (temp file + rename).
5. Each guardian writes the monotonic version (plus threshold) to `meta.json` last, as the commit marker, for anti-rollback protection.

### V1 Pull (Recovery)

```
Client                     Guardian-1        Guardian-2        Guardian-K
  |                            |                 |                 |
  |-- POST /v1/vault/pull --> [read share.bin]   |                 |
  |<-- {"share":"<base64>"} ---|                 |                 |
  |-- POST /v1/vault/pull ------------------> [read share.bin]    |
  |<-- {"share":"<base64>"} ------------------|                    |
  |     ...collect K shares...                                     |
  |                                                                |
  [Lagrange interpolation at x=0 -> reconstruct secret]
```

1. Client contacts guardians and requests its share via `POST /v1/vault/pull`.
2. Each guardian reads the share from disk and returns it base64-encoded.
3. Client collects at least K shares and reconstructs the secret via Lagrange interpolation over GF(2^8).

### V2 Store (Named Secret)

```
Client                     Guardian-1        Guardian-2        Guardian-N
  |                            |                 |                 |
  |-- auth/challenge -------->|                 |                 |
  |<-- {nonce,tag} -----------|                 |                 |
  |-- auth/session ---------->|                 |                 |
  |<-- {identity,expiry_ns,tag} (client assembles token)          |
  |                            |                 |                 |
  |  [Shamir split: secret -> N shares]                           |
  |                            |                 |                 |
  |-- PUT /v2/vault/secrets/my-key ------------> [store share]    |
  |   X-Session-Token: <tok>   |                 |                 |
  |-- PUT /v2/vault/secrets/my-key --> [store]   |                 |
  |-- PUT /v2/vault/secrets/my-key -----------------------------> [store]
  |                            |                 |                 |
  |<-- {"status":"stored"} ---|                 |                 |
```

1. Client authenticates with each guardian (challenge-response).
2. Client runs Shamir split locally: secret -> N shares with threshold K.
3. Client pushes each share to a different guardian via `PUT /v2/vault/secrets/{name}` with `X-Session-Token` header.
4. Each guardian extracts identity from the session token and stores the share under `<data_dir>/vaults/<identity>/<name>/`.
5. Anti-rollback: version must be strictly greater than the stored version.

### V2 Retrieve (Named Secret)

```
Client                     Guardian-1        Guardian-2        Guardian-K
  |                            |                 |                 |
  |-- GET /v2/vault/secrets/my-key --> [read]    |                 |
  |<-- {"share":"<b64>"} ------|                 |                 |
  |-- GET /v2/vault/secrets/my-key ------------> [read]           |
  |<-- {"share":"<b64>"} -----------------------|                  |
  |     ...collect K shares...                                     |
  |                                                                |
  [Lagrange interpolation at x=0 -> reconstruct secret]
```

## Component Diagram

```
+------------------------------------------------------------------+
|                        vault-guardian                              |
|                                                                    |
|  +------------------+     +------------------+                     |
|  |   HTTP Server    |     |  Peer Protocol   |                     |
|  |   (port 7500)    |     |  (port 7501)     |                     |
|  |                  |     |                  |                     |
|  | /v1/vault/health |     | heartbeat (5s)   |                     |
|  | /v1/vault/status |     | verify_request   |                     |
|  | /v1/vault/guard. |     | verify_response  |                     |
|  | /v1/vault/push   |     | repair_offer     |                     |
|  | /v1/vault/pull   |     | repair_accept    |                     |
|  | /v1/vault/auth/* |     +--------+---------+                     |
|  |                  |              |                               |
|  | /v2/vault/auth/* |     +--------+---------+                     |
|  | /v2/vault/secrets|     |   Heartbeat Mgr  |                     |
|  +--------+---------+     | (heartbeat.zig)  |                     |
|           |               +--------+---------+                     |
|  +--------+---------+              |                               |
|  |     Router       |              |                               |
|  |  (router.zig)    |              |                               |
|  +--------+---------+              |                               |
|           |                        |                               |
|  +--------+------------------------+---------+                     |
|  |              Guardian Struct              |                     |
|  |           (guardian.zig)                   |                     |
|  |  server_secret, node_list, share_count    |                     |
|  +---+----------+-----------+--------+-------+                     |
|      |          |           |        |                             |
|  +---+---+ +---+----+ +----+---+ +--+--------+                    |
|  | Auth  | | Storage| |  SSS   | |Membership |                    |
|  |       | |        | |  Core  | |           |                    |
|  |chall. | |file_   | |field   | |node_list  |                    |
|  |session| |store   | |poly    | |discovery  |                    |
|  +-------+ |vault_  | |split   | |quorum     |                    |
|            |store   | |combine | +-----------+                    |
|            +---+----+ |commit. |                                  |
|                |       |reshare |                                  |
|  +-------------+       +--------+                                  |
|  |   Crypto    |                                                   |
|  |             |                                                   |
|  | aes (GCM)  |                                                   |
|  | hmac       |                                                   |
|  | hkdf       |                                                   |
|  | secure_mem |                                                   |
|  | pq_kem *   |  * = stub, Phase 2                                |
|  | pq_sig *   |                                                   |
|  | hybrid *   |                                                   |
|  +-------------+                                                   |
+------------------------------------------------------------------+
```

## Key Design Decisions

### Why File-Per-User Storage (Not a Database)

Each user's share is stored as a plain file. In V1 the layout is `<data_dir>/shares/<identity_hash>/share.bin`. In V2 the layout is `<data_dir>/vaults/<identity_hex>/<secret_name>/`. This design was chosen because:

1. **No external dependencies.** The guardian binary is fully self-contained. No PostgreSQL, SQLite, or RQLite dependency for storage.
2. **Atomic writes.** The write-to-temp + rename pattern guarantees that a share is either fully written or not at all. No partial writes, no journal corruption.
3. **Simple backup.** The entire data directory can be backed up with rsync or tar.
4. **Predictable performance.** No query planning, no lock contention, no WAL growth. Each operation is a single file read or write.
5. **Natural sharding.** Files are already sharded by identity hash. No rebalancing needed.

### Storage Layouts

**V1 (Single Share per Identity):**

```
<data_dir>/shares/<identity_hash_hex>/
    share.bin          -- Raw encrypted share data
    share.bin.tmp      -- Temp file during atomic write
    checksum.bin       -- HMAC-SHA256 integrity checksum
    meta.json          -- {"version":<n>,"threshold":<k>} (anti-rollback + reconstruction params)
    wrapped_dek1.bin   -- KEK1-wrapped DEK (Phase 2)
    wrapped_dek2.bin   -- KEK2-wrapped DEK (Phase 2)
```

`meta.json` is written last, as the commit marker: the share only counts as present once it lands, giving crash-consistency across the multi-file write. The guardian also keeps a persistent at-rest integrity key at `<data_dir>/integrity.key` (created on first run, 0600 permissions) that keys the HMAC over stored share files so they survive restarts.

**V2 (Generic Secrets):**

```
<data_dir>/vaults/<identity_hex>/<secret_name>/
    share.bin     -- Encrypted share data
    checksum.bin  -- HMAC-SHA256 integrity checksum
    meta.json     -- {"version":1,"created_ns":...,"updated_ns":...,"size":123}
```

V2 supports up to 1000 named secrets per identity, each up to 512 KiB. Secret names are restricted to `[a-zA-Z0-9_-]` and max 128 characters.

### Why GF(2^8)

Shamir's Secret Sharing operates over a finite field. We use GF(2^8) -- the Galois field with 256 elements -- because:

1. **Byte-aligned.** Each field element is exactly one byte. No encoding overhead, no bignum arithmetic.
2. **Same field as AES.** GF(2^8) with irreducible polynomial x^8 + x^4 + x^3 + x + 1 (0x11B) is the same field used by AES. Well-studied, well-understood.
3. **Fast arithmetic.** Precomputed log/exp tables (generated at comptime in Zig) give O(1) multiplication, inversion, and division with zero runtime cost.
4. **255 nonzero elements.** Supports up to 255 shares (evaluation points x=1..255), which is more than sufficient for the Orama network (up to ~100 nodes per environment).

Addition and subtraction in GF(2^8) are both XOR. Multiplication uses log/exp table lookups. Division uses `a / b = a * inv(b)` where `inv(a) = exp[255 - log[a]]`.

### Why All-Node Replication

Every guardian stores one share per user. In a 14-node cluster, each user has 14 shares with an adaptive threshold K = max(3, floor(N/3)). This means:

- With 14 nodes: K=4, so any 4 guardians can reconstruct.
- With 100 nodes: K=33, so any 33 guardians can reconstruct.
- Up to N-K nodes can be completely destroyed before data is lost.

All-node replication was chosen because:

1. **Maximum fault tolerance.** The more shares that exist, the more nodes can fail.
2. **Simple push logic.** Client pushes to all nodes, no routing or placement decisions.
3. **Low per-share cost.** Each share is the same size as the original secret (~1KB typically). Even at 100 nodes, total storage per user is ~100KB.

## Guardian Lifecycle

### Startup

1. Parse CLI arguments and load config from the config file (key=value format; defaults if the file does not exist).
2. Ensure the data directory exists.
3. Generate a random 32-byte server secret (for HMAC-based auth) and load or create the persistent at-rest integrity key (`<data_dir>/integrity.key`).
4. Fetch node list from RQLite -- currently a stub that returns an empty list, so the guardian runs with no known peers.
5. Mark self as alive in the node list (only when the list is non-empty).
6. Count existing shares on disk.
7. Start the heartbeat-send thread, then the HTTP server on port 7500 (blocks in accept loop).

### Heartbeat

The peer protocol targets port 7501 (WireGuard-only). Every 5 seconds, a background thread sends a heartbeat to all known peers. Note that the peer *listener* is not yet wired into the daemon, so heartbeats are currently sent but never received -- and with RQLite discovery stubbed there are no known peers to send to anyway. The heartbeat includes:

- Sender IP (4 bytes, WireGuard address)
- Sender port (2 bytes)
- Share count (4 bytes)
- Timestamp (8 bytes, Unix seconds)

Peer state transitions:

```
            5s heartbeat received
unknown  --------------------------> alive
alive    --- no heartbeat for 15s -> suspect
suspect  --- no heartbeat for 60s -> dead
dead     --- heartbeat received ---> alive
```

### Verify

Periodic verification ensures share integrity across guardians (part of the not-yet-wired peer protocol). A guardian:

1. Selects a share and computes its SHA-256 hash (commitment root).
2. Sends a `verify_request` to a peer with the identity hash.
3. The peer reads its copy, computes SHA-256, and sends a `verify_response`.
4. The initiator compares commitment roots. Mismatch indicates tampering or corruption.

### Repair (Proactive Re-sharing)

> **Status:** The repair/reshare modules are implemented and unit-tested but not yet wired into the running daemon -- no runtime code triggers re-sharing today.

When the cluster topology changes (node join/leave) or every 24 hours, the repair protocol is designed to refresh all shares using the Herzberg-Jarecki-Krawczyk-Yung protocol:

1. Leader broadcasts `repair_offer` to all guardians.
2. Each guardian generates a random polynomial q_i(x) of degree K-1 with q_i(0)=0.
3. Guardian i sends q_i(j) to guardian j for all j.
4. Each guardian computes: `new_share = old_share + sum(received deltas)` over GF(2^8).
5. Guardians exchange new Merkle commitments to verify consistency.

The secret is preserved because `sum(q_i(0)) = 0`. Old shares become algebraically independent from new shares, so compromising old shares provides zero information about the current secret.

Repair triggers:
- Node topology change detected (join or departure)
- Periodic timer (every 24 hours)
- Manual admin trigger (Phase 2)

Safety requirement: at least 3 alive guardians to initiate repair.

### Shutdown

On shutdown, the in-memory server secret and integrity key are both zeroed (`@memset`). Share data on disk -- and the persistent `integrity.key` file -- survive restarts.

## Recovery Paths

### Path A: Mnemonic Recovery

The user has their BIP-39 mnemonic phrase. They derive the root seed locally and use it to decrypt the vault contents. No guardian interaction needed -- the guardians only store encrypted shares as an additional backup.

### Path B: Username + Passphrase Recovery

The user does not have their mnemonic but remembers their username and passphrase. The recovery flow:

1. Client derives identity hash from username (SHA-256 of identity).
2. Client contacts guardians and pulls K shares via `POST /v1/vault/pull`.
3. Client reconstructs the encrypted blob via Lagrange interpolation.
4. Client derives the decryption key from the passphrase (via HKDF).
5. Client decrypts the blob to recover the root seed/mnemonic.

This path depends on the key wrapping scheme (DEK encrypted by KEK1 from mnemonic, KEK2 from passphrase). The dual-KEK design ensures either recovery path works independently.

## Protocol Versions

- **Protocol version 1** (current): 6-byte wire header `[version:1][type:1][length:4]`, big-endian length, TCP transport on port 7501.
- **Binary v0.1.0**: MVP with single-threaded HTTP server, file-per-user storage, HMAC-based auth, stub post-quantum crypto.

## What Is Implemented vs. Planned

| Component | Status | Notes |
|-----------|--------|-------|
| SSS field arithmetic (GF(2^8)) | Complete | Exhaustive test coverage |
| SSS split/combine | Complete | Verified across all C(N,K) subsets |
| SSS reshare (Herzberg protocol) | Complete | Unit tested, not yet wired to peer protocol |
| Merkle commitments | Complete | Build, prove, verify all tested |
| AES-256-GCM encryption | Complete | Round-trip, tamper, wrong-key tests |
| HMAC-SHA256 integrity | Complete | Constant-time verification |
| HKDF-SHA256 key derivation | Complete | Cross-platform test vectors |
| Secure memory (mlock, secureZero) | Complete | Linux mlock, volatile zero |
| File-per-user storage (V1) | Complete | Atomic writes, HMAC integrity |
| V2 multi-secret storage engine | Complete | Named secrets, per-identity vaults |
| V2 CRUD HTTP handlers | Complete | PUT, GET, DELETE, LIST |
| V1-to-V2 migration tool | Complete | Migrates V1 shares into V2 layout |
| HTTP server (push/pull/health/status) | Complete | Single-threaded MVP |
| Peer binary protocol | Complete | Encode/decode with tests |
| Heartbeat state machine | Complete | alive/suspect/dead transitions |
| Peer verify protocol | Complete | Commitment comparison |
| Repair round state machine | Complete | Timeout, delta tracking |
| Node list / discovery | Complete | Static + RQLite (RQLite fetch is stub) |
| Quorum logic | Complete | Write quorum W=min(N, max(K+1, ceil(2N/3))), read quorum K |
| Challenge-response auth | Complete | HMAC-based, 60s expiry, wired to router |
| Session tokens | Complete | HMAC-based, 1h expiry, wired to router |
| Auth enforcement on V2 | Complete | Mandatory session auth on all V2 secrets endpoints |
| Auth enforcement on V1 push/pull | Complete | Mandatory session token + Ed25519 ownership proof |
| Config file parsing | Complete | key=value format (not YAML); defaults when file absent |
| Rate limiting | Complete | Per-IP, 120 requests per 60s window in the listener |
| Heartbeat send loop | Complete | Background thread in main(); no peers to send to until discovery lands |
| RQLite node discovery | Stub | Returns empty list, HTTP fetch Phase 2 |
| Post-quantum KEM (ML-KEM-768) | Stub | HMAC-derived shared secrets, not real ML-KEM |
| Post-quantum signatures (ML-DSA-65) | Stub | HMAC-based sign/verify, fail-closed but not post-quantum |
| Hybrid key exchange (X25519 + ML-KEM) | Partial | X25519 works, ML-KEM is stub |
| Multi-threaded HTTP server | Not started | Phase 3 |
| TLS termination | Not started | Phase 3, currently plain TCP |
| Peer listener (port 7501) | Not started | Protocol implemented, listener not started by the daemon |
| Peer repair orchestration | Not started | State machine exists, coordination not wired |
| Admin API | Not started | Phase 3 |