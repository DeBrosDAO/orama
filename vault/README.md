# Orama Vault

A distributed secrets store built on Shamir's Secret Sharing. Orama Vault stores one share per guardian. The **guardian HTTP API** is share-at-a-time: a client that talks to guardians directly splits and reconstructs locally. The **production gateway path does not reverse-proxy that API** — the gateway splits on push and combines on pull, so the gateway process holds the reconstructed secret in memory for the duration of that request.

## How It Works

1. **Split.** Shamir split over GF(2^8) with threshold K. Direct guardian clients split locally; the Orama gateway splits in the gateway process.
2. **Guardians store.** Each share is stored on a different guardian (HMAC integrity, not encryption-at-rest in the file store).
3. **Reconstruct.** Direct clients pull K shares and interpolate locally. The gateway pull path interpolates in the gateway and returns the secret to the caller.

The security is **information-theoretic**: K-1 shares reveal exactly zero information about the secret, regardless of computing power. This is not a computational assumption — it is mathematically proven.

## Key Properties

- **Share isolation on disk** — Each guardian holds a single share that is meaningless on its own. This does **not** mean the gateway never sees the secret.
- **Fault tolerant** — Up to N-K nodes can go offline or be destroyed without data loss
- **No central authority** — No master key, no trusted coordinator, no single point of failure
- **Tamper-evident** — Every share is protected by HMAC-SHA256 integrity checksums
- **Anti-rollback** — Monotonic version counters prevent downgrade attacks
- **Proactive re-sharing (not yet active)** — Share refresh (Herzberg protocol) that invalidates old shares is implemented and tested, but not yet triggered at runtime
- **Post-quantum ready** — Interfaces for ML-KEM-768 and ML-DSA-65 are defined, with hybrid X25519 + ML-KEM key exchange

## Use Cases

### Wallet Recovery
Store a crypto wallet's seed phrase or private key across the guardian network. If you lose your device, reconstruct the seed from any K guardians using your username and passphrase — no single server ever sees the full key.

### API Key & Credential Management
Distribute API keys, database passwords, or service tokens across guardians. Applications pull shares from K nodes at runtime and reconstruct credentials in memory. Keys are never stored in plaintext on any single machine.

### Multi-Party Key Custody
Organizations can require M-of-N approval to access critical secrets. The Shamir threshold naturally enforces that no individual (or small group) can unilaterally access the data.

### Disaster Recovery
Back up encryption keys, TLS certificates, or signing keys to the guardian network as an off-site, tamper-evident backup layer. Even if your primary infrastructure is compromised, the distributed shares remain safe.

### End-to-End Encrypted Backup
Store encrypted data blobs where the decryption key is itself split across guardians. The encrypted payload can live on IPFS or any storage — only the key holders (the guardians collectively) can enable decryption.

### Hardware Node Integration (Orama One)
Pre-built Orama One hardware nodes run a vault guardian out of the box, contributing to the distributed secrets network. Node operators earn dual rewards ($ORAMA + $ANYONE) while strengthening the network's fault tolerance.

## Tech Stack

- **Language:** Zig 0.15.2+
- **Crypto:** AES-256-GCM, HMAC-SHA256, HKDF-SHA256, GF(2^8) field arithmetic
- **Storage:** File-per-user with atomic writes (no database dependency)
- **Transport:** HTTP (port 7500, client-facing) + binary TCP protocol (port 7501, peer-to-peer over WireGuard; not yet active — see Architecture)
- **Security:** Secure memory (mlock, volatile zero), constant-time comparisons, systemd hardening

## Architecture

Each Orama Network node runs a `vault-guardian` daemon alongside the gateway:

```
Client ──▶ Gateway (443/TLS) ──▶ vault-guardian (7500/HTTP)
                                       │
                                  vault-guardian (7501/TCP, WireGuard)
                                       │
                                  Peer guardians
```

In the target design, guardians discover each other via RQLite (the cluster's membership source of truth) and maintain health through a heartbeat protocol with alive/suspect/dead state transitions.

> **Status (v0.1.0):** The multi-guardian plumbing is not yet wired in. RQLite discovery is a stub that returns an empty node list, the peer listener on port 7501 is never started, and re-sharing is never triggered — each guardian currently runs single-node, serving push/pull over HTTP on port 7500.

## API Overview

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/vault/health` | GET | Liveness check |
| `/v1/vault/status` | GET | Guardian status and config |
| `/v1/vault/guardians` | GET | List known guardian nodes |
| `/v1/vault/push` | POST | Store a share (single share per identity) (authenticated) |
| `/v1/vault/pull` | POST | Retrieve a share (authenticated) |
| `/v2/vault/secrets/{name}` | PUT | Store a named secret (authenticated) |
| `/v2/vault/secrets/{name}` | GET | Retrieve a named secret (authenticated) |
| `/v2/vault/secrets/{name}` | DELETE | Delete a named secret (authenticated) |
| `/v2/vault/secrets` | GET | List all secrets for an identity (authenticated) |

Authenticated endpoints require a session token obtained via challenge-response (HMAC-based tokens, 1-hour expiry). V1 push and pull additionally require an Ed25519 ownership proof (identity = SHA-256 of the public key, plus a signature over the request); only health, status, and guardians are unauthenticated.

See [docs/vault/API.md](../docs/vault/API.md) for the full API reference.

## Documentation

- [Architecture](../docs/vault/ARCHITECTURE.md) — System design, data flow, and component overview
- [Security Model](../docs/vault/SECURITY_MODEL.md) — Threat model, crypto rationale, and hardening measures
- [API Reference](../docs/vault/API.md) — Complete endpoint documentation
- [Deployment](../docs/vault/DEPLOYMENT.md) — Deploying vault guardians to production
- [Operator Guide](../docs/vault/OPERATOR_GUIDE.md) — Running and maintaining guardian nodes
- [Post-Quantum Integration](../docs/vault/PQ_INTEGRATION.md) — ML-KEM and ML-DSA roadmap

## Building

```bash
zig build              # Build the vault-guardian binary
zig build test         # Run all unit tests
```

Requires Zig 0.15.2 or later (`minimum_zig_version` in `build.zig.zon`).

## License

Part of the Orama Network. See the root repository for license details.
