# OramaOS Deployment Guide

OramaOS is a custom minimal Linux image built with Buildroot. It replaces the standard Ubuntu-based node deployment for mainnet, devnet, and testnet environments. Sandbox clusters remain on Ubuntu for development convenience.

## What is OramaOS?

OramaOS is a locked-down operating system designed specifically for Orama node operators. Key properties:

- **No SSH, no shell** — operators cannot access the filesystem or run commands on the machine
- **LUKS full-disk encryption** — the data partition is encrypted; the key is split via Shamir's Secret Sharing across peer nodes
- **Read-only rootfs** — the OS image uses SquashFS with dm-verity integrity verification
- **A/B partition updates** — signed OS images are applied atomically with automatic rollback on failure
- **Service sandboxing** — each service runs in its own Linux namespace with seccomp syscall filtering
- **Signed binaries** — all updates are cryptographically signed with the Orama rootwallet

## Architecture

```
Partition Layout:
  /dev/sda1 — ESP (EFI System Partition, systemd-boot)
  /dev/sda2 — rootfs-A (SquashFS, read-only, dm-verity)
  /dev/sda3 — rootfs-B (standby, for A/B updates)
  /dev/sda4 — data (LUKS2 encrypted, ext4)

Boot Flow:
  systemd-boot → dm-verity rootfs → orama-agent → WireGuard → services
```

The **orama-agent** is the only root process. It manages:
- Boot sequence and LUKS key reconstruction
- WireGuard tunnel setup
- Service lifecycle (start, stop, restart in sandboxed namespaces)
- Command reception from the Gateway over WireGuard
- OS updates (download, verify signature, A/B swap, reboot)

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
2. Starts a temporary HTTP server on port 9999
3. Opens an outbound WebSocket to the Gateway
4. Waits for enrollment to complete

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

The first OramaOS node in a cluster is the **genesis node**. It has a special boot path because there are no peers yet for Shamir key distribution:

1. Genesis generates a LUKS key and encrypts the data partition
2. The LUKS key is encrypted with a rootwallet-derived key and stored on the unencrypted rootfs
3. On reboot (before enough peers exist), the operator must manually unlock:

```bash
orama node unlock --genesis --node-ip <wg-ip>
```

This command:
1. Fetches the encrypted genesis key from the node
2. Decrypts it using the rootwallet (`rw decrypt`)
3. Sends the decrypted LUKS key to the agent over WireGuard

Once 5+ peers have joined, the genesis node distributes Shamir shares to peers, deletes the local encrypted key, and transitions to normal Shamir-based unlock. After this transition, `orama node unlock` is no longer needed.

## Normal Reboot (Shamir Unlock)

When an enrolled OramaOS node reboots:

1. Agent starts, brings up WireGuard
2. Contacts peer vault-guardians over WireGuard
3. Fetches K Shamir shares (K = threshold, typically `max(3, N/3)`)
4. Reconstructs LUKS key via Lagrange interpolation over GF(256)
5. Decrypts and mounts data partition
6. Starts all services
7. Zeros key from memory

If not enough peers are available, the agent enters a degraded "waiting for peers" state and retries with exponential backoff (1s, 2s, 4s, 8s, 16s, max 5 retries per cycle).

## Node Management

Since OramaOS has no SSH, all management happens through the Gateway API:

```bash
# Check node status
curl "https://gateway.example.com/v1/node/status?node_id=<id>"

# Send a command (e.g., restart a service)
curl -X POST "https://gateway.example.com/v1/node/command?node_id=<id>" \
  -H "Content-Type: application/json" \
  -d '{"action":"restart","service":"rqlite"}'

# View logs
curl "https://gateway.example.com/v1/node/logs?node_id=<id>&service=gateway&lines=100"

# Graceful node departure
curl -X POST "https://gateway.example.com/v1/node/leave" \
  -H "Content-Type: application/json" \
  -d '{"node_id":"<id>"}'
```

The Gateway proxies these requests to the agent over WireGuard (port 9998). The agent is never directly accessible from the public internet.

## OS Updates

OramaOS uses an A/B partition scheme for atomic, rollback-safe updates:

1. Agent periodically checks for new versions
2. Downloads the signed image (P2P over WireGuard between nodes)
3. Verifies the rootwallet EVM signature against the embedded public key
4. Writes to the standby partition (if running from A, writes to B)
5. Sets systemd-boot to boot from B with `tries_left=3`
6. Reboots
7. If B boots successfully (agent starts, WG connects, services healthy): marks B as "good"
8. If B fails 3 times: systemd-boot automatically falls back to A

No operator intervention is needed for updates. Failed updates are automatically rolled back.

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
| Rootfs integrity | None | dm-verity |
| Binary signing | Optional | Required |
| Operator data access | Full | None |
| Environments | All (including sandbox) | Mainnet, devnet, testnet |

## Cleaning / Factory Reset

OramaOS nodes cannot be cleaned with the standard `orama node clean` command (no SSH access). Instead:

- **Graceful departure:** `orama node leave` via the Gateway API — stops services, redistributes Shamir shares, removes WG peer
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

**Fix:** Ensure enough cluster nodes are online. If this is the genesis node and fewer than 5 peers exist, use:
```bash
orama node unlock --genesis --node-ip <wg-ip>
```

### Update failed, node rolled back
The node applied an update but reverted to the previous version.

**Check:** The agent logs will show why the new partition failed to boot (accessible via `GET /v1/node/logs?service=agent`).

**Common causes:** Corrupted download (signature verification should catch this), hardware issue, or incompatible configuration.

### Services not starting after reboot
The node rebooted and LUKS unlocked, but services are unhealthy.

**Check:** `GET /v1/node/status` — which services are down?

**Fix:** Try restarting the specific service via `POST /v1/node/command` with `{"action":"restart","service":"<name>"}`. If the issue persists, check service logs.
