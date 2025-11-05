# DeBros Network - Distributed P2P Database System

DeBros Network is a decentralized peer-to-peer data platform built in Go. It combines distributed SQL (RQLite), pub/sub messaging, and resilient peer discovery so applications can share state without central infrastructure.

## Table of Contents

- [At a Glance](#at-a-glance)
- [Quick Start](#quick-start)
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
   ./bin/network-cli config init
   ```

3. Launch the full development stack:

   ```bash
   make dev
   ```

   This starts three nodes and the HTTP gateway. Stop with `Ctrl+C`.

4. Validate the network from another terminal:

   ```bash
   ./bin/network-cli health
   ./bin/network-cli peers
   ./bin/network-cli pubsub publish notifications "Hello World"
   ./bin/network-cli pubsub subscribe notifications 10s
   ```

## Components & Ports

- **Bootstrap node**: P2P `4001`, RQLite HTTP `5001`, Raft `7001`
- **Additional nodes** (`node2`, `node3`): Incrementing ports (`400{2,3}`, `500{2,3}`, `700{2,3}`)
- **Gateway**: HTTP `6001` exposes REST/WebSocket APIs
- **Data directory**: `~/.debros/` stores configs, identities, and RQLite data

Use `make dev` for the complete stack or run binaries individually with `go run ./cmd/node --config <file>` and `go run ./cmd/gateway --config gateway.yaml`.

## Configuration Cheatsheet

All runtime configuration lives in `~/.debros/`.

- `bootstrap.yaml`: `type: bootstrap`, blank `database.rqlite_join_address`
- `node*.yaml`: `type: node`, set `database.rqlite_join_address` (e.g. `127.0.0.1:7001`) and include the bootstrap `discovery.bootstrap_peers`
- `gateway.yaml`: configure `gateway.bootstrap_peers`, `gateway.namespace`, and optional auth flags

Validation reminders:

- HTTP and Raft ports must differ
- Non-bootstrap nodes require a join address and bootstrap peers
- Bootstrap nodes cannot define a join address
- Multiaddrs must end with `/p2p/<peerID>`

Regenerate configs any time with `./bin/network-cli config init --force`.

## CLI Highlights

All commands accept `--format json`, `--timeout <duration>`, and `--bootstrap <multiaddr>`.

- **Auth**

  ```bash
  ./bin/network-cli auth login
  ./bin/network-cli auth status
  ./bin/network-cli auth logout
  ```

- **Network**

  ```bash
  ./bin/network-cli health
  ./bin/network-cli status
  ./bin/network-cli peers
  ```

- **Database**

  ```bash
  ./bin/network-cli query "SELECT * FROM users"
  ./bin/network-cli query "CREATE TABLE users (id INTEGER PRIMARY KEY)"
  ./bin/network-cli transaction --file ops.json
  ```

- **Pub/Sub**

  ```bash
  ./bin/network-cli pubsub publish <topic> <message>
  ./bin/network-cli pubsub subscribe <topic> 30s
  ./bin/network-cli pubsub topics
  ```

Credentials live at `~/.debros/credentials.json` with user-only permissions.

## HTTP Gateway

Start locally with `make run-gateway` or `go run ./cmd/gateway --config gateway.yaml`.

Environment overrides:

```bash
export GATEWAY_ADDR="0.0.0.0:6001"
export GATEWAY_NAMESPACE="my-app"
export GATEWAY_BOOTSTRAP_PEERS="/ip4/127.0.0.1/tcp/4001/p2p/<peerID>"
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
- **Missing configs**: Run `./bin/network-cli config init` before starting nodes.
- **Cluster join issues**: Confirm the bootstrap node is running, `peer.info` multiaddr matches `bootstrap_peers`, and firewall rules allow the P2P ports.

## Resources

- Go modules: `go mod tidy`, `go test ./...`
- Automation: `make build`, `make dev`, `make run-gateway`, `make lint`
- API reference: `openapi/gateway.yaml`
- Code of Conduct: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
