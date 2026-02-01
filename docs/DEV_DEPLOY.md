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

### Deploying to All 3 Nodes

To deploy to all nodes, repeat steps 3-5 (dev) or 3-4 (production) for each VPS IP.

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
| `--restart` | Restart all services after upgrade |

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
