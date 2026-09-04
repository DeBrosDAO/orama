# OramaOS Deployment Guide

OramaOS is a custom minimal Linux image built with Buildroot. It replaces the standard Ubuntu-based node deployment for mainnet, devnet, and testnet environments. Sandbox clusters remain on Ubuntu for development convenience.

## What is OramaOS?

OramaOS is a locked-down operating system designed specifically for Orama node operators. Key properties:

- **No SSH, no shell** — operators cannot access the filesystem or run commands on the machine
- **LUKS full-disk encryption** — the data partition is encrypted; the key is split via Shamir's Secret Sharing across peer nodes
- **Read-only rootfs** — SquashFS. dm-verity can be built into the image; it is **not** wired at boot, so rootfs integrity is not enforced today
- **A/B partition updates** — signed OS images are applied atomically with automatic rollback on failure
- **Service sandboxing** — each service runs in its own Linux namespace with seccomp syscall filtering
- **Signed binaries** — all updates are cryptographically signed with the Orama rootwallet

## Architecture

```
Partition Layout (devices as used by the agent):
  /dev/sda1 — rootfs-A (SquashFS, read-only, dm-verity)
  /dev/sda2 — rootfs-B (standby, for A/B updates)
  /dev/sda3 — data (LUKS2 encrypted, ext4)

Boot Flow:
  systemd-boot → dm-verity rootfs → orama-agent → WireGuard → services
```

Note: the GPT image as built by genimage (`os/buildroot/board/orama/genimage.cfg`) places the ESP (systemd-boot + kernel) as the first partition, followed by rootfs-A, rootfs-B, and data — which does not match the device paths the agent operates on (`PartitionA=/dev/sda1`, `PartitionB=/dev/sda2`, `DataDevice=/dev/sda3`). This inconsistency is unresolved in the current code.

The **orama-agent** is the only root process. It manages:
- Boot sequence and LUKS key reconstruction
- WireGuard tunnel setup
- Service lifecycle (start, stop, restart in sandboxed namespaces)
- Command reception from the Gateway over WireGuard
- OS updates (download, verify signature, A/B swap, boot counting)

## Enrollment Flow

OramaOS nodes join the cluster through an enrollment process (different from the Ubuntu `orama node install` flow):

### Step 1: Flash OramaOS to VPS

Download the OramaOS image and flash it to your VPS:

```bash
# Download image (URL provided upon acceptance)
wget https://releases.orama.network/oramaos-v1.0.0-amd64.qcow2

# Flash to VPS (provider-specific — Hetzner, Vultr, etc.)
# Most providers support uploading custom images via their dashboard
```

### Step 2: First Boot — Enrollment Mode

On first boot, the agent:
1. Generates a random 8-character registration code
2. Starts a temporary HTTP server on port 9999 that serves the code (one-shot)
3. Waits for the Gateway to push cluster config via HTTP `POST` to `/v1/agent/enroll/complete` on port 9999

The registration code is displayed on the VPS console (if available) and served at `http://<vps-ip>:9999/`.

### Step 3: Run Enrollment from CLI

On your local machine (where you have the `orama` CLI and rootwallet):

```bash
# Generate an invite token on any existing cluster node
orama node invite --expiry 24h

# Enroll the OramaOS node
orama node enroll --node-ip <vps-public-ip> --token <invite-token> --gateway <gateway-url>
```

The enrollment command:
1. Fetches the registration code from the node (port 9999)
2. Sends the code + invite token to the Gateway
3. Gateway validates everything, assigns a WireGuard IP, and pushes config to the node
4. Node configures WireGuard, formats the LUKS-encrypted data partition
5. LUKS key is split via Shamir and distributed to peer vault-guardians
6. Services start in sandboxed namespaces
7. Port 9999 closes permanently

### Step 4: Verify

```bash
# Check the node is online and healthy
orama monitor report --env <env>
```

## Genesis Node

The first OramaOS node in a cluster is the **genesis node**. It needs a special path because there are no peers yet for Shamir key distribution. **Most of this path is not yet implemented.**

What works today: when Shamir reconstruction fails after a reboot (genesis node, or peers offline), the agent falls back to **genesis unlock mode** — it starts an HTTP server on the WireGuard interface (port 9998) and waits for the operator to supply the raw LUKS key:

```bash
curl -X POST "http://<wg-ip>:9998/v1/agent/unlock" \
  -H "Content-Type: application/json" \
  -d '{"key":"<base64-encoded 32-byte LUKS key>"}'
```

Not yet implemented (do not rely on any of this):

- **Genesis enrollment.** Enrollment always tries to distribute Shamir shares and fails with "no peers available for key distribution" when the cluster has zero peers — there is no genesis fallback in the enrollment flow, so a genesis OramaOS node cannot currently complete enrollment.
- **Key escrow.** The agent never creates or stores a rootwallet-encrypted copy of the LUKS key, and serves no `GET /v1/agent/genesis-key` endpoint.
- **`orama node unlock --genesis --node-ip <wg-ip> --key-file <path>`.** `--key-file` is required, and it must hold a rootwallet-encrypted LUKS key. The command used to try `GET /v1/agent/genesis-key` first and spend ten seconds timing out on a path the agent has never served; that fetch is gone. Nothing currently produces the key file, so the flow is still not usable end to end — the missing piece is key escrow, not the CLI.
- **The 5-peer transition.** No agent code distributes Shamir shares once 5+ peers join, deletes a local escrowed key, or transitions to normal Shamir-based unlock.

## Normal Reboot (Shamir Unlock)

When an enrolled OramaOS node reboots:

1. Agent starts, brings up WireGuard
2. Contacts peer vault-guardians over WireGuard
3. Fetches K Shamir shares (K = threshold, typically `max(3, N/3)`)
4. Reconstructs LUKS key via Lagrange interpolation over GF(256)
5. Decrypts and mounts data partition
6. Starts all services
7. Zeros key from memory

If not enough peers are available, the agent retries the share fetch with exponential backoff (1s, 2s, 4s, 8s, 16s, max 5 retries). If reconstruction still fails, it falls back to genesis unlock mode and waits for a manual operator unlock on port 9998 (see [Genesis Node](#genesis-node)).

## Node Management

Since OramaOS has no SSH, all management happens through the Gateway API. Status, command, logs, and leave require an **admin** API key (or owner JWT). `service` on logs is an allowlist (`rqlite`, `olric`, `ipfs`, `ipfs-cluster`, `gateway`, `coredns`, `agent`).

```bash
# Check node status
curl "https://gateway.example.com/v1/node/status?node_id=<id>" \
  -H "Authorization: Bearer <admin-api-key>"

# Send a command (e.g., restart a service)
curl -X POST "https://gateway.example.com/v1/node/command?node_id=<id>" \
  -H "Authorization: Bearer <admin-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"action":"restart","service":"rqlite"}'

# View logs
curl "https://gateway.example.com/v1/node/logs?node_id=<id>&service=gateway&lines=100" \
  -H "Authorization: Bearer <admin-api-key>"

# Graceful node departure
curl -X POST "https://gateway.example.com/v1/node/leave" \
  -H "Authorization: Bearer <admin-api-key>" \
  -H "Content-Type: application/json" \
  -d '{"node_id":"<id>"}'
```

The Gateway proxies these requests to the agent over WireGuard (port 9998). The agent is never directly accessible from the public internet.

## OS Updates

OramaOS uses an A/B partition scheme for atomic, rollback-safe updates:

1. Agent checks for new versions hourly (`https://updates.orama.network/v1/latest`)
2. Downloads the signed image over HTTPS from the update server
3. Verifies the SHA-256 checksum and the rootwallet EVM signature against the embedded public key
4. Writes to the standby partition (if running from A, writes to B)
5. Sets systemd-boot to boot from B with `tries_left=3`
6. On the next reboot (the agent does not reboot automatically), the node boots into B
7. If B boots successfully (agent starts, WG connects, services healthy): marks B as "good"
8. If B fails 3 times: systemd-boot automatically falls back to A

Updates are staged automatically; activating one requires a reboot. Failed updates are automatically rolled back.

## Service Sandboxing

Each service on OramaOS runs in an isolated environment:

- **Mount namespace** — each service only sees its own data directory as writable; everything else is read-only
- **UTS namespace** — isolated hostname
- **Dedicated UID/GID** — each service runs as a different user (not root)
- **Seccomp filtering** — per-service syscall allowlist (initially in audit mode, then enforce mode)

Services and their sandbox profiles:
| Service | Writable Path | Extra Syscalls |
|---------|--------------|----------------|
| RQLite | `/opt/orama/.orama/data/rqlite` | fsync, fdatasync (Raft + SQLite WAL) |
| Olric | `/opt/orama/.orama/data/olric` | sendmmsg, recvmmsg (gossip) |
| IPFS | `/opt/orama/.orama/data/ipfs` | sendfile, splice (data transfer) |
| Gateway | `/opt/orama/.orama/data/gateway` | sendfile, splice (HTTP) |
| CoreDNS | `/opt/orama/.orama/data/coredns` | sendmmsg, recvmmsg (DNS) |

## OramaOS vs Ubuntu Deployment

| Feature | Ubuntu | OramaOS |
|---------|--------|---------|
| SSH access | Yes | No |
| Shell access | Yes | No |
| Disk encryption | No | LUKS2 (Shamir) |
| OS updates | Manual (`orama node upgrade`) | Automatic (signed, A/B) |
| Service isolation | systemd only | Namespaces + seccomp |
| Rootfs integrity | None | dm-verity hashes exist in the image; not wired at boot |
| Binary signing | Optional | Required |
| Operator data access | Full | None |
| Environments | All (including sandbox) | Mainnet, devnet, testnet |

## Cleaning / Factory Reset

OramaOS nodes cannot be cleaned with the standard `orama node clean` command (no SSH access). Instead:

- **Graceful departure:** `POST /v1/node/leave` on the Gateway API (see [Node Management](#node-management); there is no `orama node leave` CLI subcommand) — stops services, redistributes Shamir shares, removes WG peer
- **Cluster-side removal:** once the node is gone, `orama node remove --env <env> --node <ip> --offline` takes it out of raft, every namespace it served and the node registry from a survivor. `--offline` is required: the command never tries to reach an OramaOS node
- **Factory reset:** Reflash the OramaOS image on the VPS via the hosting provider's dashboard
- **Data is unrecoverable:** Since the LUKS key is distributed across peers, reflashing destroys all data permanently

## Troubleshooting

### Node stuck in enrollment mode
The node boots but enrollment never completes.

**Check:** Can you reach `http://<vps-ip>:9999/` from your machine? If not, the VPS firewall may be blocking port 9999.

**Fix:** Ensure port 9999 is open in the VPS provider's firewall. OramaOS opens it automatically via its internal firewall, but external provider firewalls (Hetzner, AWS security groups) must be configured separately.

### LUKS unlock fails (not enough peers)
After reboot, the node can't reconstruct its LUKS key.

**Check:** How many peer nodes are online? The node needs at least K peers (threshold) to be reachable over WireGuard.

**Fix:** Ensure enough cluster nodes are online. If reconstruction keeps failing, the agent falls back to genesis unlock mode and waits for a manual `POST /v1/agent/unlock` on port 9998 — see [Genesis Node](#genesis-node). (`orama node unlock --genesis` needs `--key-file` holding a rootwallet-encrypted LUKS key, and nothing produces one yet: key escrow is unimplemented.)

### Update failed, node rolled back
The node applied an update but reverted to the previous version.

**Check:** The agent logs will show why the new partition failed to boot (accessible via `GET /v1/node/logs?service=agent`).

**Common causes:** Corrupted download (signature verification should catch this), hardware issue, or incompatible configuration.

### Services not starting after reboot
The node rebooted and LUKS unlocked, but services are unhealthy.

**Check:** `GET /v1/node/status` — which services are down?

**Fix:** Try restarting the specific service via `POST /v1/node/command` with `{"action":"restart","service":"<name>"}`. If the issue persists, check service logs.
