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
#   bin/rqlite-mcp   — RQLite MCP server
```

## Running Tests

```bash
make test
```

## Running Locally (macOS)

The node runs in "direct mode" on macOS — processes are managed directly instead of via systemd.

```bash
# Start a single node
make run-node

# Start multiple nodes for cluster testing
make run-node2
make run-node3
```

## Deploying to VPS

There are two deployment workflows: **development** (fast iteration, no git required) and **production** (via git).

### Development Deployment (Fast Iteration)

Use this when iterating quickly — no need to commit or push to git.

```bash
# 1. Build the CLI for Linux
GOOS=linux GOARCH=amd64 go build -o orama-cli-linux ./cmd/cli

# 2. Generate a source archive (excludes .git, node_modules, bin/, etc.)
./scripts/generate-source-archive.sh
# Creates: /tmp/network-source.tar.gz

# 3. Copy CLI and source to the VPS
sshpass -p '<password>' scp -o StrictHostKeyChecking=no orama-cli-linux ubuntu@<ip>:/tmp/orama
sshpass -p '<password>' scp -o StrictHostKeyChecking=no /tmp/network-source.tar.gz ubuntu@<ip>:/tmp/

# 4. On the VPS: extract source and install the CLI
ssh ubuntu@<ip>
sudo rm -rf /home/debros/src && sudo mkdir -p /home/debros/src
sudo tar xzf /tmp/network-source.tar.gz -C /home/debros/src
sudo chown -R debros:debros /home/debros/src
sudo mv /tmp/orama /usr/local/bin/orama && sudo chmod +x /usr/local/bin/orama

# 5. Upgrade using local source (skips git pull)
sudo orama upgrade --no-pull --restart
```

### Development Deployment with Pre-Built Binaries (Fastest)

Cross-compile everything locally and skip all Go compilation on the VPS. This is significantly faster because your local machine compiles much faster than the VPS.

```bash
# 1. Cross-compile all binaries for Linux (DeBros + Olric + CoreDNS + Caddy)
make build-linux-all
# Outputs everything to bin-linux/

# 2. Generate a single deploy archive (source + pre-built binaries)
./scripts/generate-source-archive.sh
# Creates: /tmp/network-source.tar.gz (includes bin-linux/ if present)

# 3. Copy the single archive to the VPS
sshpass -p '<password>' scp -o StrictHostKeyChecking=no /tmp/network-source.tar.gz ubuntu@<ip>:/tmp/

# 4. Extract and install everything on the VPS
sshpass -p '<password>' ssh -o StrictHostKeyChecking=no ubuntu@<ip> \
    'sudo bash -s' < scripts/extract-deploy.sh

# 5. Install/upgrade with --pre-built (skips ALL Go compilation on VPS)
sudo orama install --no-pull --pre-built --vps-ip <ip> ...
# or
sudo orama upgrade --no-pull --pre-built --restart
```

**What `--pre-built` skips:** Go installation, `make build`, Olric `go install`, CoreDNS build, Caddy/xcaddy build.

**What `--pre-built` still runs:** apt dependencies, RQLite/IPFS/IPFS Cluster downloads (pre-built binary downloads, fast), Anyone relay setup, config generation, systemd service creation.

### Production Deployment (Via Git)

For production releases — pulls source from GitHub on the VPS.

```bash
# 1. Commit and push your changes
git push origin <branch>

# 2. Build the CLI for Linux
GOOS=linux GOARCH=amd64 go build -o orama-cli-linux ./cmd/cli

# 3. Deploy the CLI to the VPS
sshpass -p '<password>' scp orama-cli-linux ubuntu@<ip>:/tmp/orama
ssh ubuntu@<ip> "sudo mv /tmp/orama /usr/local/bin/orama && sudo chmod +x /usr/local/bin/orama"

# 4. Run upgrade (downloads source from GitHub)
ssh ubuntu@<ip> "sudo orama upgrade --branch <branch> --restart"
```

### Upgrading a Multi-Node Cluster (CRITICAL)

**NEVER restart all nodes simultaneously.** RQLite uses Raft consensus and requires a majority (quorum) to function. Restarting all nodes at once can cause cluster splits where nodes elect different leaders or form isolated clusters.

#### Safe Upgrade Procedure (Rolling Restart)

Always upgrade nodes **one at a time**, waiting for each to rejoin before proceeding:

```bash
# 1. Build locally
make build-linux-all
./scripts/generate-source-archive.sh
# Creates: /tmp/network-source.tar.gz (includes bin-linux/)

# 2. Upload to ONE node first (the "hub" node)
sshpass -p '<password>' scp /tmp/network-source.tar.gz ubuntu@<hub-ip>:/tmp/

# 3. Fan out from hub to all other nodes (server-to-server is faster)
ssh ubuntu@<hub-ip>
for ip in <ip2> <ip3> <ip4> <ip5> <ip6>; do
  scp /tmp/network-source.tar.gz ubuntu@$ip:/tmp/
done
exit

# 4. Extract on ALL nodes (can be done in parallel, no restart yet)
for ip in <ip1> <ip2> <ip3> <ip4> <ip5> <ip6>; do
  ssh ubuntu@$ip 'sudo bash -s' < scripts/extract-deploy.sh
done

# 5. Find the RQLite leader (upgrade this one LAST)
ssh ubuntu@<any-node> 'curl -s http://localhost:5001/status | jq -r .store.raft.state'

# 6. Upgrade FOLLOWER nodes one at a time
# First stop services, then upgrade, which restarts them
ssh ubuntu@<follower-ip> 'sudo orama prod stop && sudo orama upgrade --no-pull --pre-built --restart'

# Wait for rejoin before proceeding to next node
ssh ubuntu@<leader-ip> 'curl -s http://localhost:5001/status | jq -r .store.raft.num_peers'
# Should show expected number of peers (N-1)

# Repeat for each follower...

# 7. Upgrade the LEADER node last
ssh ubuntu@<leader-ip> 'sudo orama prod stop && sudo orama upgrade --no-pull --pre-built --restart'
```

#### What NOT to Do

- **DON'T** stop all nodes, replace binaries, then start all nodes
- **DON'T** run `orama upgrade --restart` on multiple nodes in parallel
- **DON'T** clear RQLite data directories unless doing a full cluster rebuild
- **DON'T** use `systemctl stop debros-node` on multiple nodes simultaneously

#### Recovery from Cluster Split

If nodes get stuck in "Candidate" state or show "leader not found" errors:

1. Identify which node has the most recent data (usually the old leader)
2. Keep that node running as the new leader
3. On each other node, clear RQLite data and restart:
   ```bash
   sudo orama prod stop
   sudo rm -rf /home/debros/.orama/data/rqlite
   sudo systemctl start debros-node
   ```
4. The node should automatically rejoin using its configured `rqlite_join_address`

If automatic rejoin fails, the node may have started without the `-join` flag. Check:
```bash
ps aux | grep rqlited
# Should include: -join 10.0.0.1:7001 (or similar)
```

If `-join` is missing, the node bootstrapped standalone. You'll need to either:
- Restart debros-node (it should detect empty data and use join)
- Or do a full cluster rebuild from CLEAN_NODE.md

### Deploying to Multiple Nodes

To deploy to all nodes, repeat steps 3-5 (dev) or 3-4 (production) for each VPS IP.

**Important:** When using `--restart`, do nodes one at a time (see "Upgrading a Multi-Node Cluster" above).

### CLI Flags Reference

#### `orama install`

| Flag | Description |
|------|-------------|
| `--vps-ip <ip>` | VPS public IP address (required) |
| `--domain <domain>` | Domain for HTTPS certificates. Nameserver nodes use the base domain (e.g., `example.com`); non-nameserver nodes use a subdomain (e.g., `node-4.example.com`) |
| `--base-domain <domain>` | Base domain for deployment routing (e.g., example.com) |
| `--nameserver` | Configure this node as a nameserver (CoreDNS + Caddy) |
| `--join <url>` | Join existing cluster via HTTPS URL (e.g., `https://node1.example.com`) |
| `--token <token>` | Invite token for joining (from `orama invite` on existing node) |
| `--branch <branch>` | Git branch to use (default: main) |
| `--no-pull` | Skip git clone/pull, use existing `/home/debros/src` |
| `--pre-built` | Skip all Go compilation, use pre-built binaries already on disk (see above) |
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

#### `orama invite`

| Flag | Description |
|------|-------------|
| `--expiry <duration>` | Token expiry duration (default: 1h, e.g. `--expiry 24h`) |

**Important notes about invite tokens:**

- **Tokens are single-use.** Once a node consumes a token during the join handshake, it cannot be reused. Generate a separate token for each node you want to join.
- **Expiry is checked in UTC.** RQLite uses `datetime('now')` which is always UTC. If your local timezone differs, account for the offset when choosing expiry durations.
- **Use longer expiry for multi-node deployments.** When deploying multiple nodes, use `--expiry 24h` to avoid tokens expiring mid-deployment.

#### `orama upgrade`

| Flag | Description |
|------|-------------|
| `--branch <branch>` | Git branch to pull from |
| `--no-pull` | Skip git pull, use existing source |
| `--pre-built` | Skip all Go compilation, use pre-built binaries already on disk |
| `--restart` | Restart all services after upgrade |

#### `orama prod` (Service Management)

Use these commands to manage services on production nodes:

```bash
# Stop all services (debros-node, coredns, caddy)
sudo orama prod stop

# Start all services
sudo orama prod start

# Restart all services
sudo orama prod restart

# Check service status
sudo orama prod status
```

**Note:** Always use `orama prod stop` instead of manually running `systemctl stop`. The CLI ensures all related services (including CoreDNS and Caddy on nameserver nodes) are handled correctly.

### Node Join Flow

```bash
# 1. Genesis node (first node, creates cluster)
# Nameserver nodes use the base domain as --domain
sudo orama install --vps-ip 1.2.3.4 --domain example.com \
    --base-domain example.com --nameserver

# 2. On genesis node, generate an invite
orama invite
# Output: sudo orama install --join https://example.com --token <TOKEN> --vps-ip <IP>

# 3. On the new node, run the printed command
# Nameserver nodes use the base domain; non-nameserver nodes use subdomains (e.g., node-4.example.com)
sudo orama install --join https://example.com --token abc123... \
    --vps-ip 5.6.7.8 --domain example.com --base-domain example.com --nameserver
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
sudo orama install --join http://1.2.3.4 --vps-ip 5.6.7.8 --token abc123... --nameserver
```

This works because Caddy's `:80` block proxies all HTTP traffic to the gateway. However, once DNS
is properly configured, always use the HTTPS domain URL.

**Important:** Never use `http://<ip>:6001` — port 6001 is the internal gateway and is blocked by
UFW from external access. The join request goes through Caddy on port 80 (HTTP) or 443 (HTTPS),
which proxies to the gateway internally.

## Pre-Install Checklist

Before running `orama install` on a VPS, ensure:

1. **Stop Docker if running.** Docker commonly binds ports 4001 and 8080 which conflict with IPFS. The installer checks for port conflicts and shows which process is using each port, but it's easier to stop Docker first:
   ```bash
   sudo systemctl stop docker docker.socket
   sudo systemctl disable docker docker.socket
   ```

2. **Stop any existing IPFS instance.**
   ```bash
   sudo systemctl stop ipfs
   ```

3. **Ensure `make` is installed.** Required for building CoreDNS and Caddy from source:
   ```bash
   sudo apt-get install -y make
   ```

4. **Stop any service on port 53** (for nameserver nodes). The installer handles `systemd-resolved` automatically, but other DNS services (like `bind9` or `dnsmasq`) must be stopped manually.

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
