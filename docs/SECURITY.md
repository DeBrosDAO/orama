# Security Hardening

This document describes all security measures applied to the Orama Network, covering both Phase 1 (service hardening on existing Ubuntu nodes) and Phase 2 (OramaOS locked-down image).

## Phase 1: Service Hardening

These measures apply to all nodes (Ubuntu and OramaOS).

### Network Isolation

**CIDR Validation (Step 1.1)**
- WireGuard subnet restricted to `10.0.0.0/24` across all components: firewall rules, rate limiter, auth module, and WireGuard PostUp/PostDown iptables rules
- Prevents other tenants on shared VPS providers from bypassing the firewall via overlapping `10.x.x.x` ranges

**IPv6 Disabled (Step 1.2)**
- IPv6 disabled system-wide via sysctl: `net.ipv6.conf.all.disable_ipv6=1`
- Prevents services bound to `0.0.0.0` from being reachable via IPv6 (which had no firewall rules)

### Authentication

**Internal Endpoint Auth (Step 1.3)**
- `/v1/internal/wg/peers` and `/v1/internal/wg/peer/remove` now require cluster secret validation
- Peer removal additionally validates the request originates from a WireGuard subnet IP

**RQLite Authentication (Step 1.7)**
- Credentials are generated at genesis and written to `rqlite-auth.json` / `rqlite-password`
- `rqlited` is **not** started with `-auth` today: `RQLiteAuthFile` is never assigned, so the HTTP API does not require those credentials
- Clients still send `Authorization: Basic` when they have a password (harmless if the server ignores it)
- What keeps the RQLite API off the public internet is the firewall / WireGuard overlay, not RQLite HTTP auth

**Olric Gossip Encryption (Step 1.8)**
- Olric memberlist uses a 32-byte encryption key for all gossip traffic
- Key generated at genesis, distributed via join response
- Prevents rogue nodes from joining the gossip ring and poisoning caches
- Note: encryption is all-or-nothing (coordinated restart required when enabling)

**IPFS Cluster TrustedPeers (Step 1.9)**
- IPFS Cluster `TrustedPeers` populated with actual cluster peer IDs (was `["*"]`)
- New peers added to TrustedPeers on all existing nodes during join
- Prevents unauthorized peers from controlling IPFS pinning

**Vault V1 Auth Enforcement (Step 1.14)**
- V1 push/pull endpoints require a valid session token when vault-guardian is configured
- Previously, auth was optional for backward compatibility — any WG peer could read/overwrite Shamir shares

### Token & Key Storage

**Refresh Token Hashing (Step 1.5)**
- Refresh tokens stored as SHA-256 hashes in RQLite (never plaintext)
- On lookup: hash the incoming token, query by hash
- On revocation: hash before revoking (both single-token and by-subject)
- Existing tokens invalidated on upgrade (users re-authenticate)

**API Key Hashing (Step 1.6)**
- API keys stored as HMAC-SHA256 hashes using a server-side secret
- HMAC secret generated at cluster genesis, stored in `~/.orama/secrets/api-key-hmac-secret`
- Namespace and index gateway spawn refuse to start if that file is missing or empty
- On lookup: compute HMAC, query by hash against the **core/index** RQLite registry (never a tenant RQLite)
- In-memory cache uses raw key as cache key (never persisted)
- Startup migrates leftover plaintext `ak_…` rows to HMAC in place; lookup is hashed-only after that

**TURN Secret Encryption (Step 1.15)**
- TURN shared secrets encrypted at rest in RQLite using AES-256-GCM
- Encryption key derived via HKDF from the cluster secret with purpose string `"turn-encryption"`

### TLS & Transport

**InsecureSkipVerify Fix (Step 1.10)**
- During node join, TLS verification uses TOFU (Trust On First Use)
- Invite token output includes the CA certificate fingerprint (SHA-256)
- Joining node verifies the server cert fingerprint matches before proceeding
- After join: CA cert stored locally for future connections

**WebSocket Origin Validation (Step 1.4)**
- All WebSocket upgraders validate the `Origin` header against the node's configured domain
- Non-browser clients (no Origin header) are still allowed
- Prevents cross-site WebSocket hijacking attacks

### Process Isolation

**Dedicated User (Step 1.11)**
- All services run as the `orama` user (not root)
- Caddy and CoreDNS get `AmbientCapabilities=CAP_NET_BIND_SERVICE` for ports 80/443 and 53
- WireGuard stays as root (kernel netlink requires it)
- vault-guardian already had proper hardening

**systemd Hardening (Step 1.12)**
- All service units include:
  ```ini
  ProtectSystem=strict
  ProtectHome=yes
  NoNewPrivileges=yes
  PrivateDevices=yes
  ProtectKernelTunables=yes
  ProtectKernelModules=yes
  RestrictNamespaces=yes
  ReadWritePaths=/opt/orama/.orama
  ```
- Applied to both template files (`pkg/environments/templates/`) and hardcoded unit generators (`pkg/environments/production/services.go`)

### Supply Chain

**Binary Signing (Step 1.13)**
- Build archives include `manifest.sig` — a rootwallet EVM signature of the manifest hash
- During install, the signature is verified against the embedded Orama public key
- Unsigned or tampered archives are rejected

## Phase 2: OramaOS

These measures apply only to OramaOS nodes (mainnet, devnet, testnet).

### Immutable OS

- **Read-only rootfs** — SquashFS. dm-verity hashes can be built into the image; they are **not** wired into the boot path, so integrity is not enforced at boot today
- **No shell** — `/bin/sh` symlinked to `/bin/false`, no bash/ash/ssh
- **No SSH** — OpenSSH not included in the image
- **Minimal packages** — only what's needed for systemd, cryptsetup, and the agent

### Full-Disk Encryption

- **LUKS2** with AES-XTS-Plain64 on the data partition
- **Shamir's Secret Sharing** over GF(256) — LUKS key split across peer vault-guardians
- **Adaptive threshold** — K = max(2, floor(N/3)) where N is the number of peers; writes require W = min(N, max(K+1, ceil(2N/3))) shares so a successful write is always recoverable
- **Key zeroing** — LUKS key wiped from memory immediately after use
- **Malicious share detection** — fetch K+1 shares when possible, verify consistency

### Service Sandboxing

Each service runs in isolated Linux namespaces:
- **CLONE_NEWNS** — mount namespace (filesystem isolation)
- **CLONE_NEWUTS** — hostname namespace
- **Dedicated UID/GID** — each service has its own user
- **Seccomp filtering** — per-service syscall allowlist

Note: CLONE_NEWPID is intentionally omitted — it makes services PID 1 in their namespace, which changes signal semantics (SIGTERM ignored by default for PID 1).

### Signed Updates

- A/B partition scheme with systemd-boot and boot counting (`tries_left=3`)
- All updates signed with rootwallet EVM signature (secp256k1 + keccak256)
- Signer address: `0xb5d8a496c8b2412990d7D467E17727fdF5954afC`
- P2P distribution over WireGuard between nodes
- Automatic rollback on 3 consecutive boot failures

### Zero Operator Access

- Operators cannot read data on the machine (LUKS encrypted, no shell)
- Management only through Gateway API → agent over WireGuard
- All commands are logged and auditable
- No root access, no console access, no file system access

## RAM-to-disk (swap and cores)

Guest `mlock`/`mlockall` is **not** used in the Go services. `mlock(2)` does not survive `execve`, so a launcher that locks then execs `rqlited`/`caddy`/`olric-server` gives those binaries zero locked pages. Go `string` values are also unzeroable. Against a hosting-provider RAM snapshot, mlock buys nothing.

The control that keeps secrets off the **block device** is cgroup `MemorySwapMax=0` on secret-bearing units, plus install-time `swapoff` / `fs.suid_dumpable=0` / systemd-coredump `Storage=none`. That does **not** stop a RAM snapshot or provider VM-suspend.

## What this does not defend against today

Stated so the gaps above are known positions, not implied protections:

- **RAM snapshot** of a running node (secrets in process memory, including gateway vault combine)
- **Hosting-provider / hypervisor access** to the guest
- **Ubuntu fleet SSH** — Zero Operator Access and LUKS FDE apply to OramaOS only
- **dm-verity at boot** — hashes may exist in the OramaOS image; they are not wired into the boot path
- **RQLite HTTP auth** — `rqlited -auth` is not enabled; overlay + firewall are the control
- **ntfy** — no auth-file in v1; listen-localhost is the control
- **A captured disk snapshot of RQLite** — plaintext application data, including `deployment_env_vars`

## Rollout Strategy

### Phase 1 Batches

```
Batch 1 (zero-risk, no restart):
  - CIDR fix
  - IPv6 disable
  - Internal endpoint auth
  - WebSocket origin check

Batch 2 (medium-risk, restart needed):
  - Hash refresh tokens
  - Hash API keys
  - Binary signing
  - Vault V1 auth enforcement
  - TURN secret encryption

Batch 3 (high-risk, coordinated rollout):
  - RQLite auth (followers first, leader last)
  - Olric encryption (simultaneous restart)
  - IPFS Cluster TrustedPeers

Batch 4 (infrastructure changes):
  - InsecureSkipVerify fix
  - Dedicated user
  - systemd hardening
```

### Phase 2

1. Build and test OramaOS image in QEMU
2. Deploy to sandbox cluster alongside Ubuntu nodes
3. Verify interop and stability
4. Gradual migration: testnet → devnet → mainnet (one node at a time, maintaining Raft quorum)

## Verification

All changes verified on sandbox cluster before production deployment:

- `make test` — all unit tests pass
- `orama monitor report --env sandbox` — full cluster health
- Manual endpoint testing (e.g., curl without auth → 401)
- Security-specific checks (IPv6 listeners, RQLite auth, binary signatures)
