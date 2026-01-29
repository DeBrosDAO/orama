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

| Flag | Description |
|------|-------------|
| `--branch <branch>` | Git branch to pull from (production deployment) |
| `--no-pull` | Skip git pull, use existing `/home/debros/src` (dev deployment) |
| `--restart` | Restart all services after upgrade |
| `--nameserver` | Configure this node as a nameserver (install only) |
| `--domain <domain>` | Domain for HTTPS certificates (install only) |
| `--vps-ip <ip>` | VPS public IP address (install only) |

## Debugging Production Issues

Always follow the local-first approach:

1. **Reproduce locally** — set up the same conditions on your machine
2. **Find the root cause** — understand why it's happening
3. **Fix in the codebase** — make changes to the source code
4. **Test locally** — run `make test` and verify
5. **Deploy** — only then deploy the fix to production

Never fix issues directly on the server — those fixes are lost on next deployment.

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
