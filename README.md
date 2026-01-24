# Orama Network - Distributed P2P Platform

A high-performance API Gateway and distributed platform built in Go. Provides a unified HTTP/HTTPS API for distributed SQL (RQLite), distributed caching (Olric), decentralized storage (IPFS), pub/sub messaging, and serverless WebAssembly execution.

**Architecture:** Modular Gateway / Edge Proxy following SOLID principles

## Features

- **🔐 Authentication** - Wallet signatures, API keys, JWT tokens
- **💾 Storage** - IPFS-based decentralized file storage with encryption
- **⚡ Cache** - Distributed cache with Olric (in-memory key-value)
- **🗄️ Database** - RQLite distributed SQL with Raft consensus + Per-namespace SQLite databases
- **📡 Pub/Sub** - Real-time messaging via LibP2P and WebSocket
- **⚙️ Serverless** - WebAssembly function execution with host functions
- **🌐 HTTP Gateway** - Unified REST API with automatic HTTPS (Let's Encrypt)
- **📦 Client SDK** - Type-safe Go SDK for all services
- **🚀 App Deployments** - Deploy React, Next.js, Go, Node.js apps with automatic domains
- **🗄️ SQLite Databases** - Per-namespace isolated databases with IPFS backups

## Application Deployments

Deploy full-stack applications with automatic domain assignment and namespace isolation.

### Deploy a React App

```bash
# Build your app
cd my-react-app
npm run build

# Deploy to Orama Network
orama deploy static ./dist --name my-app

# Your app is now live at: https://my-app.orama.network
```

### Deploy Next.js with SSR

```bash
cd my-nextjs-app

# Ensure next.config.js has: output: 'standalone'
npm run build
orama deploy nextjs . --name my-nextjs --ssr

# Live at: https://my-nextjs.orama.network
```

### Deploy Go Backend

```bash
# Build for Linux (name binary 'app' for auto-detection)
GOOS=linux GOARCH=amd64 go build -o app main.go

# Deploy (must implement /health endpoint)
orama deploy go ./app --name my-api

# API live at: https://my-api.orama.network
```

### Create SQLite Database

```bash
# Create database
orama db create my-database

# Create schema
orama db query my-database "CREATE TABLE users (id INT, name TEXT)"

# Insert data
orama db query my-database "INSERT INTO users VALUES (1, 'Alice')"

# Query data
orama db query my-database "SELECT * FROM users"

# Backup to IPFS
orama db backup my-database
```

### Full-Stack Example

Deploy a complete app with React frontend, Go backend, and SQLite database:

```bash
# 1. Create database
orama db create myapp-db
orama db query myapp-db "CREATE TABLE users (id INT PRIMARY KEY, name TEXT)"

# 2. Deploy Go backend (connects to database)
GOOS=linux GOARCH=amd64 go build -o api main.go
orama deploy go ./api --name myapp-api

# 3. Deploy React frontend (calls backend API)
cd frontend && npm run build
orama deploy static ./dist --name myapp

# Access:
# Frontend: https://myapp.orama.network
# Backend: https://myapp-api.orama.network
```

**📖 Full Guide**: See [Deployment Guide](docs/DEPLOYMENT_GUIDE.md) for complete documentation, examples, and best practices.

## Quick Start

### Local Development

```bash
# Build the project
make build

# Start 5-node development cluster
make dev
```

The cluster automatically performs health checks before declaring success.

### Stop Development Environment

```bash
make stop
```

## Testing Services

After running `make dev`, test service health using these curl requests:

### Node Unified Gateways

Each node is accessible via a single unified gateway port:

```bash
# Node-1 (port 6001)
curl http://localhost:6001/health

# Node-2 (port 6002)
curl http://localhost:6002/health

# Node-3 (port 6003)
curl http://localhost:6003/health

# Node-4 (port 6004)
curl http://localhost:6004/health

# Node-5 (port 6005)
curl http://localhost:6005/health
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
orama stop

# Build binaries
make build
```

## CLI Commands

### Authentication

```bash
orama auth login            # Authenticate with wallet
orama auth status           # Check authentication
orama auth logout           # Clear credentials
```

### Application Deployments

```bash
# Deploy applications
orama deploy static <path> --name myapp       # React, Vue, static sites
orama deploy nextjs <path> --name myapp --ssr # Next.js with SSR (requires output: 'standalone')
orama deploy go <path> --name myapp           # Go binaries (must have /health endpoint)
orama deploy nodejs <path> --name myapp       # Node.js apps (must have /health endpoint)

# Manage deployments
orama deployments list                        # List all deployments
orama deployments get <name>                  # Get deployment details
orama deployments logs <name> --follow        # View logs
orama deployments delete <name>               # Delete deployment
orama deployments rollback <name> --version 1 # Rollback to version
```

### SQLite Databases

```bash
orama db create <name>                   # Create database
orama db query <name> "SELECT * FROM t"  # Execute SQL query
orama db list                            # List all databases
orama db backup <name>                   # Backup to IPFS
orama db backups <name>                  # List backups
```

### Network Status

```bash
orama health                # Cluster health check
orama peers                 # List connected peers
orama status                # Network status
```

### RQLite Operations

```bash
orama query "SELECT * FROM users"
orama query "CREATE TABLE users (id INTEGER PRIMARY KEY)"
orama transaction --file ops.json
```

### Pub/Sub

```bash
orama pubsub publish <topic> <message>
orama pubsub subscribe <topic> 30s
orama pubsub topics
```

## Serverless Functions (WASM)

Orama supports high-performance serverless function execution using WebAssembly (WASM). Functions are isolated, secure, and can interact with network services like the distributed cache.

### 1. Build Functions

Functions must be compiled to WASM. We recommend using [TinyGo](https://tinygo.org/).

```bash
# Build example functions to examples/functions/bin/
./examples/functions/build.sh
```

### 2. Deployment

Deploy your compiled `.wasm` file to the network via the Gateway.

```bash
# Deploy a function
curl -X POST http://localhost:6001/v1/functions \
  -H "Authorization: Bearer <your_api_key>" \
  -F "name=hello-world" \
  -F "namespace=default" \
  -F "wasm=@./examples/functions/bin/hello.wasm"
```

### 3. Invocation

Trigger your function with a JSON payload. The function receives the payload via `stdin` and returns its response via `stdout`.

```bash
# Invoke via HTTP
curl -X POST http://localhost:6001/v1/functions/hello-world/invoke \
  -H "Authorization: Bearer <your_api_key>" \
  -H "Content-Type: application/json" \
  -d '{"name": "Developer"}'
```

### 4. Management

```bash
# List all functions in a namespace
curl http://localhost:6001/v1/functions?namespace=default

# Delete a function
curl -X DELETE http://localhost:6001/v1/functions/hello-world?namespace=default
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

sudo orama install --interactive
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
sudo orama upgrade --interactive
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
- `POST /v1/functions` - Deploy function (multipart/form-data)
- `POST /v1/functions/{name}/invoke` - Invoke function
- `GET /v1/functions` - List functions
- `DELETE /v1/functions/{name}` - Delete function
- `GET /v1/functions/{name}/logs` - Get function logs

See `openapi/gateway.yaml` for complete API specification.

## Documentation

- **[Deployment Guide](docs/DEPLOYMENT_GUIDE.md)** - Deploy React, Next.js, Go apps and manage databases
- **[Architecture Guide](docs/ARCHITECTURE.md)** - System architecture and design patterns
- **[Client SDK](docs/CLIENT_SDK.md)** - Go SDK documentation and examples
- **[Gateway API](docs/GATEWAY_API.md)** - Complete HTTP API reference
- **[Security Deployment](docs/SECURITY_DEPLOYMENT_GUIDE.md)** - Production security hardening
- **[Testing Plan](docs/TESTING_PLAN.md)** - Comprehensive testing strategy and implementation

## Resources

- [RQLite Documentation](https://rqlite.io/docs/)
- [IPFS Documentation](https://docs.ipfs.tech/)
- [LibP2P Documentation](https://docs.libp2p.io/)
- [WebAssembly](https://webassembly.org/)
- [GitHub Repository](https://github.com/DeBrosOfficial/network)
- [Issue Tracker](https://github.com/DeBrosOfficial/network/issues)

## Project Structure

```
network/
├── cmd/              # Binary entry points
│   ├── cli/         # CLI tool
│   ├── gateway/     # HTTP Gateway
│   ├── node/        # P2P Node
│   └── rqlite-mcp/  # RQLite MCP server
├── pkg/              # Core packages
│   ├── gateway/     # Gateway implementation
│   │   └── handlers/ # HTTP handlers by domain
│   ├── client/      # Go SDK
│   ├── serverless/  # WASM engine
│   ├── rqlite/      # Database ORM
│   ├── contracts/   # Interface definitions
│   ├── httputil/    # HTTP utilities
│   └── errors/      # Error handling
├── docs/            # Documentation
├── e2e/             # End-to-end tests
└── examples/        # Example code
```

## Contributing

Contributions are welcome! This project follows:
- **SOLID Principles** - Single responsibility, open/closed, etc.
- **DRY Principle** - Don't repeat yourself
- **Clean Architecture** - Clear separation of concerns
- **Test Coverage** - Unit and E2E tests required

See our architecture docs for design patterns and guidelines.
