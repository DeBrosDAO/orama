# DeBros Network - Distributed P2P Database System

DeBros Network is a decentralized peer-to-peer data platform built in Go. It combines distributed SQL (RQLite), pub/sub messaging, and resilient peer discovery so applications can share state without central infrastructure.

## Table of Contents

- [At a Glance](#at-a-glance)
- [Quick Start](#quick-start)
- [Production Deployment](#production-deployment)
- [Components & Ports](#components--ports)
- [Configuration Cheatsheet](#configuration-cheatsheet)
- [CLI Highlights](#cli-highlights)
- [HTTP Gateway](#http-gateway)
- [Troubleshooting](#troubleshooting)
- [Resources](#resources)

## At a Glance

- Distributed SQL backed by RQLite and Raft consensus
- Topic-based pub/sub with automatic cleanup
- Namespace isolation for multi-tenant apps
- Secure transport using libp2p plus Noise/TLS
- Lightweight Go client and CLI tooling

## Quick Start

1. Clone and build the project:

   ```bash
   git clone https://github.com/DeBrosOfficial/network.git
   cd network
   make build
   ```

2. Generate local configuration (bootstrap, node2, node3, gateway):

   ```bash
   ./bin/dbn config init
   ```

3. Launch the full development stack:

   ```bash
   make dev
   ```

   This starts three nodes and the HTTP gateway. **The command will not complete successfully until all services pass health checks** (IPFS peer connectivity, RQLite cluster formation, and LibP2P connectivity). If health checks fail, all services are stopped automatically. Stop with `Ctrl+C`.

4. Validate the network from another terminal:

   ```bash
   ./bin/dbn health
   ./bin/dbn peers
   ./bin/dbn pubsub publish notifications "Hello World"
   ./bin/dbn pubsub subscribe notifications 10s
   ```

## Production Deployment

DeBros Network can be deployed as production systemd services on Linux servers. The production installer handles all dependencies, configuration, and service management automatically.

### Prerequisites

- **OS**: Ubuntu 20.04+, Debian 11+, or compatible Linux distribution
- **Architecture**: `amd64` (x86_64) or `arm64` (aarch64)
- **Permissions**: Root access (use `sudo`)
- **Resources**: Minimum 2GB RAM, 10GB disk space, 2 CPU cores

### Installation

#### Quick Install

Install the CLI tool first:

```bash
curl -fsSL https://install.debros.network | sudo bash

./install-debros-network.sh --prerelease --nightly
```

Or download manually from [GitHub Releases](https://github.com/DeBrosOfficial/network/releases).

#### Bootstrap Node (First Node)

Install the first node in your cluster:

```bash
# Main branch (stable releases)
sudo dbn prod install --bootstrap

# Nightly branch (latest development)
sudo dbn prod install --bootstrap --branch nightly
```

The bootstrap node initializes the cluster and serves as the primary peer for other nodes to join.

#### Secondary Node (Join Existing Cluster)

Join an existing cluster by providing the bootstrap node's IP and peer multiaddr:

```bash
sudo dbn prod install \
  --vps-ip <your_public_ip> \
  --peers /ip4/<bootstrap_ip>/tcp/4001/p2p/<peer_id> \
  --branch nightly
```

**Required flags for secondary nodes:**

- `--vps-ip`: Your server's public IP address
- `--peers`: Comma-separated list of bootstrap peer multiaddrs

**Optional flags:**

- `--branch`: Git branch to use (`main` or `nightly`, default: `main`)
- `--domain`: Domain name for HTTPS (enables ACME/Let's Encrypt) - see [HTTPS Setup](#https-setup-with-domain) below
- `--bootstrap-join`: Raft join address for secondary bootstrap nodes
- `--force`: Reconfigure all settings (use with caution)

#### Secondary Bootstrap Node

Create a secondary bootstrap node that joins an existing Raft cluster:

```bash
sudo dbn prod install \
  --bootstrap \
  --vps-ip <your_public_ip> \
  --bootstrap-join <primary_bootstrap_ip>:7001 \
  --branch nightly
```

### Branch Selection

DeBros Network supports two branches:

- **`main`**: Stable releases (default). Recommended for production.
- **`nightly`**: Latest development builds. Use for testing new features.

**Branch preference is saved automatically** during installation. Future upgrades will use the same branch unless you override it with `--branch`.

**Examples:**

```bash
# Install with nightly branch
sudo dbn prod install --bootstrap --branch nightly

# Upgrade using saved branch preference
sudo dbn prod upgrade --restart

# Upgrade and switch to main branch
sudo dbn prod upgrade --restart --branch main
```

### Upgrade

Upgrade an existing installation to the latest version:

```bash
# Upgrade using saved branch preference
sudo dbn prod upgrade --restart

# Upgrade and switch branches
sudo dbn prod upgrade --restart --branch nightly

# Upgrade without restarting services
sudo dbn prod upgrade
```

The upgrade process:

1. ✅ Checks prerequisites
2. ✅ Updates binaries (fetches latest from selected branch)
3. ✅ Preserves existing configurations and data
4. ✅ Updates configurations to latest format
5. ✅ Updates systemd service files
6. ✅ Optionally restarts services (`--restart` flag)

**Note**: The upgrade automatically detects your node type (bootstrap vs. regular node) and preserves all secrets, data, and configurations.

**Note**: Currently, the `upgrade` command does not support adding a domain via `--domain` flag. To enable HTTPS after installation, see [Adding Domain After Installation](#adding-domain-after-installation) below.

### HTTPS Setup with Domain

DeBros Gateway supports automatic HTTPS with Let's Encrypt certificates via ACME. This enables secure connections on ports 80 (HTTP redirect) and 443 (HTTPS).

#### Prerequisites

- Domain name pointing to your server's public IP address
- Ports 80 and 443 open and accessible from the internet
- Gateway service running

#### Adding Domain During Installation

Specify your domain during installation:

```bash
# Bootstrap node with HTTPS
sudo dbn prod install --bootstrap --domain node-kv4la8.debros.network --branch nightly

# Secondary node with HTTPS
sudo dbn prod install \
  --vps-ip <your_public_ip> \
  --peers /ip4/<bootstrap_ip>/tcp/4001/p2p/<peer_id> \
  --domain example.com \
  --branch nightly
```

The gateway will automatically:

- Obtain Let's Encrypt certificates via ACME
- Serve HTTP on port 80 (redirects to HTTPS)
- Serve HTTPS on port 443
- Renew certificates automatically

#### Adding Domain After Installation

Currently, the `upgrade` command doesn't support `--domain` flag. To enable HTTPS on an existing installation:

1. **Edit the gateway configuration:**

```bash
sudo nano /home/debros/.debros/data/gateway.yaml
```

2. **Update the configuration:**

```yaml
listen_addr: ":6001"
client_namespace: "default"
rqlite_dsn: ""
bootstrap_peers: []
enable_https: true
domain_name: "your-domain.com"
tls_cache_dir: "/home/debros/.debros/tls-cache"
olric_servers:
  - "127.0.0.1:3320"
olric_timeout: "10s"
ipfs_cluster_api_url: "http://localhost:9094"
ipfs_api_url: "http://localhost:4501"
ipfs_timeout: "60s"
ipfs_replication_factor: 3
```

3. **Ensure ports 80 and 443 are available:**

```bash
# Check if ports are in use
sudo lsof -i :80
sudo lsof -i :443

# If needed, stop conflicting services
```

4. **Restart the gateway:**

```bash
sudo systemctl restart debros-gateway.service
```

5. **Verify HTTPS is working:**

```bash
# Check gateway logs
sudo journalctl -u debros-gateway.service -f

# Test HTTPS endpoint
curl https://your-domain.com/health
```

**Important Notes:**

- The gateway will automatically obtain Let's Encrypt certificates on first start
- Certificates are cached in `/home/debros/.debros/tls-cache`
- Certificate renewal happens automatically
- Ensure your domain's DNS A record points to the server's public IP before enabling HTTPS

### Service Management

All services run as systemd units under the `debros` user.

#### Check Status

```bash
# View status of all services
dbn prod status

# Or use systemctl directly
systemctl status debros-node-bootstrap
systemctl status debros-ipfs-bootstrap
systemctl status debros-gateway
```

#### View Logs

```bash
# View recent logs (last 50 lines)
dbn prod logs node

# Follow logs in real-time
dbn prod logs node --follow

# View specific service logs
dbn prod logs ipfs --follow
dbn prod logs ipfs-cluster --follow
dbn prod logs rqlite --follow
dbn prod logs olric --follow
dbn prod logs gateway --follow
```

**Available log service names:**

- `node` - DeBros Network Node (bootstrap or regular)
- `ipfs` - IPFS Daemon
- `ipfs-cluster` - IPFS Cluster Service
- `rqlite` - RQLite Database
- `olric` - Olric Cache Server
- `gateway` - DeBros Gateway

**Note:** The `logs` command uses journalctl and accepts the full systemd service name. Use the short names above for convenience.

#### Service Control Commands

Use `dbn prod` commands for convenient service management:

```bash
# Start all services
sudo dbn prod start

# Stop all services
sudo dbn prod stop

# Restart all services
sudo dbn prod restart
```

Or use `systemctl` directly for more control:

```bash
# Restart all services
sudo systemctl restart debros-*

# Restart specific service
sudo systemctl restart debros-node-bootstrap

# Stop services
sudo systemctl stop debros-*

# Start services
sudo systemctl start debros-*

# Enable services (start on boot)
sudo systemctl enable debros-*
```

### Complete Production Commands Reference

#### Installation & Upgrade

```bash
# Install bootstrap node
sudo dbn prod install --bootstrap [--domain DOMAIN] [--branch BRANCH]


sudo dbn prod install --nightly --domain node-gh38V1.debros.network --vps-ip 57.128.223.92 --ignore-resource-checks --bootstrap-join

# Install secondary node
sudo dbn prod install --vps-ip IP --peers ADDRS [--domain DOMAIN] [--branch BRANCH]

# Install secondary bootstrap
sudo dbn prod install --bootstrap --vps-ip IP --bootstrap-join ADDR [--domain DOMAIN] [--branch BRANCH]

# Upgrade installation
sudo dbn prod upgrade [--restart] [--branch BRANCH]
```

#### Service Management

```bash
# Check service status (no sudo required)
dbn prod status

# Start all services
sudo dbn prod start

# Stop all services
sudo dbn prod stop

# Restart all services
sudo dbn prod restart
```

#### Logs

```bash
# View recent logs
dbn prod logs <service>

# Follow logs in real-time
dbn prod logs <service> --follow

# Available services: node, ipfs, ipfs-cluster, rqlite, olric, gateway
```

#### Uninstall

```bash
# Remove all services (preserves data and configs)
sudo dbn prod uninstall
```

### Directory Structure

Production installations use `/home/debros/.debros/`:

```
/home/debros/.debros/
├── configs/              # Configuration files
│   ├── bootstrap.yaml    # Bootstrap node config
│   ├── node.yaml         # Regular node config
│   ├── gateway.yaml      # Gateway config
│   └── olric/            # Olric cache config
├── data/                 # Runtime data
│   ├── bootstrap/        # Bootstrap node data
│   │   ├── ipfs/         # IPFS repository
│   │   ├── ipfs-cluster/ # IPFS Cluster data
│   │   └── rqlite/       # RQLite database
│   └── node/             # Regular node data
├── secrets/              # Secrets and keys
│   ├── cluster-secret    # IPFS Cluster secret
│   └── swarm.key         # IPFS swarm key
├── logs/                 # Service logs
│   ├── node-bootstrap.log
│   ├── ipfs-bootstrap.log
│   └── gateway.log
└── .branch               # Saved branch preference
```

### Uninstall

Remove all production services (preserves data and configs):

```bash
sudo dbn prod uninstall
```

This stops and removes all systemd services but keeps `/home/debros/.debros/` intact. You'll be prompted to confirm before uninstalling.

**To completely remove everything:**

```bash
sudo dbn prod uninstall
sudo rm -rf /home/debros/.debros
```

### Production Troubleshooting

#### Services Not Starting

```bash
# Check service status
systemctl status debros-node-bootstrap

# View detailed logs
journalctl -u debros-node-bootstrap -n 100

# Check log files
tail -f /home/debros/.debros/logs/node-bootstrap.log
```

#### Configuration Issues

```bash
# Verify configs exist
ls -la /home/debros/.debros/configs/

# Regenerate configs (preserves secrets)
sudo dbn prod upgrade --restart
```

#### IPFS AutoConf Errors

If you see "AutoConf.Enabled=false but 'auto' placeholder is used" errors, the upgrade process should fix this automatically. If not:

```bash
# Re-run upgrade to fix IPFS config
sudo dbn prod upgrade --restart
```

#### Port Conflicts

```bash
# Check what's using ports
sudo lsof -i :4001  # P2P port
sudo lsof -i :5001  # RQLite HTTP
sudo lsof -i :6001  # Gateway
```

#### Reset Installation

To start fresh (⚠️ **destroys all data**):

```bash
sudo dbn prod uninstall
sudo rm -rf /home/debros/.debros
sudo dbn prod install --bootstrap --branch nightly
```

## Components & Ports

- **Bootstrap node**: P2P `4001`, RQLite HTTP `5001`, Raft `7001`
- **Additional nodes** (`node2`, `node3`): Incrementing ports (`400{2,3}`, `500{2,3}`, `700{2,3}`)
- **Gateway**: HTTP `6001` exposes REST/WebSocket APIs
- **Data directory**: `~/.debros/` stores configs, identities, and RQLite data

Use `make dev` for the complete stack or run binaries individually with `go run ./cmd/node --config <file>` and `go run ./cmd/gateway --config gateway.yaml`.

## Configuration Cheatsheet

All runtime configuration lives in `~/.debros/`.

- `bootstrap.yaml`: `type: bootstrap`, optionally set `database.rqlite_join_address` to join another bootstrap's cluster
- `node*.yaml`: `type: node`, set `database.rqlite_join_address` (e.g. `localhost:7001`) and include the bootstrap `discovery.bootstrap_peers`
- `gateway.yaml`: configure `gateway.bootstrap_peers`, `gateway.namespace`, and optional auth flags

Validation reminders:

- HTTP and Raft ports must differ
- Non-bootstrap nodes require a join address and bootstrap peers
- Bootstrap nodes can optionally define a join address to synchronize with another bootstrap
- Multiaddrs must end with `/p2p/<peerID>`

Regenerate configs any time with `./bin/dbn config init --force`.

## CLI Highlights

All commands accept `--format json`, `--timeout <duration>`, and `--bootstrap <multiaddr>`.

- **Auth**

  ```bash
  ./bin/dbn auth login
  ./bin/dbn auth status
  ./bin/dbn auth logout
  ```

- **Network**

  ```bash
  ./bin/dbn health
  ./bin/dbn status
  ./bin/dbn peers
  ```

- **Database**

  ```bash
  ./bin/dbn query "SELECT * FROM users"
  ./bin/dbn query "CREATE TABLE users (id INTEGER PRIMARY KEY)"
  ./bin/dbn transaction --file ops.json
  ```

- **Pub/Sub**

  ```bash
  ./bin/dbn pubsub publish <topic> <message>
  ./bin/dbn pubsub subscribe <topic> 30s
  ./bin/dbn pubsub topics
  ```

Credentials live at `~/.debros/credentials.json` with user-only permissions.

## HTTP Gateway

Start locally with `make run-gateway` or `go run ./cmd/gateway --config gateway.yaml`.

Environment overrides:

```bash
export GATEWAY_ADDR="0.0.0.0:6001"
export GATEWAY_NAMESPACE="my-app"
export GATEWAY_BOOTSTRAP_PEERS="/ip4/localhost/tcp/4001/p2p/<peerID>"
export GATEWAY_REQUIRE_AUTH=true
export GATEWAY_API_KEYS="key1:namespace1,key2:namespace2"
```

Common endpoints (see `openapi/gateway.yaml` for the full spec):

- `GET /health`, `GET /v1/status`, `GET /v1/version`
- `POST /v1/auth/challenge`, `POST /v1/auth/verify`, `POST /v1/auth/refresh`
- `POST /v1/rqlite/exec`, `POST /v1/rqlite/find`, `POST /v1/rqlite/select`, `POST /v1/rqlite/transaction`
- `GET /v1/rqlite/schema`
- `POST /v1/pubsub/publish`, `GET /v1/pubsub/topics`, `GET /v1/pubsub/ws?topic=<topic>`
- `POST /v1/storage/upload`, `POST /v1/storage/pin`, `GET /v1/storage/status/:cid`, `GET /v1/storage/get/:cid`, `DELETE /v1/storage/unpin/:cid`

## RQLite Operations & Monitoring

RQLite is the distributed SQL database backing DeBros Network. Proper monitoring and maintenance are critical for cluster health.

### Connecting to RQLite

```bash
# Local development (bootstrap) - port 5001
rqlite -H localhost -p 5001

# Local development (bootstrap2) - port 5011
rqlite -H localhost -p 5011

# Production nodes
rqlite -H 192.168.1.151 -p 5001
```

### Health Checks (CRITICAL for Cluster Health)

```bash
# Check node status and diagnostics
rqlite -H localhost -p 5001 ".status"

# List all nodes in cluster (verify all nodes connected)
rqlite -H localhost -p 5001 ".nodes"

# Check if node is ready for operations
rqlite -H localhost -p 5001 ".ready"

# Get Go runtime info (goroutines, memory, performance)
rqlite -H localhost -p 5001 ".expvar"

# Show all tables
rqlite -H localhost -p 5001 ".tables"

# Show schema (CREATE statements)
rqlite -H localhost -p 5001 ".schema"

# Show all indexes
rqlite -H localhost -p 5001 ".indexes"
```

### Backup & Restore

```bash
# Backup database
rqlite -H localhost -p 5001 ".backup ~/rqlite-backup.db"

# Restore from backup
rqlite -H localhost -p 5001 ".restore ~/rqlite-backup.db"

# Dump database in SQL text format
rqlite -H localhost -p 5001 ".dump ~/rqlite-dump.sql"
```

### Consistency Levels (Important for Data Integrity)

RQLite supports three consistency levels for read operations:

```bash
# View current consistency level
rqlite -H localhost -p 5001 ".consistency"

# Set to weak (default, good balance for most applications)
rqlite -H localhost -p 5001 ".consistency weak"

# Set to strong (guaranteed consistency across entire cluster)
rqlite -H localhost -p 5001 ".consistency strong"

# Set to none (fastest reads, no consistency guarantees)
rqlite -H localhost -p 5001 ".consistency none"
```

**Recommendation**: Use `weak` for general operations, `strong` when data integrity is critical, and `none` only for cache-like data.

### Cluster Management

```bash
# Show detailed cluster diagnostics
rqlite -H localhost -p 5001 ".sysdump /tmp/rqlite-diagnostic.txt"

# Remove a node from cluster (use raft ID from .nodes output)
rqlite -H localhost -p 5001 ".remove <raft_id>"
```

### RQLite Log Files (Development)

All RQLite logs are now written to individual files for easier debugging:

```
~/.debros/logs/rqlite-bootstrap.log
~/.debros/logs/rqlite-bootstrap2.log
~/.debros/logs/rqlite-node2.log
~/.debros/logs/rqlite-node3.log
~/.debros/logs/rqlite-node4.log
```

View logs:

```bash
tail -f ~/.debros/logs/rqlite-bootstrap.log
tail -f ~/.debros/logs/rqlite-node2.log
dbn dev logs rqlite-bootstrap --follow
```

## Development Environment Operations

### Starting & Managing Development Environment

```bash
# Start the complete development stack (2 bootstraps + 3 nodes + gateway)
make dev

# Check status of running services
dbn dev status

# Stop all services
dbn dev down
```

### Development Logs

```bash
# View logs for specific component
dbn dev logs bootstrap
dbn dev logs bootstrap2
dbn dev logs node2
dbn dev logs node3
dbn dev logs node4
dbn dev logs gateway
dbn dev logs olric
dbn dev logs anon

# Follow logs in real-time (like tail -f)
dbn dev logs bootstrap --follow
dbn dev logs rqlite-bootstrap --follow
```

### Key Development Endpoints

```
Gateway:              http://localhost:6001
Bootstrap IPFS:       http://localhost:4501
Bootstrap2 IPFS:      http://localhost:4511
Node2 IPFS:           http://localhost:4502
Node3 IPFS:           http://localhost:4503
Node4 IPFS:           http://localhost:4504
Anon SOCKS:           127.0.0.1:9050
Olric Cache:          http://localhost:3320
RQLite Bootstrap:     http://localhost:5001
RQLite Bootstrap2:    http://localhost:5011
RQLite Node2:         http://localhost:5002
RQLite Node3:         http://localhost:5003
RQLite Node4:         http://localhost:5004
```

## IPFS Configuration

### Ensure Consistent Cluster Setup

All nodes in a cluster must have identical `cluster.secret` and `swarm.key`:

```bash
# Copy swarm key to each host (adjust path for bootstrap vs node):

# Bootstrap node
sudo cp /home/debros/.debros/secrets/swarm.key /home/debros/.debros/data/bootstrap/ipfs/repo/swarm.key

# Regular nodes
sudo cp /home/debros/.debros/secrets/swarm.key /home/debros/.debros/data/node/ipfs/repo/swarm.key

# Fix permissions
sudo chown debros:debros /home/debros/.debros/data/*/ipfs/repo/swarm.key
sudo chmod 600 /home/debros/.debros/data/*/ipfs/repo/swarm.key
```

### Important IPFS Configuration Notes

- **Production**: Update Olric config - change `0.0.0.0` to actual IP address for both entries
- **All Nodes**: Must have identical `cluster.secret` and `swarm.key` for cluster to form

## Troubleshooting

### General Issues

- **Config directory errors**: Ensure `~/.debros/` exists, is writable, and has free disk space (`touch ~/.debros/test && rm ~/.debros/test`).
- **Port conflicts**: Inspect with `lsof -i :4001` (or other ports) and stop conflicting processes or regenerate configs with new ports.
- **Missing configs**: Run `./bin/dbn config init` before starting nodes.
- **Cluster join issues**: Confirm the bootstrap node is running, `peer.info` multiaddr matches `bootstrap_peers`, and firewall rules allow the P2P ports.

### RQLite Troubleshooting

#### Cluster Not Forming

```bash
# Verify all nodes see each other
rqlite -H localhost -p 5001 ".nodes"

# Check node readiness
rqlite -H localhost -p 5001 ".ready"

# Check status and Raft logs
rqlite -H localhost -p 5001 ".status"
```

#### Broken RQLite Raft (Production)

```bash
# Fix RQLite Raft consensus
sudo env HOME=/home/debros network-cli rqlite fix
```

#### Reset RQLite State (DESTRUCTIVE - Last Resort Only)

```bash
# ⚠️ WARNING: This destroys all RQLite data!
rm -f ~/.debros/data/rqlite/raft.db
rm -f ~/.debros/data/rqlite/raft/peers.json
```

#### Kill IPFS Cluster Service

```bash
pkill -f ipfs-cluster-service
```

### Services Not Starting

```bash
# Check service status
systemctl status debros-node-bootstrap

# View detailed logs
journalctl -u debros-node-bootstrap -n 100

# Check log files
tail -f /home/debros/.debros/logs/node-bootstrap.log
```

### Port Conflicts

```bash
# Check what's using specific ports
sudo lsof -i :4001  # P2P port
sudo lsof -i :5001  # RQLite HTTP
sudo lsof -i :6001  # Gateway
sudo lsof -i :9094  # IPFS Cluster API

# Kill all DeBros-related processes (except Anyone on 9050)
lsof -ti:7001,7002,7003,5001,5002,5003,6001,4001,3320,3322,9094 | xargs kill -9 2>/dev/null && echo "Killed processes" || echo "No processes found"
```

### Systemd Service Management

```bash
# Stop all services (keeps Anyone proxy running on 9050)
sudo systemctl stop debros-*

# Disable services from auto-start
sudo systemctl disable debros-*

# Restart all services
sudo systemctl restart debros-*

# Enable services for auto-start on boot
sudo systemctl enable debros-*

# View all DeBros services
systemctl list-units 'debros-*'

# Clean up failed services
sudo systemctl reset-failed
```

### Reset Installation (⚠️ Destroys All Data)

```bash
# Start fresh (production)
sudo dbn prod uninstall
sudo rm -rf /home/debros/.debros
sudo dbn prod install --bootstrap --branch nightly
```

## Operations Cheat Sheet

### User Management (Linux)

```bash
# Switch to DeBros user
sudo -u debros bash

# Kill all DeBros user processes
sudo killall -9 -u debros

# Remove DeBros user completely
sudo userdel -r -f debros
```

### Installation & Deployment

```bash
# Local development
make dev

# Install nightly branch
wget https://raw.githubusercontent.com/DeBrosOfficial/network/refs/heads/nightly/scripts/install-debros-network.sh
chmod +x ./install-debros-network.sh
./install-debros-network.sh --prerelease --nightly

# Production bootstrap node
sudo dbn prod install --bootstrap --branch nightly

# Production secondary node
sudo dbn prod install \
  --vps-ip <your_ip> \
  --peers /ip4/<bootstrap_ip>/tcp/4001/p2p/<peer_id> \
  --branch nightly
```

### Configuration & Sudoers (Deploy User)

```bash
# Add to sudoers for deploy automation
ubuntu ALL=(ALL) NOPASSWD: /bin/bash
ubuntu ALL=(ALL) NOPASSWD: /usr/bin/make

# Git configuration
git config --global --add safe.directory /home/debros/src
```

### Authentication

```bash
# Login to gateway
env DEBROS_GATEWAY_URL=https://node-kv4la8.debros.network dbn auth login
```

## Resources

- [RQLite CLI Documentation](https://rqlite.io/docs/cli/)
- [RQLite Features](https://rqlite.io/docs/features/)
- [RQLite Clustering Guide](https://rqlite.io/docs/clustering/)
- [RQLite Security](https://rqlite.io/docs/security/)
- [RQLite Backup & Restore](https://rqlite.io/docs/backup-and-restore/)
- Go modules: `go mod tidy`, `go test ./...`
- Automation: `make build`, `make dev`, `make run-gateway`, `make lint`
- API reference: `openapi/gateway.yaml`
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
