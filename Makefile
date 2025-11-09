TEST?=./...

.PHONY: test
test:
	@echo Running tests...
	go test -v $(TEST)

# Gateway-focused E2E tests assume gateway and nodes are already running
# Configure via env:
#   GATEWAY_BASE_URL (default http://localhost:6001)
#   GATEWAY_API_KEY  (required for auth-protected routes)
.PHONY: test-e2e
test-e2e:
	@echo "Running gateway E2E tests (HTTP/WS only)..."
	@echo "Base URL: $${GATEWAY_BASE_URL:-http://localhost:6001}"
	@test -n "$$GATEWAY_API_KEY" || (echo "GATEWAY_API_KEY must be set" && exit 1)
	go test -v -tags e2e ./e2e

# Network - Distributed P2P Database System
# Makefile for development and build tasks

.PHONY: build clean test run-node run-node2 run-node3 run-example deps tidy fmt vet lint clear-ports install-hooks kill

VERSION := 0.60.1
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

# Install git hooks
install-hooks:
	@echo "Installing git hooks..."
	@bash scripts/install-hooks.sh

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
# Usage: make run-node2 JOINADDR=/ip4/localhost/tcp/5001 HTTP=5002 RAFT=7002 P2P=4002
run-node2:
	@echo "Starting regular node (node.yaml)..."
	@echo "Config: ~/.debros/node.yaml"
	@echo "Generate it with: network-cli config init --type node --join localhost:5001 --bootstrap-peers '<peer_multiaddr>'"
	go run ./cmd/node --config node2.yaml

# Run third node (regular) - requires join address of bootstrap node
# Usage: make run-node3 JOINADDR=/ip4/localhost/tcp/5001 HTTP=5003 RAFT=7003 P2P=4003
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

# Development environment target
# Uses network-cli dev up to start full stack with dependency and port checking
dev: build
	@./bin/network-cli dev up

# Kill all processes using network-cli dev down
kill:
	@./bin/network-cli dev down

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build all executables"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run tests"
	@echo ""
	@echo "Local Development (Recommended):"
	@echo "  make dev      - Start full development stack with one command"
	@echo "                 - Checks dependencies and available ports"
	@echo "                 - Generates configs (bootstrap + node2 + node3 + gateway)"
	@echo "                 - Starts IPFS, RQLite, Olric, nodes, and gateway"
	@echo "                 - Validates cluster health (IPFS peers, RQLite, LibP2P)"
	@echo "                 - Stops all services if health checks fail"
	@echo "                 - Includes comprehensive logging"
	@echo "  make kill     - Stop all development services"
	@echo ""
	@echo "Development Management (via network-cli):"
	@echo "  ./bin/network-cli dev status  - Show status of all dev services"
	@echo "  ./bin/network-cli dev logs <component> [--follow]"
	@echo ""
	@echo "Individual Node Targets (advanced):"
	@echo "  run-node      - Start bootstrap node directly"
	@echo "  run-node2     - Start second node directly"
	@echo "  run-node3     - Start third node directly"
	@echo "  run-gateway   - Start HTTP gateway directly"
	@echo ""
	@echo "Maintenance:"
	@echo "  deps          - Download dependencies"
	@echo "  tidy          - Tidy dependencies"
	@echo "  fmt           - Format code"
	@echo "  vet           - Vet code"
	@echo "  lint          - Lint code (fmt + vet)"
	@echo "  help          - Show this help"
