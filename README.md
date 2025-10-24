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
go run ./cmd/node --config configs/bootstrap.yaml
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

### Example Configuration Files

#### `configs/bootstrap.yaml`

```yaml
node:
  id: ""
  type: "bootstrap"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/4001"
  data_dir: "./data/bootstrap"
  max_connections: 100

database:
  data_dir: "./data/bootstrap/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: 24h
  rqlite_port: 5001
  rqlite_raft_port: 7001
  rqlite_join_address: ""

discovery:
  bootstrap_peers: []
  discovery_interval: 15s
  bootstrap_port: 4001
  http_adv_address: "127.0.0.1"
  raft_adv_address: ""

security:
  enable_tls: false
  private_key_file: ""
  certificate_file: ""

logging:
  level: "info"
  format: "console"
  output_file: ""
```

#### `configs/node.yaml`

```yaml
node:
  id: "node2"
  type: "node"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/4002"
  data_dir: "./data/node2"
  max_connections: 50

database:
  data_dir: "./data/node2/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: 24h
  rqlite_port: 5002
  rqlite_raft_port: 7002
  rqlite_join_address: "127.0.0.1:7001"

discovery:
  bootstrap_peers:
    - "/ip4/127.0.0.1/tcp/4001/p2p/<YOUR_BOOTSTRAP_PEER_ID>"
  discovery_interval: 15s
  bootstrap_port: 4002
  http_adv_address: "127.0.0.1"
  raft_adv_address: ""

security:
  enable_tls: false
  private_key_file: ""
  certificate_file: ""

logging:
  level: "info"
  format: "console"
  output_file: ""
```

### YAML Reference

#### Node YAML (configs/node.yaml or configs/bootstrap.yaml)

The .yaml files are required in order for the nodes and the gateway to run correctly.

node:

- id (string) Optional node ID. Auto-generated if empty.
- type (string) "bootstrap" or "node". Default: "node".
- listen_addresses (string[]) LibP2P listen multiaddrs. Default: ["/ip4/0.0.0.0/tcp/4001"].
- data_dir (string) Data directory. Default: "./data".
- max_connections (int) Max peer connections. Default: 50.

database:

- data_dir (string) Directory for database files. Default: "./data/db".
- replication_factor (int) Number of replicas. Default: 3.
- shard_count (int) Shards for data distribution. Default: 16.
- max_database_size (int64 bytes) Max DB size. Default: 1073741824 (1GB).
- backup_interval (duration) e.g., "24h". Default: 24h.
- rqlite_port (int) RQLite HTTP API port. Default: 5001.
- rqlite_raft_port (int) RQLite Raft port. Default: 7001.
- rqlite_join_address (string) Raft address of an existing RQLite node to join (host:port format). Empty for bootstrap.

discovery:

- bootstrap_peers (string[]) List of LibP2P multiaddrs of bootstrap peers.
- discovery_interval (duration) How often to announce/discover peers. Default: 15s.
- bootstrap_port (int) Default port for bootstrap nodes. Default: 4001.
- http_adv_address (string) Advertised HTTP address for RQLite (host:port).
- raft_adv_address (string) Advertised Raft address (host:port).
- node_namespace (string) Namespace for node identifiers. Default: "default".

security:

- enable_tls (bool) Enable TLS for externally exposed services. Default: false.
- private_key_file (string) Path to TLS private key (if TLS enabled).
- certificate_file (string) Path to TLS certificate (if TLS enabled).

logging:

- level (string) one of "debug", "info", "warn", "error". Default: "info".
- format (string) "json" or "console". Default: "console".
- output_file (string) Empty for stdout; otherwise path to log file.

Precedence (node): Flags > YAML > Defaults.

Example node.yaml

```yaml
node:
  id: "node2"
  type: "node"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/4002"
  data_dir: "./data/node2"
  max_connections: 50

database:
  data_dir: "./data/node2/rqlite"
  replication_factor: 3
  shard_count: 16
  max_database_size: 1073741824
  backup_interval: 24h
  rqlite_port: 5002
  rqlite_raft_port: 7002
  rqlite_join_address: "127.0.0.1:7001"

discovery:
  bootstrap_peers:
    - "/ip4/127.0.0.1/tcp/4001/p2p/<YOUR_BOOTSTRAP_PEER_ID>"
  discovery_interval: 15s
  bootstrap_port: 4001
  http_adv_address: "127.0.0.1"
  raft_adv_address: ""
  node_namespace: "default"

security:
  enable_tls: false
  private_key_file: ""
  certificate_file: ""

logging:
  level: "info"
  format: "console"
  output_file: ""
```

#### Gateway YAML (configs/gateway.yaml)

- listen_addr (string) HTTP listen address, e.g., ":6001". Default: ":6001".
- client_namespace (string) Namespace used by the gateway client. Default: "default".
- bootstrap_peers (string[]) List of bootstrap peer multiaddrs. Default: empty.

Precedence (gateway): Flags > Environment Variables > YAML > Defaults.
Environment variables:

- GATEWAY_ADDR
- GATEWAY_NAMESPACE
- GATEWAY_BOOTSTRAP_PEERS (comma-separated)

Example gateway.yaml

```yaml
listen_addr: ":6001"
client_namespace: "default"
bootstrap_peers:
  - "<YOUR_BOOTSTRAP_PEER_ID_MULTIADDR>"
```

### Flags & Environment Variables

- **Flags**: Override config at startup (`--data`, `--p2p-port`, `--rqlite-http-port`, etc.)
- **Env Vars**: Override config and flags (`NODE_ID`, `RQLITE_PORT`, `BOOTSTRAP_PEERS`, etc.)
- **Precedence (gateway)**: Flags > Env Vars > YAML > Defaults
- **Precedence (node)**: Flags > YAML > Defaults

### Bootstrap & Database Endpoints

- **Bootstrap peers**: Set in config or via `BOOTSTRAP_PEERS` env var.
- **Database endpoints**: Set in config or via `RQLITE_NODES` env var.
- **Development mode**: Use `NETWORK_DEV_LOCAL=1` for localhost defaults.

### Configuration Validation

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
  - Topics: `GET /v1/pubsub/topics` → `