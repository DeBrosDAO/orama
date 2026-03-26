# Orama Vault -- Deployment Guide

## Prerequisites

- **Zig 0.14.0+** (specified in `build.zig.zon` as `minimum_zig_version`). Zig 0.15+ recommended for the latest std library improvements.
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

> **Note:** YAML parsing is not yet implemented (Phase 2). The config loader currently returns defaults regardless of file contents. All configuration must be done via CLI arguments for now.

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
    shares/
        <identity_hash_hex>/
            share.bin       -- Encrypted share data
            version         -- Monotonic version counter
            checksum.bin    -- HMAC-SHA256 integrity checksum
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

The project includes a systemd service file at `systemd/orama-vault.service`:

```ini
[Unit]
Description=Orama Vault Guardian
Documentation=https://github.com/orama-network/debros
After=network.target
PartOf=orama-node.service

[Service]
Type=simple
ExecStart=/opt/orama/bin/vault-guardian --config /opt/orama/.orama/data/vault/vault.yaml
Restart=on-failure
RestartSec=5s

# Security hardening
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/opt/orama/.orama/data/vault
NoNewPrivileges=yes

# Allow mlock for secure memory
LimitMEMLOCK=67108864

# Resource limits
MemoryMax=512M

[Install]
WantedBy=multi-user.target
```

### Installation

```bash
# Copy binary
sudo cp zig-out/bin/vault-guardian /opt/orama/bin/vault-guardian
sudo chmod 755 /opt/orama/bin/vault-guardian

# Copy service file
sudo cp systemd/orama-vault.service /etc/systemd/system/orama-vault.service

# Reload systemd
sudo systemctl daemon-reload

# Enable and start
sudo systemctl enable orama-vault
sudo systemctl start orama-vault

# Check status
sudo systemctl status orama-vault
```

### Service Dependencies

The service is `PartOf=orama-node.service`, meaning:

- When `orama-node.service` is stopped, `orama-vault` is also stopped.
- When `orama-node.service` is restarted, `orama-vault` is also restarted.
- `After=network.target` ensures the network stack is up before the guardian starts.

### Restart Behavior

- `Restart=on-failure`: The guardian restarts if it exits with a non-zero status.
- `RestartSec=5s`: Wait 5 seconds between restarts.
- The guardian generates a new server secret on each start, which invalidates all existing session tokens. This is intentional -- sessions should not survive restarts.

---

## Firewall Rules

### UFW Configuration

```bash
# Client port: accessible from WireGuard overlay and optionally from gateway
sudo ufw allow from 10.0.0.0/24 to any port 7500 proto tcp comment "vault-guardian client"

# Peer port: WireGuard overlay ONLY
sudo ufw allow from 10.0.0.0/24 to any port 7501 proto tcp comment "vault-guardian peer"
```

### Port Summary

| Port | Protocol | Interface | Purpose |
|------|----------|-----------|---------|
| 7500 | TCP | WireGuard (10.0.0.x) | Client-facing HTTP API |
| 7501 | TCP | WireGuard (10.0.0.x) only | Guardian-to-guardian binary protocol |

**Port 7501 must NEVER be exposed on the public interface.** The peer protocol has no authentication beyond WireGuard -- it trusts that only authorized nodes can reach it.

Port 7500 may be exposed on the public interface if the node runs without the Orama gateway reverse proxy, but this is not recommended for production.

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
curl http://127.0.0.1:7500/v1/vault/health

# Expected response:
# {"status":"ok","version":"0.1.0"}

# Check status endpoint
curl http://127.0.0.1:7500/v1/vault/status
```

---

## Environment-Specific Notes

### Development / Local Testing

```bash
# Run with a local data directory, non-default port
./zig-out/bin/vault-guardian --data-dir /tmp/vault-dev --port 7500 --bind 127.0.0.1
```

The guardian runs in single-node mode when it cannot reach RQLite. This is normal for local development.

### Staging / Testnet

Same binary, deployed to testnet nodes. Use the standard config path:

```bash
vault-guardian --config /opt/orama/.orama/data/vault/vault.yaml
```

### Production / Mainnet

- Ensure WireGuard is up and peers are connected before starting the guardian.
- Ensure RQLite is running and healthy (the guardian queries it for node discovery).
- Verify firewall rules restrict port 7501 to WireGuard interfaces only.
- Monitor via the health endpoint and systemd journal.
