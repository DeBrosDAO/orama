# DeBros Network - Distributed P2P Database System

A robust, decentralized peer-to-peer network built in Go, providing distributed SQL database, key-value storage, pub/sub messaging, and resilient peer management. Designed for applications needing reliable, scalable, and secure data sharing without centralized infrastructure.

---

## Table of Contents

- [Features](#features)
- [Architecture Overview](#architecture-overview)
- [System Requirements](#system-requirements)
- [Quick Start](#quick-start)
- [Deployment & Installation](#deployment--installation)
- [Configuration](#configuration)
- [CLI Usage](#cli-usage)
- [HTTP Gateway](#http-gateway)
- [Development](#development)
- [Database Client (Go ORM-like)](#database-client-go-orm-like)
- [Troubleshooting](#troubleshooting)
- [License](#license)

---

## Features

- **Distributed SQL Database:** RQLite-backed, Raft-consensus, ACID transactions, automatic failover.
- **Pub/Sub Messaging:** Topic-based, real-time, namespaced, automatic cleanup.
- **Peer Discovery & Management:** Nodes discover peers, bootstrap support, health monitoring.
- **Application Isolation:** Namespace-based multi-tenancy, per-app config.
- **Secure by Default:** Noise/TLS transport, peer identity, systemd hardening.
- **Simple Client API:** Lightweight Go client for apps and CLI tools.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                  DeBros Network Cluster                    │
├─────────────────────────────────────────────────────────────┤
│                   Application Layer                        │
│ ┌─────────────┐ ┌─────────────┐ ┌────────────────────────┐ │
│ │   Anchat    │ │ Custom App  │ │      CLI Tools        │ │
│ └─────────────┘ └─────────────┘ └────────────────────────┘ │
├─────────────────────────────────────────────────────────────┤
│                      Client API                            │
│ ┌─────────────┐ ┌────────────────────────┐               │
│ │  Database   │ │        PubSub         │               │
│ │   Client    │ │        Client         │               │
│ └─────────────┘ └────────────────────────┘               │
├─────────────────────────────────────────────────────────────┤
│                    Network Node Layer                      │
│ ┌─────────────┐ ┌─────────────┐ ┌────────────────────────┐ │
│ │ Discovery   │ │   PubSub    │ │      Database         │ │
│ │  Manager    │ │   Manager   │ │    (RQLite)          │ │
│ └─────────────┘ └─────────────┘ └────────────────────────┘ │
├─────────────────────────────────────────────────────────────┤
│                  Transport Layer                           │
│ ┌─────────────┐ ┌─────────────┐ ┌────────────────────────┐ │
│ │   LibP2P    │ │   Noise/TLS │ │      RQLite           │ │
│ │   Host      │ │  Encryption │ │    Database           │ │
│ └─────────────┘ └─────────────┘ └────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

- **Node:** Full P2P participant, runs services, handles peer discovery, database, pubsub.
- **Client:** Lightweight, connects only to bootstrap peers, consumes services, no peer discovery.

---

## System Requirements

### Software

- **Go:** 1.21+ (recommended)
- **RQLite:** 8.x (distributed SQLite)
- **Git:** For source management
- **Make:** For build automation (recommended)

### Hardware

- **Minimum:** 2 CPU cores, 4GB RAM, 10GB disk, stable internet
- **Recommended:** 4+ cores, 8GB+ RAM, 50GB+ SSD, low-latency network

### Network Ports

- **4001:** LibP2P P2P communication
- **5001:** RQLite HTTP API
- **7001:** RQLite Raft consensus

### Filesystem Permissions

DeBros Network stores all configuration and data in `~/.debros/` directory. Ensure you have:

- **Read/Write access** to your home directory (`~`)
- **Available disk space**: At least 10GB for database and logs
- **No restrictive mount options**: The home directory must not be mounted read-only
- **Unix permissions**: Standard user permissions are sufficient (no root/sudo required)

#### Directory Structure

DeBros automatically creates the following directory structure:

```
~/.debros/
├── bootstrap.yaml          # Bootstrap node config
├── node.yaml               # Node config
├── gateway.yaml            # Gateway config
├── bootstrap/              # Bootstrap node data (auto-created)
│   ├── rqlite/             # RQLite database files
│   │   ├── db.sqlite       # Main database
│   │   ├── raft/           # Raft consensus data
│   │   └── rsnapshots/     # Raft snapshots
│   ├── peer.info           # Node multiaddr (created at startup)
│   └── identity.key        # Node private key (created at startup)
├── node/                   # Node data (auto-created)
│   ├── rqlite/             # RQLite database files
│   ├── raft/               # Raft data
│   ├── peer.info           # Node multiaddr (created at startup)
│   └── identity.key        # Node private key (created at startup)
└── node2/                  # Additional node configs (if running multiple)
    └── rqlite/             # RQLite database files
```

**Files Created at Startup:**
- `identity.key` - LibP2P private key for the node (generated once, reused)
- `peer.info` - The node's multiaddr (e.g., `/ip4/0.0.0.0/tcp/4001/p2p/12D3KooW...`)

**Automatic Creation**: The node automatically creates all necessary data directories when started. You only need to ensure:
1. `~/.debros/` is writable
2. Sufficient disk space available
3. Correct config files exist

**Permission Check:**

```bash
# Verify home directory is writable
touch ~/test-write && rm ~/test-write && echo "✓ Home directory is writable"

# Check available disk space
df -h ~
```

**If you get permission errors:**

```
Error: Failed to create/access config directory
Please ensure:
  1. Home directory is accessible
  2. You have write permissions to home directory
  3. Disk space is available
```

**Solution:**

- Ensure you're not running with overly restrictive umask: `umask` should show `0022` or similar
- Check home directory permissions: `ls -ld ~` should show your user as owner
- For sandboxed/containerized environments: Ensure `/home/<user>` is writable

---

## Quick Start

### 1. Clone and Setup

```bash
git clone https://github.com/DeBrosOfficial/network.git
cd network
```

### 2. Build All Executables

```bash
make build
```

### 3. Start a Bootstrap Node

```bash
make run-node
# Or manually:
go run ./cmd/node --config configs/node.yaml
```

### 4. Start Additional Nodes

```bash
make run-node2
# Or manually:
go run ./cmd/node --config configs/node.yaml
```

### 5. Test with CLI

```bash
./bin/network-cli health
./bin/network-cli peers
./bin/network-cli pubsub publish notifications "Hello World"
./bin/network-cli pubsub subscribe notifications 10s
```

---

## Deployment & Installation

### Automated Production Install

Run the install script for a secure, production-ready setup:

```bash
curl -sSL https://github.com/DeBrosOfficial/network/raw/main/scripts/install-debros-network.sh | sudo bash
```

**What the Script Does:**

- Detects OS, installs Go, RQLite, dependencies
- Creates `debros` system user, secure directory structure
- Generates LibP2P identity keys
- Clones source, builds binaries
- Sets up systemd service (`debros-node`)
- Configures firewall (UFW) for required ports
- Generates YAML config in `/opt/debros/configs/node.yaml`

**Directory Structure:**

```
/opt/debros/
├── bin/           # Binaries
├── configs/       # YAML configs
├── keys/          # Identity keys
├── data/          # RQLite DB, storage
├── logs/          # Node logs
├── src/           # Source code
```

**Service Management:**

```bash
sudo systemctl status debros-node
sudo systemctl start debros-node
sudo systemctl stop debros-node
sudo systemctl restart debros-node
sudo journalctl -u debros-node.service -f
```

---

## Configuration

### Configuration Files Location

All configuration files are stored in `~/.debros/` for both local development and production deployments:

- `~/.debros/node.yaml` - Node configuration
- `~/.debros/node.yaml` - Bootstrap node configuration
- `~/.debros/gateway.yaml` - Gateway configuration

The system will **only** load config from `~/.debros/` and will error if required config files are missing.

### Generating Configuration Files

Use the `network-cli config init` command to generate configuration files:

#### Generate a Node Config

```bash
# Generate basic node config with bootstrap peers
network-cli config init --type node --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/QmXxx,/ip4/127.0.0.1/tcp/4002/p2p/QmYyy"

# With custom ports
network-cli config init --type node --name node2.yaml --listen-port 4002 --rqlite-http-port 5002 --rqlite-raft-port 7002 --join localhost:5001 --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/QmXxx"

# Force overwrite existing config
network-cli config init --type node --force
```

#### Generate a Bootstrap Node Config

```bash
# Generate bootstrap node (no join address required)
network-cli config init --type bootstrap

# With custom ports
network-cli config init --type bootstrap --listen-port 4001 --rqlite-http-port 5001 --rqlite-raft-port 7001
```

#### Generate a Gateway Config

```bash
# Generate gateway config
network-cli config init --type gateway

# With bootstrap peers
network-cli config init --type gateway --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/QmXxx"
```

### Running Multiple Nodes on the Same Machine

You can run multiple nodes on a single machine by creating separate configuration files and using the `--config` flag:

#### Create Multiple Node Configs

```bash
# Node 1
./bin/network-cli config init --type node --name node1.yaml \
  --listen-port 4001 --rqlite-http-port 5001 --rqlite-raft-port 7001 \
  --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/<BOOTSTRAP_ID>"

# Node 2
./bin/network-cli config init --type node --name node2.yaml \
  --listen-port 4002 --rqlite-http-port 5002 --rqlite-raft-port 7002 \
  --join localhost:5001 \
  --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/<BOOTSTRAP_ID>"

# Node 3
./bin/network-cli config init --type node --name node3.yaml \
  --listen-port 4003 --rqlite-http-port 5003 --rqlite-raft-port 7003 \
  --join localhost:5001 \
  --bootstrap-peers "/ip4/127.0.0.1/tcp/4001/p2p/<BOOTSTRAP_ID>"
```

#### Run Multiple Nodes in Separate Terminals

```bash
# Terminal 1 - Bootstrap node
go run ./cmd/node --config bootstrap.yaml

# Terminal 2 - Node 1
go run ./cmd/node --config node1.yaml

# Terminal 3 - Node 2
go run ./cmd/node --config node2.yaml

# Terminal 4 - Node 3
go run ./cmd/node --config node3.yaml
```

#### Or Use Makefile Targets

```bash
# Terminal 1
make run-node      # Runs: go run ./cmd/node --config bootstrap.yaml

# Terminal 2
make run-node2     # Runs: go run ./cmd/node --config node.yaml

# Terminal 3
make run-node3     # Runs: go run ./cmd/node --config node2.yaml
```

#### Key Points for Multiple Nodes

- **Each node needs unique ports**: P2P port, RQLite HTTP port, and RQLite Raft port must all be different
- **Join address**: Non-bootstrap nodes need `rqlite_join_address` pointing to the bootstrap or an existing node
- **Bootstrap peers**: All nodes need the bootstrap node's multiaddr in `discovery.bootstrap_peers`
- **Config files**: Store all configs in `~/.debros/` with different filenames
- **--config flag**: Specify which config file to load (defaults to `node.yaml`)

⚠️ **Common Mistake - Same Ports:**
If all nodes use the same ports (e.g., 5001, 7001), they will try to bind to the same addresses and fail to communicate. Verify each node has unique ports:

```bash
# Bootstrap
grep "rqlite_port\|rqlite_raft_port" ~/.debros/bootstrap.yaml
# Should show: rqlite_port: 5001, rqlite_raft_port: 7001

# Node 2
grep "rqlite_port\|rqlite_raft_port" ~/.debros/node.yaml
# Should show: rqlite_port: 5002, rqlite_raft_port: 7002

# Node 3
grep "rqlite_port\|rqlite_raft_port" ~/.debros/node2.yaml
# Should show: rqlite_port: 5003, rqlite_raft_port: 7003
```

If ports are wrong, regenerate the config with `--force`:

```bash
./bin/network-cli config init --type node --name node.yaml \
  --listen-port 4002 --rqlite-http-port 5002 --rqlite-raft-port 7002 \
  --join localhost:5001 --bootstrap-peers '<bootstrap_multiaddr>' --force
```

### Validating Configuration

DeBros Network performs strict validation of all configuration files at startup. This ensures invalid configurations are caught immediately rather than causing silent failures later.

#### Validation Features

- **Strict YAML Parsing:** Unknown configuration keys are rejected with helpful error messages
- **Format Validation:** Multiaddrs, ports, durations, and other formats are validated for correctness
- **Cross-Field Validation:** Configuration constraints (e.g., bootstrap nodes don't join clusters) are enforced
- **Aggregated Error Reporting:** All validation errors are reported together, not one-by-one

#### Common Validation Errors

**Missing or Invalid `node.type`**
```
node.type: must be one of [bootstrap node]; got "invalid"
```
Solution: Set `type: "bootstrap"` or `type: "node"`

**Invalid Bootstrap Peer Format**
```
discovery.bootstrap_peers[0]: invalid multiaddr; expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>
discovery.bootstrap_peers[0]: missing /p2p/<peerID> component
```
Solution: Use full multiaddr format: `/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...`

**Port Conflicts**
```
database.rqlite_raft_port: must differ from database.rqlite_port (5001)
```
Solution: Use different ports for HTTP and Raft (e.g., 5001 and 7001)

**RQLite Join Address Issues (Nodes)**
```
database.rqlite_join_address: required for node type (non-bootstrap)
database.rqlite_join_address: invalid format; expected host:port
```
Solution: Non-bootstrap nodes must specify where to join the cluster. Use Raft port: `127.0.0.1:7001`

**Bootstrap Nodes Cannot Join**
```
database.rqlite_join_address: must be empty for bootstrap type
```
Solution: Bootstrap nodes should have `rqlite_join_address: ""`

**Invalid Listen Addresses**
```
node.listen_addresses[0]: invalid TCP port 99999; port must be between 1 and 65535
```
Solution: Use valid ports [1-65535], e.g., `/ip4/0.0.0.0/tcp/4001`

**Unknown Configuration Keys**
```
invalid config: yaml: unmarshal errors:
  line 42: field migrations_path not found in type config.DatabaseConfig
```
Solution: Remove unsupported keys. Supported keys are documented in the YAML Reference section above.

---

## CLI Usage

### Authentication Commands

```bash
./bin/network-cli auth login                  # Authenticate with wallet
./bin/network-cli auth whoami                 # Show current authentication status
./bin/network-cli auth status                 # Show detailed authentication info
./bin/network-cli auth logout                 # Clear stored credentials
```

### Network Operations

```bash
./bin/network-cli health                    # Check network health
./bin/network-cli status                    # Get network status
./bin/network-cli peers                     # List connected peers
```

### Database Operations

```bash
./bin/network-cli query "SELECT * FROM table"              # Execute SQL
./bin/network-cli query "CREATE TABLE users (id INTEGER)"  # DDL operations
```

### Pub/Sub Messaging

```bash
./bin/network-cli pubsub publish <topic> <message>     # Send message
./bin/network-cli pubsub subscribe <topic> [duration]  # Listen for messages
./bin/network-cli pubsub topics                        # List active topics
```

### CLI Options

```bash
--format json                 # Output in JSON format
--timeout 30s                 # Set operation timeout
--bootstrap <multiaddr>       # Override bootstrap peer
--production                  # Use production bootstrap peers
```

### Database Operations (Gateway REST)

```http
POST /v1/rqlite/exec             # Body: {"sql": "INSERT/UPDATE/DELETE/DDL ...", "args": [...]}
POST /v1/rqlite/find             # Body: {"table":"...", "criteria":{"col":val,...}, "options":{...}}
POST /v1/rqlite/find-one         # Body: same as /find, returns a single row (404 if not found)
POST /v1/rqlite/select           # Body: {"table":"...", "select":[...], "where":[...], "joins":[...], "order_by":[...], "limit":N, "offset":N, "one":false}
POST /v1/rqlite/transaction      # Body: {"ops":[{"kind":"exec|query","sql":"...","args":[...]}], "return_results": true}
POST /v1/rqlite/query            # Body: {"sql": "SELECT ...", "args": [..]}  (legacy-friendly SELECT)
GET  /v1/rqlite/schema           # Returns tables/views + create SQL
POST /v1/rqlite/create-table     # Body: {"schema": "CREATE TABLE ..."}
POST /v1/rqlite/drop-table       # Body: {"table": "table_name"}
```

Common workflows:

```bash
# Exec (INSERT/UPDATE/DELETE/DDL)
curl -X POST "$GW/v1/rqlite/exec" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"sql":"INSERT INTO users(name,email) VALUES(?,?)","args":["Alice","alice@example.com"]}'

# Find (criteria + options)
curl -X POST "$GW/v1/rqlite/find" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{
        "table":"users",
        "criteria":{"active":true},
        "options":{"select":["id","email"],"order_by":["created_at DESC"],"limit":25}
      }'

# Select (fluent builder via JSON)
curl -X POST "$GW/v1/rqlite/select" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{
        "table":"orders o",
        "select":["o.id","o.total","u.email AS user_email"],
        "joins":[{"kind":"INNER","table":"users u","on":"u.id = o.user_id"}],
        "where":[{"conj":"AND","expr":"o.total > ?","args":[100]}],
        "order_by":["o.created_at DESC"],
        "limit":10
      }'

# Transaction (atomic batch)
curl -X POST "$GW/v1/rqlite/transaction" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{
        "return_results": true,
        "ops": [
          {"kind":"exec","sql":"INSERT INTO users(email) VALUES(?)","args":["bob@example.com"]},
          {"kind":"query","sql":"SELECT last_insert_rowid() AS id","args":[]}
        ]
      }'

# Schema
curl "$GW/v1/rqlite/schema" -H "Authorization: Bearer $API_KEY"

# DDL helpers
curl -X POST "$GW/v1/rqlite/create-table" -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"schema":"CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"}'
curl -X POST "$GW/v1/rqlite/drop-table" -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"table":"users"}'
```

### Authentication

The CLI features an enhanced authentication system with explicit command support and automatic wallet detection:

#### Explicit Authentication Commands

Use the `auth` command to manage your credentials:

```bash
# Authenticate with your wallet (opens browser for signature)
./bin/network-cli auth login

# Check if you're authenticated
./bin/network-cli auth whoami

# View detailed authentication info
./bin/network-cli auth status

# Clear all stored credentials
./bin/network-cli auth logout
```

Credentials are stored securely in `~/.debros/credentials.json` with restricted file permissions (readable only by owner).

#### Key Features

- **Explicit Authentication:** Use `auth login` command to authenticate with your wallet
- **Automatic Authentication:** Commands that require auth (query, pubsub, etc.) automatically prompt if needed
- **Multi-Wallet Management:** Seamlessly switch between multiple wallet credentials
- **Persistent Sessions:** Wallet credentials are automatically saved and restored between sessions
- **Enhanced User Experience:** Streamlined authentication flow with better error handling and user feedback

#### Automatic Authentication Flow

When using operations that require authentication (query, pubsub publish/subscribe), the CLI will automatically:

1. Check for existing valid credentials
2. Prompt for wallet authentication if needed
3. Handle signature verification
4. Persist credentials for future use

**Example with automatic authentication:**

```bash
# First time - will prompt for wallet authentication when needed
./bin/network-cli pubsub publish notifications "Hello World"
```

#### Environment Variables

You can override the gateway URL used for authentication:

```bash
export DEBROS_GATEWAY_URL="http://localhost:6001"
./bin/network-cli auth login
```

---

## HTTP Gateway

The DeBros Network includes a powerful HTTP/WebSocket gateway that provides a modern REST API and WebSocket interface over the P2P network, featuring an enhanced authentication system with multi-wallet support.

### Quick Start

```bash
make run-gateway
# Or manually:
go run ./cmd/gateway
```

### Configuration

The gateway can be configured via configs/gateway.yaml and environment variables (env override YAML):

```bash
# Basic Configuration
export GATEWAY_ADDR="0.0.0.0:6001"
export GATEWAY_NAMESPACE="my-app"
export GATEWAY_BOOTSTRAP_PEERS="/ip4/127.0.0.1/tcp/4001/p2p/YOUR_PEER_ID"

# Authentication Configuration
export GATEWAY_REQUIRE_AUTH=true
export GATEWAY_API_KEYS="key1:namespace1,key2:namespace2"
```

### Enhanced Authentication System

The gateway features a significantly improved authentication system with the following capabilities:

#### Key Features

- **Automatic Authentication:** No manual auth commands required - authentication happens automatically when needed
- **Multi-Wallet Support:** Seamlessly manage multiple wallet credentials with automatic switching
- **Persistent Sessions:** Wallet credentials are automatically saved and restored
- **Enhanced User Experience:** Streamlined authentication flow with better error handling

#### Authentication Methods

**Wallet-Based Authentication (Ethereum EIP-191)**

- Uses `personal_sign` for secure wallet verification
- Supports multiple wallets with automatic detection
- Addresses are case-insensitive with normalized signature handling

**JWT Tokens**

- Issued by the gateway with configurable expiration
- JWKS endpoints available at `/v1/auth/jwks` and `/.well-known/jwks.json`
- Automatic refresh capability

**API Keys**

- Support for pre-configured API keys via `Authorization: Bearer <key>` or `X-API-Key` headers
- Optional namespace mapping for multi-tenant applications

### API Endpoints

#### Health & Status

```http
GET /health                 # Basic health check
GET /v1/health             # Detailed health status
GET /v1/status             # Network status
GET /v1/version            # Version information
```

#### Authentication (Public Endpoints)

```http
POST /v1/auth/challenge    # Generate wallet challenge
POST /v1/auth/verify       # Verify wallet signature
POST /v1/auth/register     # Register new wallet
POST /v1/auth/refresh      # Refresh JWT token
POST /v1/auth/logout       # Clear authentication
GET  /v1/auth/whoami       # Current auth status
POST /v1/auth/api-key      # Generate API key (authenticated)
```

#### RQLite HTTP ORM Gateway (/v1/db)

The gateway now exposes a full HTTP interface over the Go ORM-like client (see `pkg/rqlite/gateway.go`) so you can build SDKs in any language.

- Base path: `/v1/db`
- Endpoints:
  - `POST /v1/rqlite/exec` — Execute write/DDL SQL; returns `{ rows_affected, last_insert_id }`
  - `POST /v1/rqlite/find` — Map-based criteria; returns `{ items: [...], count: N }`
  - `POST /v1/rqlite/find-one` — Single row; 404 if not found
  - `POST /v1/rqlite/select` — Fluent SELECT via JSON (joins, where, order, group, limit, offset)
  - `POST /v1/rqlite/transaction` — Atomic batch of exec/query ops, optional per-op results
  - `POST /v1/rqlite/query` — Arbitrary SELECT (legacy-friendly), returns `items`
  - `GET  /v1/rqlite/schema` — List user tables/views + create SQL
  - `POST /v1/rqlite/create-table` — Convenience for DDL
  - `POST /v1/rqlite/drop-table` — Safe drop (identifier validated)

Payload examples are shown in the [Database Operations (Gateway REST)](#database-operations-gateway-rest) section.

#### Network Operations

```http
GET  /v1/network/status    # Network status
GET  /v1/network/peers     # Connected peers
POST /v1/network/connect   # Connect to peer
POST /v1/network/disconnect # Disconnect from peer
```

#### Pub/Sub Messaging

**WebSocket Interface**

```http
GET /v1/pubsub/ws?topic=<topic>  # WebSocket connection for real-time messaging
```

**REST Interface**

```http
POST /v1/pubsub/publish    # Publish message to topic
GET  /v1/pubsub/topics     # List active topics
```

---

## SDK Authoring Guide

### Base concepts

- OpenAPI: a machine-readable spec is available at `openapi/gateway.yaml` for SDK code generation.
- **Auth**: send `X-API-Key: <key>` or `Authorization: Bearer <key|JWT>` with every request.
- **Versioning**: all endpoints are under `/v1/`.
- **Responses**: mutations return `{status:"ok"}`; queries/lists return JSON; errors return `{ "error": "message" }` with proper HTTP status.

### Key HTTP endpoints for SDKs

- **Database**
  - Exec: `POST /v1/rqlite/exec` `{sql, args?}` → `{rows_affected,last_insert_id}`
  - Find: `POST /v1/rqlite/find` `{table, criteria, options?}` → `{items,count}`
  - FindOne: `POST /v1/rqlite/find-one` `{table, criteria, options?}` → single object or 404
  - Select: `POST /v1/rqlite/select` `{table, select?, joins?, where?, order_by?, group_by?, limit?, offset?, one?}`
  - Transaction: `POST /v1/rqlite/transaction` `{ops:[{kind,sql,args?}], return_results?}`
  - Query: `POST /v1/rqlite/query` `{sql, args?}` → `{items,count}`
  - Schema: `GET /v1/rqlite/schema`
  - Create Table: `POST /v1/rqlite/create-table` `{schema}`
  - Drop Table: `POST /v1/rqlite/drop-table` `{table}`
- **PubSub**
  - WS Subscribe: `GET /v1/pubsub/ws?topic=<topic>`
  - Publish: `POST /v1/pubsub/publish` `{topic, data_base64}` → `{status:"ok"}`
  - Topics: `GET /v1/pubsub/topics` → `{topics:[...]}`

---

## Troubleshooting

### Configuration & Permissions

**Error: "Failed to create/access config directory"**

This happens when DeBros cannot access or create `~/.debros/` directory.

**Causes:**
1. Home directory is not writable
2. Home directory doesn't exist
3. Filesystem is read-only (sandboxed/containerized environment)
4. Permission denied (running with wrong user/umask)

**Solutions:**

```bash
# Check home directory exists and is writable
ls -ld ~
touch ~/test-write && rm ~/test-write

# Check umask (should be 0022 or 0002)
umask

# If umask is too restrictive, change it
umask 0022

# Check disk space
df -h ~

# For containerized environments, ensure /home/<user> is mounted with write permissions
docker run -v /home:/home --user $(id -u):$(id -g) debros-network
```

**Error: "Config file not found at ~/.debros/node.yaml"**

The node requires a config file to exist before starting.

**Solution:**

Generate config files first:

```bash
# Build CLI
make build

# Generate configs
./bin/network-cli config init --type bootstrap
./bin/network-cli config init --type node --bootstrap-peers '<peer_multiaddr>'
./bin/network-cli config init --type gateway
```

### Node Startup Issues

**Error: "node.data_dir: parent directory not writable"**

The data directory parent is not accessible.

**Solution:**

Ensure `~/.debros` is writable and has at least 10GB free space:

```bash
# Check permissions
ls -ld ~/.debros

# Check available space
df -h ~/.debros

# Recreate if corrupted
rm -rf ~/.debros
./bin/network-cli config init --type bootstrap
```

**Error: "failed to create data directory"**

The node cannot create its data directory in `~/.debros`.

**Causes:**
1. `~/.debros` is not writable
2. Parent directory path in config uses `~` which isn't expanded properly
3. Disk is full

**Solutions:**

```bash
# Check ~/.debros exists and is writable
mkdir -p ~/.debros
ls -ld ~/.debros

# Verify data_dir in config uses ~ (e.g., ~/.debros/node)
cat ~/.debros/node.yaml | grep data_dir

# Check disk space
df -h ~

# Ensure user owns ~/.debros
chown -R $(whoami) ~/.debros

# Retry node startup
make run-node
```

**Error: "stat ~/.debros: no such file or directory"**

**Port Already in Use**

If you get "address already in use" errors:

```bash
# Find processes using ports
lsof -i :4001  # P2P port
lsof -i :5001  # RQLite HTTP
lsof -i :7001  # RQLite Raft

# Kill if needed
kill -9 <PID>

# Or use different ports in config
./bin/network-cli config init --type node --listen-port 4002 --rqlite-http-port 5002 --rqlite-raft-port 7002
```

### Common Configuration Errors

**Error: "discovery.bootstrap_peers: required for node type"**

Nodes (non-bootstrap) must specify bootstrap peers to discover the network.

**Solution:**

Generate node config with bootstrap peers:

```bash
./bin/network-cli config init --type node --bootstrap-peers '/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...'
```

**Error: "database.rqlite_join_address: required for node type"**

Non-bootstrap nodes must specify which node to join in the Raft cluster.

**Solution:**

Generate config with join address:

```bash
./bin/network-cli config init --type node --join localhost:5001
```

**Error: "database.rqlite_raft_port: must differ from database.rqlite_port"**

HTTP and Raft ports cannot be the same.

**Solution:**

Use different ports (RQLite HTTP and Raft must be on different ports):

```bash
./bin/network-cli config init --type node \
  --rqlite-http-port 5001 \
  --rqlite-raft-port 7001
```

### Peer Discovery Issues

If nodes can't find each other:

1. **Verify bootstrap node is running:**
   ```bash
   ./bin/network-cli health
   ./bin/network-cli peers
   ```

2. **Check bootstrap peer multiaddr is correct:**
   ```bash
   cat ~/.debros/bootstrap/peer.info  # On bootstrap node
   # Should match value in other nodes' discovery.bootstrap_peers
   ```

3. **Ensure all nodes have same bootstrap peers in config**

4. **Check firewall/network:**
   ```bash
   # Verify P2P port is open
   nc -zv 127.0.0.1 4001
   ```

---

## License