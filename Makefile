TEST?=./...

.PHONY: test
test:
	@echo Running tests...
	go test -v $(TEST)

# Gateway-focused E2E tests assume gateway and nodes are already running
# Configure via env:
#   GATEWAY_BASE_URL (default http://127.0.0.1:6001)
#   GATEWAY_API_KEY  (required for auth-protected routes)
.PHONY: test-e2e
test-e2e:
	@echo "Running gateway E2E tests (HTTP/WS only)..."
	@echo "Base URL: $${GATEWAY_BASE_URL:-http://127.0.0.1:6001}"
	@test -n "$$GATEWAY_API_KEY" || (echo "GATEWAY_API_KEY must be set" && exit 1)
	go test -v -tags e2e ./e2e

# Network - Distributed P2P Database System
# Makefile for development and build tasks

.PHONY: build clean test run-node run-node2 run-node3 run-example deps tidy fmt vet lint clear-ports

VERSION := 0.53.12
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

# Build targets
build: deps
	@echo "Building network executables (version=$(VERSION))..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/identity ./cmd/identity
	go build -ldflags "$(LDFLAGS)" -o bin/node ./cmd/node
	go build -ldflags "$(LDFLAGS)" -o bin/network-cli cmd/cli/main.go
	# Inject gateway build metadata via pkg path variables
	go build -ldflags "$(LDFLAGS) -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildVersion=$(VERSION)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildCommit=$(COMMIT)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildTime=$(DATE)'" -o bin/gateway ./cmd/gateway
	@echo "Build complete! Run ./bin/network-cli version"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -rf data/
	@echo "Clean complete!"

# Run bootstrap node (auto-selects identity and data dir)
run-node:
	@echo "Starting bootstrap node..."
	@echo "Config: ~/.debros/bootstrap.yaml"
	@echo "Generate it with: network-cli config init --type bootstrap"
	go run ./cmd/node --config node.yaml

# Run second node (regular) - requires join address of bootstrap node
# Usage: make run-node2 JOINADDR=/ip4/127.0.0.1/tcp/5001 HTTP=5002 RAFT=7002 P2P=4002
run-node2:
	@echo "Starting regular node (node.yaml)..."
	@echo "Config: ~/.debros/node.yaml"
	@echo "Generate it with: network-cli config init --type node --join localhost:5001 --bootstrap-peers '<peer_multiaddr>'"
	go run ./cmd/node --config node2.yaml

# Run third node (regular) - requires join address of bootstrap node
# Usage: make run-node3 JOINADDR=/ip4/127.0.0.1/tcp/5001 HTTP=5003 RAFT=7003 P2P=4003
run-node3:
	@echo "Starting regular node (node2.yaml)..."
	@echo "Config: ~/.debros/node2.yaml"
	@echo "Generate it with: network-cli config init --type node --name node2.yaml --join localhost:5001 --bootstrap-peers '<peer_multiaddr>'"
	go run ./cmd/node --config node3.yaml

# Run gateway HTTP server
# Usage examples:
#   make run-gateway                                   # uses ~/.debros/gateway.yaml
#   Config generated with: network-cli config init --type gateway
run-gateway:
	@echo "Starting gateway HTTP server..."
	@echo "Note: Config must be in ~/.debros/gateway.yaml"
	@echo "Generate it with: network-cli config init --type gateway"
	go run ./cmd/gateway

# One-command dev: Start bootstrap, node2, node3, gateway, and anon in background
# Requires: configs already exist in ~/.debros
dev: build
	@echo "🚀 Starting development network stack..."
	@mkdir -p .dev/pids
	@mkdir -p $$HOME/.debros/logs
	@echo "Starting Anyone client (anon proxy)..."
	@if [ "$$(uname)" = "Darwin" ]; then \
		echo "  Detected macOS - using npx anyone-client"; \
		if command -v npx >/dev/null 2>&1; then \
			nohup npx anyone-client > $$HOME/.debros/logs/anon.log 2>&1 & echo $$! > .dev/pids/anon.pid; \
			echo "  Anyone client started (PID: $$(cat .dev/pids/anon.pid))"; \
		else \
			echo "  ⚠️  npx not found - skipping Anyone client"; \
			echo "  Install with: npm install -g npm"; \
		fi; \
	elif [ "$$(uname)" = "Linux" ]; then \
		echo "  Detected Linux - checking systemctl"; \
		if systemctl is-active --quiet anon 2>/dev/null; then \
			echo "  ✓ Anon service already running"; \
		elif command -v systemctl >/dev/null 2>&1; then \
			echo "  Starting anon service..."; \
			sudo systemctl start anon 2>/dev/null || echo "  ⚠️  Failed to start anon service"; \
		else \
			echo "  ⚠️  systemctl not found - skipping Anon"; \
		fi; \
	fi
	@sleep 2
	@echo "Starting bootstrap node..."
	@nohup ./bin/node --config bootstrap.yaml > $$HOME/.debros/logs/bootstrap.log 2>&1 & echo $$! > .dev/pids/bootstrap.pid
	@sleep 2
	@echo "Starting node2..."
	@nohup ./bin/node --config node2.yaml > $$HOME/.debros/logs/node2.log 2>&1 & echo $$! > .dev/pids/node2.pid
	@sleep 1
	@echo "Starting node3..."
	@nohup ./bin/node --config node3.yaml > $$HOME/.debros/logs/node3.log 2>&1 & echo $$! > .dev/pids/node3.pid
	@sleep 1
	@echo "Starting gateway..."
	@nohup ./bin/gateway --config gateway.yaml > $$HOME/.debros/logs/gateway.log 2>&1 & echo $$! > .dev/pids/gateway.pid
	@echo ""
	@echo "============================================================"
	@echo "✅ Development stack started!"
	@echo "============================================================"
	@echo ""
	@echo "Processes:"
	@if [ -f .dev/pids/anon.pid ]; then \
		echo "  Anon:      PID=$$(cat .dev/pids/anon.pid) (SOCKS: 9050)"; \
	fi
	@echo "  Bootstrap: PID=$$(cat .dev/pids/bootstrap.pid)"
	@echo "  Node2:     PID=$$(cat .dev/pids/node2.pid)"
	@echo "  Node3:     PID=$$(cat .dev/pids/node3.pid)"
	@echo "  Gateway:   PID=$$(cat .dev/pids/gateway.pid)"
	@echo ""
	@echo "Ports:"
	@echo "  Anon SOCKS:    9050 (proxy endpoint: POST /v1/proxy/anon)"
	@echo "  Bootstrap P2P: 4001, HTTP: 5001, Raft: 7001"
	@echo "  Node2     P2P: 4002, HTTP: 5002, Raft: 7002"
	@echo "  Node3     P2P: 4003, HTTP: 5003, Raft: 7003"
	@echo "  Gateway:       6001"
	@echo ""
	@echo "Press Ctrl+C to stop all processes"
	@echo "============================================================"
	@echo ""
	@if [ -f .dev/pids/anon.pid ]; then \
		trap 'echo "Stopping all processes..."; kill $$(cat .dev/pids/*.pid) 2>/dev/null; rm -f .dev/pids/*.pid; exit 0' INT; \
		tail -f $$HOME/.debros/logs/anon.log $$HOME/.debros/logs/bootstrap.log $$HOME/.debros/logs/node2.log $$HOME/.debros/logs/node3.log $$HOME/.debros/logs/gateway.log; \
	else \
		trap 'echo "Stopping all processes..."; kill $$(cat .dev/pids/*.pid) 2>/dev/null; rm -f .dev/pids/*.pid; exit 0' INT; \
		tail -f $$HOME/.debros/logs/bootstrap.log $$HOME/.debros/logs/node2.log $$HOME/.debros/logs/node3.log $$HOME/.debros/logs/gateway.log; \
	fi

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build all executables"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run tests"
	@echo ""
	@echo "Development:"
	@echo "  dev           - Start full dev stack (bootstrap + 2 nodes + gateway)"
	@echo "                 Requires: configs in ~/.debros (run 'network-cli config init' first)"
	@echo ""
	@echo "Configuration (NEW):"
	@echo "  First, generate config files in ~/.debros with:"
	@echo "    make build                                         # Build CLI first"
	@echo "    ./bin/network-cli config init                      # Generate full stack"
	@echo ""
	@echo "Network Targets (requires config files in ~/.debros):"
	@echo "  run-node      - Start bootstrap node"
	@echo "  run-node2     - Start second node"
	@echo "  run-node3     - Start third node"
	@echo "  run-gateway   - Start HTTP gateway"
	@echo "  run-example   - Run usage example"
	@echo ""
	@echo "Running Multiple Nodes:"
	@echo "  Nodes use --config flag to select which YAML file in ~/.debros to load:"
	@echo "    go run ./cmd/node --config bootstrap.yaml"
	@echo "    go run ./cmd/node --config node.yaml"
	@echo "    go run ./cmd/node --config node2.yaml"
	@echo "  Generate configs with: ./bin/network-cli config init --name <filename.yaml>"
	@echo ""
	@echo "CLI Commands:"
	@echo "  run-cli       - Run network CLI help"
	@echo "  cli-health    - Check network health"
	@echo "  cli-peers     - List network peers"
	@echo "  cli-status    - Get network status"
	@echo "  cli-storage-test - Test storage operations"
	@echo "  cli-pubsub-test  - Test pub/sub operations"
	@echo ""
	@echo "Development:"
	@echo "  test-multinode   - Full multi-node test with 1 bootstrap + 2 nodes"
	@echo "  test-peer-discovery - Test peer discovery (requires running nodes)"
	@echo "  test-replication - Test data replication (requires running nodes)"
	@echo "  test-consensus   - Test database consensus (requires running nodes)"
	@echo ""
	@echo "Maintenance:"
	@echo "  deps          - Download dependencies"
	@echo "  tidy          - Tidy dependencies"
	@echo "  fmt           - Format code"
	@echo "  vet           - Vet code"
	@echo "  lint          - Lint code (fmt + vet)"
	@echo "  clear-ports   - Clear common dev ports"
	@echo "  dev-setup     - Setup development environment"
	@echo "  dev-cluster   - Show cluster startup commands"
	@echo "  dev           - Full development workflow"
	@echo "  help          - Show this help"
