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
- `--domain`: Domain name for HTTPS (enables ACME/Let's Encrypt)
- `--bootstrap-join`: Raft join address for secondary bootstrap nodes

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
# View recent logs
dbn prod logs node

# Follow logs in real-time
dbn prod logs node --follow

# View specific service logs
dbn prod logs ipfs --follow
dbn prod logs gateway --follow
```

Available log targets: `node`, `ipfs`, `ipfs-cluster`, `rqlite`, `olric`, `gateway`

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

This stops and removes all systemd services but keeps `/home/debros/.debros/` intact. To completely remove:

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

## Troubleshooting

- **Config directory errors**: Ensure `~/.debros/` exists, is writable, and has free disk space (`touch ~/.debros/test && rm ~/.debros/test`).
- **Port conflicts**: Inspect with `lsof -i :4001` (or other ports) and stop conflicting processes or regenerate configs with new ports.
- **Missing configs**: Run `./bin/dbn config init` before starting nodes.
- **Cluster join issues**: Confirm the bootstrap node is running, `peer.info` multiaddr matches `bootstrap_peers`, and firewall rules allow the P2P ports.

## Resources

- Go modules: `go mod tidy`, `go test ./...`
- Automation: `make build`, `make dev`, `make run-gateway`, `make lint`
- API reference: `openapi/gateway.yaml`
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
