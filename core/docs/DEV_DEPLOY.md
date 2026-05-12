# Development Guide

## Prerequisites

- Go 1.21+
- Node.js 18+ (for anyone-client in dev mode)
- macOS or Linux

## Building

```bash
# Build all binaries
make build

# Outputs:
#   bin/orama-node   — the node binary
#   bin/orama        — the CLI
#   bin/gateway      — standalone gateway (optional)
#   bin/identity     — identity tool
```

## Running Tests

```bash
make test
```

## Deploying to VPS

All binaries are pre-compiled locally and shipped as a binary archive. Zero compilation on the VPS.

### Deploy Workflow

```bash
# One-command: build + push + rolling upgrade
orama node rollout --env testnet

# Or step by step:

# 1. Build binary archive (cross-compiles all binaries for linux/amd64)
orama build
# Creates: /tmp/orama-<version>-linux-amd64.tar.gz

# 2. Push archive to all nodes (fanout via hub node)
orama node push --env testnet

# 3. Rolling upgrade (one node at a time, followers first, leader last)
orama node upgrade --env testnet
```

### Fresh Node Install

```bash
# Build the archive first (if not already built)
orama build

# Install on a new VPS (auto-uploads binary archive, zero compilation)
orama node install --vps-ip <ip> --nameserver --domain <domain> --base-domain <domain>
```

The installer auto-detects the binary archive at `/opt/orama/manifest.json` and copies pre-built binaries instead of compiling from source.

### Upgrading a Multi-Node Cluster (CRITICAL)

**NEVER restart all nodes simultaneously.** RQLite uses Raft consensus and requires a majority (quorum) to function.

#### Safe Upgrade Procedure

```bash
# Full rollout (build + push + rolling upgrade, one command)
orama node rollout --env testnet

# Or with more control:
orama node push --env testnet                     # Push archive to all nodes
orama node upgrade --env testnet                  # Rolling upgrade (auto-detects leader)
orama node upgrade --env testnet --node 1.2.3.4   # Single node only
orama node upgrade --env testnet --delay 60       # 60s between nodes
```

The rolling upgrade automatically:
1. Upgrades **follower** nodes first
2. Upgrades the **leader** last
3. Waits a configurable delay between nodes (default: 30s)

After each node, verify health:
```bash
orama monitor report --env testnet
```

#### What NOT to Do

- **DON'T** stop all nodes, replace binaries, then start all nodes
- **DON'T** run `orama node upgrade --restart` on multiple nodes in parallel
- **DON'T** clear RQLite data directories unless doing a full cluster rebuild
- **DON'T** use `systemctl stop orama-node` on multiple nodes simultaneously

#### Schema-Migration Ordering Invariant

The gateway binary embeds a set of SQL migrations. The highest-numbered migration is the schema version that binary REQUIRES — **the gateway will refuse to start if its required schema isn't applied** (the schema-version contract added after the 2026-05-06 incident).

This means rolling upgrades have ONE invariant you must respect:

> The new gateway binary's required migrations must be applied to RQLite **before or as part of** starting the new binary on a node.

There are two acceptable patterns:

**Pattern A — let the gateway apply migrations on startup (default).**
The gateway calls `ApplyEmbeddedMigrations` during `NewDependencies` and asserts the schema is at the required version before serving traffic. If the apply succeeds, you're done. If a transient error blocks the apply, gateway startup aborts with a clear `schema mismatch: binary requires version N, database has M` error.

This is the default for both the genesis startup flow and rolling upgrades. No operator action required when it works.

**Pattern B — pre-apply migrations explicitly via the CLI.**
On any node:
```bash
sudo orama node schema status      # show binary required vs applied
sudo orama node schema apply --yes # apply pending migrations
```
Then start the new gateway. Useful when you want explicit control during a high-risk upgrade or when the auto-apply path is failing for reasons you want to debug separately.

#### Verifying schema state remotely

Tenants can self-check schema drift without SSH access via:
```
GET /v1/schema-status
```
Returns `{ok, required_version, applied_version, in_sync, pending: [...]}`. The same data is available via `orama node schema status` for operators with shell access.

#### Build-time guard (CI)

`go test ./migrations/` runs a roundtrip test that opens an in-memory SQLite, applies every embedded migration, and exercises representative SQL operations from the platform's Go code. If a Go handler is added that references a column no migration creates, the test fails — drift is caught at PR review time, not at production deploy.

When adding a new platform table or column:
1. Write the migration in `core/migrations/NNN_description.sql`
2. Update the relevant Go code that reads/writes the new column
3. Add an exemplar to `migrations/roundtrip_test.go` mirroring the new SQL — this enforces the contract permanently

#### Recovery from Cluster Split

If nodes get stuck in "Candidate" state or show "leader not found" errors:

```bash
# Recover the Raft cluster (specify the node with highest commit index as leader)
orama node recover-raft --env testnet --leader 1.2.3.4
```

This will:
1. Stop orama-node on ALL nodes
2. Backup + delete raft/ on non-leader nodes
3. Start the leader, wait for Leader state
4. Start remaining nodes in batches
5. Verify cluster health

### Cleaning Nodes for Reinstallation

```bash
# Wipe all data and services (preserves Anyone relay keys)
orama node clean --env testnet --force

# Also remove shared binaries (rqlited, ipfs, caddy, etc.)
orama node clean --env testnet --nuclear --force

# Single node only
orama node clean --env testnet --node 1.2.3.4 --force
```

### Push Options

```bash
orama node push --env devnet                     # Fanout via hub (default, fastest)
orama node push --env testnet --node 1.2.3.4     # Single node
orama node push --env testnet --direct            # Sequential, no fanout
```

### CLI Flags Reference

#### `orama node install`

| Flag | Description |
|------|-------------|
| `--vps-ip <ip>` | VPS public IP address (required) |
| `--domain <domain>` | Domain for HTTPS certificates. Required for nameserver nodes (use the base domain, e.g., `example.com`). Auto-generated for non-nameserver nodes if omitted (e.g., `node-a3f8k2.example.com`) |
| `--base-domain <domain>` | Base domain for deployment routing (e.g., example.com) |
| `--nameserver` | Configure this node as a nameserver (CoreDNS + Caddy) |
| `--join <url>` | Join existing cluster via HTTPS URL (e.g., `https://node1.example.com`) |
| `--token <token>` | Invite token for joining (from `orama node invite` on existing node) |
| `--force` | Force reconfiguration even if already installed |
| `--skip-firewall` | Skip UFW firewall setup |
| `--skip-checks` | Skip minimum resource checks (RAM/CPU) |
| `--anyone-relay` | Install and configure an Anyone relay on this node |
| `--anyone-migrate` | Migrate existing Anyone relay installation (preserves keys/fingerprint) |
| `--anyone-nickname <name>` | Relay nickname (required for relay mode) |
| `--anyone-wallet <addr>` | Ethereum wallet for relay rewards (required for relay mode) |
| `--anyone-contact <info>` | Contact info for relay (required for relay mode) |
| `--anyone-family <fps>` | Comma-separated fingerprints of related relays (MyFamily) |
| `--anyone-orport <port>` | ORPort for relay (default: 9001) |
| `--anyone-exit` | Configure as an exit relay (default: non-exit) |
| `--anyone-bandwidth <pct>` | Limit relay to N% of VPS bandwidth (default: 30, 0=unlimited). Runs a speedtest during install to measure available bandwidth |
| `--anyone-accounting <GB>` | Monthly data cap for relay in GB (0=unlimited) |

#### `orama node invite`

| Flag | Description |
|------|-------------|
| `--expiry <duration>` | Token expiry duration (default: 1h, e.g. `--expiry 24h`) |

**Important notes about invite tokens:**

- **Tokens are single-use.** Once a node consumes a token during the join handshake, it cannot be reused. Generate a separate token for each node you want to join.
- **Expiry is checked in UTC.** RQLite uses `datetime('now')` which is always UTC. If your local timezone differs, account for the offset when choosing expiry durations.
- **Use longer expiry for multi-node deployments.** When deploying multiple nodes, use `--expiry 24h` to avoid tokens expiring mid-deployment.

#### `orama node upgrade`

| Flag | Description |
|------|-------------|
| `--restart` | Restart all services after upgrade (local mode) |
| `--env <env>` | Target environment for remote rolling upgrade |
| `--node <ip>` | Upgrade a single node only |
| `--delay <seconds>` | Delay between nodes during rolling upgrade (default: 30) |
| `--anyone-relay` | Enable Anyone relay (same flags as install) |
| `--anyone-bandwidth <pct>` | Limit relay to N% of VPS bandwidth (default: 30, 0=unlimited) |
| `--anyone-accounting <GB>` | Monthly data cap for relay in GB (0=unlimited) |

#### `orama build`

| Flag | Description |
|------|-------------|
| `--arch <arch>` | Target architecture (default: amd64) |
| `--output <path>` | Output archive path |
| `--verbose` | Verbose build output |

#### `orama node push`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--node <ip>` | Push to a single node only |
| `--direct` | Sequential upload (no hub fanout) |

#### `orama node rollout`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--no-build` | Skip the build step |
| `--yes` | Skip confirmation |
| `--delay <seconds>` | Delay between nodes (default: 30) |

#### `orama node clean`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--node <ip>` | Clean a single node only |
| `--nuclear` | Also remove shared binaries |
| `--force` | Skip confirmation (DESTRUCTIVE) |

#### `orama node recover-raft`

| Flag | Description |
|------|-------------|
| `--env <env>` | Target environment (required) |
| `--leader <ip>` | Leader node IP — highest commit index (required) |
| `--force` | Skip confirmation (DESTRUCTIVE) |

#### `orama node` (Service Management)

Use these commands to manage services on production nodes:

```bash
# Stop all services (orama-node, coredns, caddy)
sudo orama node stop

# Start all services
sudo orama node start

# Restart all services
sudo orama node restart

# Check service status
sudo orama node status

# Diagnose common issues
sudo orama node doctor
```

**Note:** Always use `orama node stop` instead of manually running `systemctl stop`. The CLI ensures all related services (including CoreDNS and Caddy on nameserver nodes) are handled correctly.

#### `orama node report`

Outputs comprehensive health data as JSON. Used by `orama monitor` over SSH:

```bash
sudo orama node report --json
```

See [MONITORING.md](MONITORING.md) for full details.

#### `orama monitor`

Real-time cluster monitoring from your local machine:

```bash
# Interactive TUI
orama monitor --env testnet

# Cluster overview
orama monitor cluster --env testnet

# Alerts only
orama monitor alerts --env testnet

# Full JSON for LLM analysis
orama monitor report --env testnet
```

See [MONITORING.md](MONITORING.md) for all subcommands and flags.

### Node Join Flow

```bash
# 1. Genesis node (first node, creates cluster)
# Nameserver nodes use the base domain as --domain
sudo orama node install --vps-ip 1.2.3.4 --domain example.com \
    --base-domain example.com --nameserver

# 2. On genesis node, generate an invite
orama node invite --expiry 24h
# Output: sudo orama node install --join https://example.com --token <TOKEN> --vps-ip <IP>

# 3a. Join as nameserver (requires --domain set to base domain)
sudo orama node install --join http://1.2.3.4 --token abc123... \
    --vps-ip 5.6.7.8 --domain example.com --base-domain example.com --nameserver

# 3b. Join as regular node (domain auto-generated, no --domain needed)
sudo orama node install --join http://1.2.3.4 --token abc123... \
    --vps-ip 5.6.7.8 --base-domain example.com
```

The join flow establishes a WireGuard VPN tunnel before starting cluster services.
All inter-node communication (RQLite, IPFS, Olric) uses WireGuard IPs (10.0.0.x).
No cluster ports are ever exposed publicly.

#### DNS Prerequisite

The `--join` URL should use the HTTPS domain of the genesis node (e.g., `https://node1.example.com`).
For this to work, the domain registrar for `example.com` must have NS records pointing to the genesis
node's IP so that `node1.example.com` resolves publicly.

**If DNS is not yet configured**, you can use the genesis node's public IP with HTTP as a fallback:

```bash
sudo orama node install --join http://1.2.3.4 --vps-ip 5.6.7.8 --token abc123... --nameserver
```

This works because Caddy's `:80` block proxies all HTTP traffic to the gateway. However, once DNS
is properly configured, always use the HTTPS domain URL.

**Important:** Never use `http://<ip>:6001` — port 6001 is the internal gateway and is blocked by
UFW from external access. The join request goes through Caddy on port 80 (HTTP) or 443 (HTTPS),
which proxies to the gateway internally.

## OramaOS Enrollment

For OramaOS nodes (mainnet, devnet, testnet), use the enrollment flow instead of `orama node install`:

```bash
# 1. Flash OramaOS image to VPS (via provider dashboard)
# 2. Generate invite token on existing cluster node
orama node invite --expiry 24h

# 3. Enroll the OramaOS node
orama node enroll --node-ip <vps-public-ip> --token <invite-token> --gateway <gateway-url>

# 4. For genesis node reboots (before 5+ peers exist)
orama node unlock --genesis --node-ip <wg-ip>
```

OramaOS nodes have no SSH access. All management happens through the Gateway API:

```bash
# Status, logs, commands — all via Gateway proxy
curl "https://gateway.example.com/v1/node/status?node_id=<id>"
curl "https://gateway.example.com/v1/node/logs?node_id=<id>&service=gateway"
```

See [ORAMAOS_DEPLOYMENT.md](ORAMAOS_DEPLOYMENT.md) for the full guide.

**Note:** `orama node clean` does not work on OramaOS nodes (no SSH). Use `orama node leave` for graceful departure, or reflash the image for a factory reset.

## Pre-Install Checklist (Ubuntu Only)

Before running `orama node install` on a VPS, ensure:

1. **Stop Docker if running.** Docker commonly binds ports 4001 and 8080 which conflict with IPFS. The installer checks for port conflicts and shows which process is using each port, but it's easier to stop Docker first:
   ```bash
   sudo systemctl stop docker docker.socket
   sudo systemctl disable docker docker.socket
   ```

2. **Stop any existing IPFS instance.**
   ```bash
   sudo systemctl stop ipfs
   ```

3. **Stop any service on port 53** (for nameserver nodes). The installer handles `systemd-resolved` automatically, but other DNS services (like `bind9` or `dnsmasq`) must be stopped manually.

## Recovering from Failed Joins

If a node partially joins the cluster (registers in RQLite's Raft but then fails or gets cleaned), the remaining cluster can lose quorum permanently. This happens because RQLite thinks there are N voters but only N-1 are reachable.

**Symptoms:** RQLite stuck in "Candidate" state, no leader elected, all writes fail.

**Solution:** Do a full clean reinstall of all affected nodes. Use [CLEAN_NODE.md](CLEAN_NODE.md) to reset each node, then reinstall starting from the genesis node.

**Prevention:** Always ensure a joining node can complete the full installation before it joins. The installer validates port availability upfront to catch conflicts early.

## Debugging Production Issues

Always follow the local-first approach:

1. **Reproduce locally** — set up the same conditions on your machine
2. **Find the root cause** — understand why it's happening
3. **Fix in the codebase** — make changes to the source code
4. **Test locally** — run `make test` and verify
5. **Deploy** — only then deploy the fix to production

Never fix issues directly on the server — those fixes are lost on next deployment.

## Trusting the Self-Signed TLS Certificate

When Let's Encrypt is rate-limited, Caddy falls back to its internal CA (self-signed certificates). Browsers will show security warnings unless you install the root CA certificate.

### Downloading the Root CA Certificate

From VPS 1 (or any node), copy the certificate:

```bash
# Copy the cert to an accessible location on the VPS
ssh ubuntu@<VPS_IP> "sudo cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /tmp/caddy-root-ca.crt && sudo chmod 644 /tmp/caddy-root-ca.crt"

# Download to your local machine
scp ubuntu@<VPS_IP>:/tmp/caddy-root-ca.crt ~/Downloads/caddy-root-ca.crt
```

### macOS

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ~/Downloads/caddy-root-ca.crt
```

This adds the cert system-wide. All browsers (Safari, Chrome, Arc, etc.) will trust it immediately. Firefox uses its own certificate store — go to **Settings > Privacy & Security > Certificates > View Certificates > Import** and import the `.crt` file there.

To remove it later:
```bash
sudo security remove-trusted-cert -d ~/Downloads/caddy-root-ca.crt
```

### iOS (iPhone/iPad)

1. Transfer `caddy-root-ca.crt` to your device (AirDrop, email attachment, or host it on a URL)
2. Open the file — iOS will show "Profile Downloaded"
3. Go to **Settings > General > VPN & Device Management** (or "Profiles" on older iOS)
4. Tap the "Caddy Local Authority" profile and tap **Install**
5. Go to **Settings > General > About > Certificate Trust Settings**
6. Enable **full trust** for "Caddy Local Authority - 2026 ECC Root"

### Android

1. Transfer `caddy-root-ca.crt` to your device
2. Go to **Settings > Security > Encryption & Credentials > Install a certificate > CA certificate**
3. Select the `caddy-root-ca.crt` file
4. Confirm the installation

Note: On Android 7+, user-installed CA certificates are only trusted by apps that explicitly opt in. Chrome will trust it, but some apps may not.

### Windows

```powershell
certutil -addstore -f "ROOT" caddy-root-ca.crt
```

Or double-click the `.crt` file > **Install Certificate** > **Local Machine** > **Place in "Trusted Root Certification Authorities"**.

### Linux

```bash
sudo cp caddy-root-ca.crt /usr/local/share/ca-certificates/caddy-root-ca.crt
sudo update-ca-certificates
```

## Push notifications

Push provider configuration is **tenant-self-service** as of bug #220
follow-up. Tenants set their own ntfy / Expo credentials via authenticated
HTTP — operators no longer need to edit YAML and restart for every namespace
that wants push.

### Tenant flow (no operator involvement)

```bash
# Set per-namespace config
curl -X PUT https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>' \
  -H 'Content-Type: application/json' \
  -d '{"ntfy_base_url": "https://ntfy.sh"}'

# Read current config (secrets redacted to booleans)
curl https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'

# Clear (push reverts to gateway YAML defaults, or 503 if no defaults)
curl -X DELETE https://ns-anchat-test.orama-devnet.network/v1/push/config \
  -H 'Authorization: Bearer <user-jwt>'
```

Per-namespace config takes effect on the NEXT push send (the cached
dispatcher is invalidated on PUT/DELETE). No restart needed.

### Operator flow (cluster-wide defaults — optional)

Operators can still seed defaults in the gateway YAML. Per-namespace config
OVERRIDES the defaults; namespaces with no row inherit them.

```yaml
# Cluster-wide push defaults (optional; tenants override per-namespace)
push:
  ntfy_base_url: "https://ntfy.sh"           # default for namespaces with no override
  expo_access_token: "..."                    # default Expo token
```

### Encryption

Sensitive credentials (`ntfy_auth_token`, `expo_access_token`) are
AES-256-GCM-encrypted at rest in the `namespace_push_config` table using
a key derived from the cluster secret. The GET endpoint returns boolean
`has_X` flags only — credentials are NEVER echoed back over HTTP.

### Disabling push entirely

If `cluster_secret` isn't configured on the gateway, the push subsystem
is disabled and `/v1/push/*` returns 503. To enable: set the cluster secret
and restart. (This is the only operator-side restart still required, and
it's a one-time action at gateway provisioning.)

## Project Structure

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full architecture overview.

Key directories:

```
cmd/
  cli/          — CLI entry point (orama command)
  node/         — Node entry point (orama-node)
  gateway/      — Standalone gateway entry point
pkg/
  cli/          — CLI command implementations
  gateway/      — HTTP gateway, routes, middleware
  deployments/  — Deployment types, service, storage
  environments/ — Production (systemd) and development (direct) modes
  rqlite/       — Distributed SQLite via RQLite
```
