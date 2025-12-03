# Orama Network - Distributed P2P Database System

A decentralized peer-to-peer data platform built in Go. Combines distributed SQL (RQLite), pub/sub messaging, and resilient peer discovery so applications can share state without central infrastructure.

## Features

- **Distributed SQL** - RQLite with Raft consensus
- **Pub/Sub Messaging** - Topic-based with automatic cleanup
- **Namespace Isolation** - Multi-tenant support
- **Secure Transport** - LibP2P + Noise/TLS encryption
- **Unified Gateway** - Single port access to all node services

## Quick Start

### Local Development

```bash
# Build the project
make build

# Start 5-node development cluster
make dev
```

The cluster automatically performs health checks before declaring success. Check the output for:

- Node unified gateway ports (6001-6005)
- IPFS API endpoints
- Olric cache server
- Peer connection status
- Example curl commands

### Stop Development Environment

```bash
make down
```

## Testing Services

After running `make dev`, test service health using these curl requests:

> **Note:** Local domains (node-1.local, etc.) require running `sudo make setup-domains` first. Alternatively, use `localhost` with port numbers.

### Node Unified Gateways

Each node is accessible via a single unified gateway port:

```bash
# Node-1 (port 6001)
curl http://node-1.local:6001/health
curl http://node-1.local:6001/rqlite/http/db/execute -H "Content-Type: application/json" -d '{"sql":"SELECT 1"}'
curl http://node-1.local:6001/cluster/health
curl http://node-1.local:6001/ipfs/api/v0/version

# Node-2 (port 6002)
curl http://node-2.local:6002/health
curl http://node-2.local:6002/rqlite/http/db/execute -H "Content-Type: application/json" -d '{"sql":"SELECT 1"}'

# Node-3 (port 6003)
curl http://node-3.local:6003/health

# Node-4 (port 6004)
curl http://node-4.local:6004/health

# Node-5 (port 6005)
curl http://node-5.local:6005/health
```

### Main Gateway

The main gateway provides `/v1/*` routes for RQLite, pub/sub, and storage:

```bash
# Gateway health
curl http://node-1.local:6001/health

# Gateway status
curl http://node-1.local:6001/v1/status

# Network peers
curl http://node-1.local:6001/v1/network/status

# Database query
curl http://node-1.local:6001/v1/rqlite/query \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT 1"}'

# Pub/Sub topics
curl http://node-1.local:6001/v1/pubsub/topics
```

### Direct Service Access (Debugging)

Direct access to individual service ports without unified gateway:

```bash
# RQLite HTTP (each node on its own port)
curl http://localhost:5001/db/execute -H "Content-Type: application/json" -d '{"sql":"SELECT 1"}'  # Bootstrap
curl http://localhost:5002/db/execute -H "Content-Type: application/json" -d '{"sql":"SELECT 1"}'  # Node2

# IPFS API
curl http://localhost:4501/api/v0/version  # Bootstrap IPFS
curl http://localhost:4502/api/v0/version  # Node2 IPFS

# Olric Cache
curl http://localhost:3320/stats
```

## Network Architecture

### Unified Gateway Ports

```
Node-1:     localhost:6001  → /rqlite/http, /rqlite/raft, /cluster, /ipfs/api
Node-2:     localhost:6002  → Same routes
Node-3:     localhost:6003  → Same routes
Node-4:     localhost:6004  → Same routes
Node-5:     localhost:6005  → Same routes
```

### Direct Service Ports (for debugging)

```
RQLite HTTP:     5001, 5002, 5003, 5004, 5005 (one per node)
RQLite Raft:     7001, 7002, 7003, 7004, 7005
IPFS API:        4501, 4502, 4503, 4504, 4505
IPFS Swarm:      4101, 4102, 4103, 4104, 4105
Cluster API:     9094, 9104, 9114, 9124, 9134
Internal Gateway: 6000
Olric Cache:     3320
Anon SOCKS:      9050
```

## Development Commands

```bash
# Start full cluster (5 nodes + gateway)
make dev

# Check service status
orama dev status

# View logs
orama dev logs node-1           # Node-1 logs
orama dev logs node-1 --follow  # Follow logs in real-time
orama dev logs gateway --follow # Gateway logs

# Stop all services
orama dev down

# Build binaries
make build
```

## CLI Commands

### Network Status

```bash
./bin/orama health          # Cluster health check
./bin/orama peers           # List connected peers
./bin/orama status          # Network status
```

### Database Operations

```bash
./bin/orama query "SELECT * FROM users"
./bin/orama query "CREATE TABLE users (id INTEGER PRIMARY KEY)"
./bin/orama transaction --file ops.json
```

### Pub/Sub

```bash
./bin/orama pubsub publish <topic> <message>
./bin/orama pubsub subscribe <topic> 30s
./bin/orama pubsub topics
```

### Authentication

```bash
./bin/orama auth login
./bin/orama auth status
./bin/orama auth logout
```

## Production Deployment

### Prerequisites

- Ubuntu 22.04+ or Debian 12+
- `amd64` or `arm64` architecture
- 4GB RAM, 50GB SSD, 2 CPU cores

### Required Ports

**External (must be open in firewall):**

- **80** - HTTP (ACME/Let's Encrypt certificate challenges)
- **443** - HTTPS (Main gateway API endpoint)
- **4101** - IPFS Swarm (peer connections)
- **7001** - RQLite Raft (cluster consensus)

**Internal (bound to localhost, no firewall needed):**

- 4501 - IPFS API
- 5001 - RQLite HTTP API
- 6001 - Unified Gateway
- 8080 - IPFS Gateway
- 9050 - Anyone Client SOCKS5 proxy
- 9094 - IPFS Cluster API
- 3320/3322 - Olric Cache

### Installation

```bash
# Install via APT
echo "deb https://debrosficial.github.io/network/apt stable main" | sudo tee /etc/apt/sources.list.d/debros.list

sudo apt update && sudo apt install orama

# Interactive installation (recommended)
sudo orama install

# Or with flags - First node (creates new cluster)
sudo orama install --vps-ip <public_ip> --domain node-1.example.com

# Joining existing cluster
sudo orama install --vps-ip <public_ip> --domain node-2.example.com \
  --peers /ip4/<first_node_ip>/tcp/4001/p2p/<peer_id> \
  --cluster-secret <64-hex-secret>
```

### Service Management

```bash
# Status
orama status

# Control services
sudo orama start
sudo orama stop
sudo orama restart

# View logs
orama logs node --follow
orama logs gateway --follow
orama logs ipfs --follow
```

### Upgrade

```bash
# Upgrade to latest version
sudo orama upgrade --restart [--branch nightly]
```

## Configuration

All configuration lives in `~/.orama/`:

- `configs/node.yaml` - Node configuration
- `configs/gateway.yaml` - Gateway configuration
- `configs/olric.yaml` - Cache configuration
- `secrets/` - Keys and certificates
- `data/` - Service data directories

## Troubleshooting

### Services Not Starting

```bash
# Check status
systemctl status debros-node

# View logs
journalctl -u debros-node -f

# Check log files
tail -f /home/debros/.orama/logs/node.log
```

### Port Conflicts

```bash
# Check what's using specific ports
sudo lsof -i :443   # HTTPS Gateway
sudo lsof -i :7001  # TCP/SNI Gateway
sudo lsof -i :6001  # Internal Gateway
```

### RQLite Cluster Issues

```bash
# Connect to RQLite CLI
rqlite -H localhost -p 5001

# Check cluster status
.nodes
.status
.ready

# Check consistency level
.consistency
```

### Reset Installation

```bash
# Production reset (⚠️ DESTROYS DATA)
sudo orama uninstall
sudo rm -rf /home/debros/.orama
sudo orama install
```

## HTTP Gateway API

### Main Gateway Endpoints

- `GET /health` - Health status
- `GET /v1/status` - Full status
- `GET /v1/version` - Version info
- `POST /v1/rqlite/exec` - Execute SQL
- `POST /v1/rqlite/query` - Query database
- `GET /v1/rqlite/schema` - Get schema
- `POST /v1/pubsub/publish` - Publish message
- `GET /v1/pubsub/topics` - List topics
- `GET /v1/pubsub/ws?topic=<name>` - WebSocket subscribe

See `openapi/gateway.yaml` for complete API specification.

## Resources

- [RQLite Documentation](https://rqlite.io/docs/)
- [LibP2P Documentation](https://docs.libp2p.io/)
- [GitHub Repository](https://github.com/DeBrosOfficial/network)
- [Issue Tracker](https://github.com/DeBrosOfficial/network/issues)
