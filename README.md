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

### Building

```bash
# Build all binaries
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
orama app list                               # List all deployments
orama app get <name>                         # Get deployment details
orama app logs <name> --follow               # View logs
orama app delete <name>                      # Delete deployment
orama app rollback <name> --version 1        # Rollback to version
```

### SQLite Databases

```bash
orama db create <name>                   # Create database
orama db query <name> "SELECT * FROM t"  # Execute SQL query
orama db list                            # List all databases
orama db backup <name>                   # Backup to IPFS
orama db backups <name>                  # List backups
```

### Environment Management

```bash
orama env list              # List available environments
orama env current           # Show active environment
orama env use <name>        # Switch environment
```

## Serverless Functions (WASM)

Orama supports high-performance serverless function execution using WebAssembly (WASM). Functions are isolated, secure, and can interact with network services like the distributed cache.

> **Full guide:** See [docs/SERVERLESS.md](docs/SERVERLESS.md) for host functions API, secrets management, PubSub triggers, and examples.

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
curl -X POST https://your-node.example.com/v1/functions \
  -H "Authorization: Bearer <your_api_key>" \
  -F "name=hello-world" \
  -F "namespace=default" \
  -F "wasm=@./examples/functions/bin/hello.wasm"
```

### 3. Invocation

Trigger your function with a JSON payload. The function receives the payload via `stdin` and returns its response via `stdout`.

```bash
# Invoke via HTTP
curl -X POST https://your-node.example.com/v1/functions/hello-world/invoke \
  -H "Authorization: Bearer <your_api_key>" \
  -H "Content-Type: application/json" \
  -d '{"name": "Developer"}'
```

### 4. Management

```bash
# List all functions in a namespace
curl https://your-node.example.com/v1/functions?namespace=default

# Delete a function
curl -X DELETE https://your-node.example.com/v1/functions/hello-world?namespace=default
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
- 9050 - Anyone SOCKS5 proxy
- 9094 - IPFS Cluster API
- 3320/3322 - Olric Cache

**Anyone Relay Mode (optional, for earning rewards):**

- 9001 - Anyone ORPort (relay traffic, must be open externally)

### Anyone Network Integration

Orama Network integrates with the [Anyone Protocol](https://anyone.io) for anonymous routing. By default, nodes run as **clients** (consuming the network). Optionally, you can run as a **relay operator** to earn rewards.

**Client Mode (Default):**
- Routes traffic through Anyone network for anonymity
- SOCKS5 proxy on localhost:9050
- No rewards, just consumes network

**Relay Mode (Earn Rewards):**
- Provide bandwidth to the Anyone network
- Earn $ANYONE tokens as a relay operator
- Requires 100 $ANYONE tokens in your wallet
- Requires ORPort (9001) open to the internet

```bash
# Install as relay operator (earn rewards)
sudo orama node install --vps-ip <IP> --domain <domain> \
  --anyone-relay \
  --anyone-nickname "MyRelay" \
  --anyone-contact "operator@email.com" \
  --anyone-wallet "0x1234...abcd"

# With exit relay (legal implications apply)
sudo orama node install --vps-ip <IP> --domain <domain> \
  --anyone-relay \
  --anyone-exit \
  --anyone-nickname "MyExitRelay" \
  --anyone-contact "operator@email.com" \
  --anyone-wallet "0x1234...abcd"

# Migrate existing Anyone installation
sudo orama node install --vps-ip <IP> --domain <domain> \
  --anyone-relay \
  --anyone-migrate \
  --anyone-nickname "MyRelay" \
  --anyone-contact "operator@email.com" \
  --anyone-wallet "0x1234...abcd"
```

**Important:** After installation, register your relay at [dashboard.anyone.io](https://dashboard.anyone.io) to start earning rewards.

### Installation

**macOS (Homebrew):**

```bash
brew install DeBrosOfficial/tap/orama
```

**Linux (Debian/Ubuntu):**

```bash
# Download and install the latest .deb package
curl -sL https://github.com/DeBrosOfficial/network/releases/latest/download/orama_$(curl -s https://api.github.com/repos/DeBrosOfficial/network/releases/latest | grep tag_name | cut -d '"' -f 4 | tr -d 'v')_linux_amd64.deb -o orama.deb
sudo dpkg -i orama.deb
```

**From Source:**

```bash
go install github.com/DeBrosOfficial/network/cmd/cli@latest
```

**Setup (after installation):**

```bash
sudo orama node install --interactive
```

### Service Management

```bash
# Status
sudo orama node status

# Control services
sudo orama node start
sudo orama node stop
sudo orama node restart

# Diagnose issues
sudo orama node doctor

# View logs
orama node logs node --follow
orama node logs gateway --follow
orama node logs ipfs --follow
```

### Upgrade

```bash
# Upgrade to latest version
sudo orama node upgrade --restart
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
sudo orama node status

# View logs
orama node logs node --follow

# Check log files
sudo orama node doctor
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
sudo orama node uninstall
sudo rm -rf /opt/orama/.orama
sudo orama node install
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
- **[Monitoring](docs/MONITORING.md)** - Cluster monitoring and health checks
- **[Inspector](docs/INSPECTOR.md)** - Deep subsystem health inspection
- **[Serverless Functions](docs/SERVERLESS.md)** - WASM serverless with host functions
- **[WebRTC](docs/WEBRTC.md)** - Real-time communication setup
- **[Common Problems](docs/COMMON_PROBLEMS.md)** - Troubleshooting known issues

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
