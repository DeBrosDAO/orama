# Orama Vault -- Deployment Guide

## Prerequisites

- **Zig 0.15.2+** (specified in `build.zig.zon` as `minimum_zig_version`). Older versions, including 0.15.0/0.15.1, are refused by the build.
- **Linux x86_64** target for production nodes.
- **macOS (arm64 or x86_64)** for local development and testing.

No external dependencies. The guardian is a single static binary with no runtime library requirements (musl libc, statically linked).

---

## Building

### Production Build (Linux target)

From the project root:

```bash
zig build -Dtarget=x86_64-linux-musl -Doptimize=ReleaseSafe
```

This produces:

```
zig-out/bin/vault-guardian
```

The binary is statically linked against musl libc, so it runs on any Linux x86_64 system regardless of glibc version.

**Build options:**

| Flag | Description |
|------|-------------|
| `-Dtarget=x86_64-linux-musl` | Cross-compile for Linux x86_64 with musl (static) |
| `-Doptimize=ReleaseSafe` | Optimized with safety checks (recommended for production) |
| `-Doptimize=ReleaseFast` | Maximum optimization, no safety checks |
| `-Doptimize=Debug` | Debug build with full debug info |

### Development Build (native)

```bash
zig build
```

Produces a debug binary for the host platform at `zig-out/bin/vault-guardian`.

### Running Tests

```bash
zig build test
```

Runs all unit tests across every module (SSS, crypto, storage, auth, membership, peer protocol, server). The test entry point is `src/tests.zig`, which imports all test modules via comptime.

### Run Locally

```bash
zig build run -- --data-dir /tmp/vault-test --port 7500 --bind 127.0.0.1
```

Or after building:

```bash
./zig-out/bin/vault-guardian --data-dir /tmp/vault-test --port 7500 --bind 127.0.0.1
```

---

## CLI Arguments

```
Usage: vault-guardian [OPTIONS]

Orama Vault Guardian -- distributed secret share storage

Options:
  --config <path>   Path to config file (default: /opt/orama/.orama/data/vault/vault.yaml)
  --data-dir <path> Override data directory
  --port <port>     Override client port (default: 7500)
  --bind <addr>     Override bind address (default: 0.0.0.0)
  --help, -h        Show this help
  --version, -v     Show version
```

CLI arguments override config file values.

---

## Configuration

The guardian reads a config file from `--config` (default: `/opt/orama/.orama/data/vault/vault.yaml`).

> **Note:** Despite the `.yaml` default filename, the config format is simple `key=value` lines, not YAML. Lines starting with `#` are comments; unknown keys are ignored. If the file does not exist, defaults are used. CLI arguments override config file values.

Example config file:

```
# vault-guardian config
listen_address = 0.0.0.0
client_port = 7500
peer_port = 7501
data_dir = /opt/orama/.orama/data/vault
rqlite_url = http://127.0.0.1:4001
```

### Config Fields

| Field | Default | Description |
|-------|---------|-------------|
| `listen_address` | `0.0.0.0` | Address to bind both client and peer listeners |
| `client_port` | `7500` | Client-facing HTTP port |
| `peer_port` | `7501` | Guardian-to-guardian binary protocol port |
| `data_dir` | `/opt/orama/.orama/data/vault` | Directory for share storage |
| `rqlite_url` | `http://127.0.0.1:4001` | RQLite endpoint for node discovery |

---

## Data Directory Setup

The guardian creates the data directory on startup if it does not exist. The directory structure:

```
/opt/orama/.orama/data/vault/
    integrity.key           -- Persistent at-rest integrity key (created on first run, 0600)
    shares/
        <identity_hash_hex>/
            share.bin       -- Encrypted share data
            checksum.bin    -- HMAC-SHA256 integrity checksum
            meta.json       -- {"version":<n>,"threshold":<k>} (anti-rollback)
```

### Permissions

The data directory must be writable by the guardian process. With the systemd service, only `/opt/orama/.orama/data/vault` is writable (`ReadWritePaths`).

```bash
# Create data directory
sudo mkdir -p /opt/orama/.orama/data/vault
sudo chown orama:orama /opt/orama/.orama/data/vault
sudo chmod 700 /opt/orama/.orama/data/vault
```

---

## Systemd Service

The live unit is `orama-namespace-vault@index`, started by `orama-node`. Data stays at `/opt/orama/.orama/data/vault` (adopt in place). The template lives at `core/systemd/orama-namespace-vault@.service`.

```ini
[Unit]
Description=Orama Namespace Vault Guardian (%i)
After=network-online.target orama-namespace-wireguard@%i.service
PartOf=orama-node.service
```

### Installation

```bash
# Copy binary
sudo cp zig-out/bin/vault-guardian /opt/orama/bin/vault-guardian
sudo chmod 755 /opt/orama/bin/vault-guardian

# Check status
sudo systemctl status orama-namespace-vault@index
```

### Service Dependencies

The service is `PartOf=orama-node.service`, meaning:

- When `orama-node.service` is stopped, `orama-namespace-vault@index` is also stopped.
- When `orama-node.service` is restarted, the supervisor starts vault again.
- WireGuard is up first (`After=orama-namespace-wireguard@index`).

### Restart Behavior

- `Restart=on-failure`: The guardian restarts if it exits with a non-zero status.
- `RestartSec=5s`: Wait 5 seconds between restarts.
- The guardian generates a new server secret on each start, which invalidates all existing session tokens. This is intentional -- sessions should not survive restarts.

---

## Firewall Rules

### UFW Configuration

```bash
# Client port: accessible from WireGuard overlay and optionally from gateway
sudo ufw allow from 10.0.0.0/24 to any port 10106 proto tcp comment "vault-guardian client"

# Peer port: WireGuard overlay ONLY
sudo ufw allow from 10.0.0.0/24 to any port 7501 proto tcp comment "vault-guardian peer"
```

### Port Summary

| Port | Protocol | Interface | Purpose |
|------|----------|-----------|---------|
| 10106 | TCP | WireGuard (10.0.0.x) | Client-facing HTTP API (Orama production `vault.yaml`) |
| 7501 | TCP | WireGuard (10.0.0.x) only | Guardian-to-guardian binary protocol (reserved -- no listener yet) |

> **Note:** In v0.1.0 the daemon does not start the peer listener, so nothing accepts connections on port 7501 yet. The firewall rule is forward-looking for when the peer protocol is wired in.

**Port 7501 must NEVER be exposed on the public interface.** The peer protocol has no authentication beyond WireGuard -- it trusts that only authorized nodes can reach it.

The standalone vault binary still defaults to `--port 7500` when run without Orama's `vault.yaml`. Production installs write `client_port = 10106`. Do not expose the client port on the public interface; the gateway reverse-proxies it.

---

## Cross-Compilation

Zig's cross-compilation makes it trivial to build for any target:

```bash
# Linux x86_64 (production)
zig build -Dtarget=x86_64-linux-musl -Doptimize=ReleaseSafe

# Linux aarch64 (ARM servers)
zig build -Dtarget=aarch64-linux-musl -Doptimize=ReleaseSafe

# macOS x86_64
zig build -Dtarget=x86_64-macos -Doptimize=ReleaseSafe

# macOS aarch64 (Apple Silicon)
zig build -Dtarget=aarch64-macos -Doptimize=ReleaseSafe
```

All targets produce a single static binary. No runtime dependencies to install on the target system.

---

## Deployment to Orama Network Nodes

The vault guardian is deployed alongside other Orama services. The typical deployment workflow:

1. Build the binary on the development machine (cross-compile for Linux).
2. Copy the binary to the target node via `scp` or the `orama` CLI deploy tool.
3. Place the binary at `/opt/orama/bin/vault-guardian`.
4. Ensure the systemd service is installed and enabled.
5. Restart the service: `sudo systemctl restart orama-vault`.

For rolling upgrades across the cluster, follow the standard Orama network rolling upgrade protocol: upgrade one node at a time, verify health between each node.

### Health Verification After Deploy

```bash
# Check systemd service
sudo systemctl status orama-vault

# Check health endpoint
curl http://127.0.0.1:10106/v1/vault/health

# Expected response:
# {"status":"degraded","version":"0.1.0","shares":0,"peers":0,"data_dir_ok":true}

# Check status endpoint
curl http://127.0.0.1:10106/v1/vault/status
```

`"status":"degraded"` with `"peers":0` is the expected healthy state today: peer discovery via RQLite is not yet implemented, so every node runs with zero alive peers. A failed deploy shows up as `"status":"unhealthy"` (data directory inaccessible), `"data_dir_ok":false`, or no response at all.

---

## Environment-Specific Notes

### Development / Local Testing

```bash
# Run with a local data directory, non-default port
./zig-out/bin/vault-guardian --data-dir /tmp/vault-dev --port 7500 --bind 127.0.0.1
```

The guardian always runs in single-node mode today: RQLite node discovery is not yet implemented (the fetch returns an empty node list), so no peers are ever known. This is normal for local development and, for now, for deployed nodes too.

### Staging / Testnet

Same binary, deployed to testnet nodes. Use the standard config path:

```bash
vault-guardian --config /opt/orama/.orama/data/vault/vault.yaml
```

### Production / Mainnet

- Ensure WireGuard is up and peers are connected before starting the guardian.
- RQLite-based node discovery is not yet implemented -- the guardian currently runs single-node regardless of RQLite state. The `rqlite_url` config value is reserved for when discovery lands.
- Verify firewall rules restrict port 7501 to WireGuard interfaces only.
- Monitor via the health endpoint and systemd journal.
