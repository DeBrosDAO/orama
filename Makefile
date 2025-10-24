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

VERSION := 0.51.6-beta
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

# Run basic usage example
run-example:
	@echo "Running basic usage example..."
	go run examples/basic_usage.go

# Show how to run with flags
show-bootstrap:
	@echo "Provide join address via flags, e.g.:"
	@echo "  make run-node2 JOINADDR=/ip4/127.0.0.1/tcp/5001 HTTP=5002 RAFT=7002 P2P=4002"

# Run network CLI
run-cli:
	@echo "Running network CLI help..."
	./bin/network-cli help

# Network CLI helper commands
cli-health:
	@echo "Checking network health..."
	./bin/network-cli health

cli-peers:
	@echo "Listing network peers..."
	./bin/network-cli peers

cli-status:
	@echo "Getting network status..."
	./bin/network-cli status

cli-storage-test:
	@echo "Testing storage operations..."
	@./bin/network-cli storage put test-key "Hello Network" || echo "Storage test requires running network"
	@./bin/network-cli storage get test-key || echo "Storage test requires running network"
	@./bin/network-cli storage list || echo "Storage test requires running network"

cli-pubsub-test:
	@echo "Testing pub/sub operations..."
	@./bin/network-cli pubsub publish test-topic "Hello World" || echo "PubSub test requires running network"
	@./bin/network-cli pubsub topics || echo "PubSub test requires running network"

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	go mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Vet code
vet:
	@echo "Vetting code..."
	go vet ./...

# Lint alias (lightweight for now)
lint: fmt vet
	@echo "Linting complete (fmt + vet)"

# Clear common development ports
clear-ports:
	@echo "Clearing common dev ports (4001/4002, 5001/5002, 7001/7002)..."
	@chmod +x scripts/clear-ports.sh || true
	@scripts/clear-ports.sh

# Development setup
dev-setup: deps
	@echo "Setting up development environment..."
	@mkdir -p data/bootstrap data/node data/node2 data/node3
	@mkdir -p data/test-bootstrap data/test-node1 data/test-node2
	@echo "Development setup complete!"

# Start development cluster (requires multiple terminals)
dev-cluster:
	@echo "To start a development cluster with 3 nodes:"
	@echo ""
	@echo "1. Generate config files in ~/.debros:"
	@echo "   make build"
	@echo "   ./bin/network-cli config init --type bootstrap"
	@echo "   ./bin/network-cli config init --type node --name node.yaml --bootstrap-peers '<bootstrap_peer_multiaddr>'"
	@echo "   ./bin/network-cli config init --type node --name node2.yaml --bootstrap-peers '<bootstrap_peer_multiaddr>'"
	@echo ""
	@echo "2. Run in separate terminals:"
	@echo "   Terminal 1: make run-node           # Start bootstrap node (bootstrap.yaml)"
	@echo "   Terminal 2: make run-node2          # Start node 1 (node.yaml)"
	@echo "   Terminal 3: make run-node3          # Start node 2 (node2.yaml)"
	@echo "   Terminal 4: make run-gateway        # Start gateway"
	@echo ""
	@echo "3. Or run custom node with any config file:"
	@echo "   go run ./cmd/node --config custom-node.yaml"
	@echo ""
	@echo "4. Test:"
	@echo "   make cli-health                     # Check network health"
	@echo "   make cli-peers                      # List peers"
	@echo "   make cli-storage-test               # Test storage"
	@echo "   make cli-pubsub-test                # Test messaging"

# Full development workflow
dev: clean build test
	@echo "Development workflow complete!"

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build all executables"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run tests"
	@echo ""
	@echo "Configuration (NEW):"
	@echo "  First, generate config files in ~/.debros with:"
	@echo "    make build                                         # Build CLI first"
	@echo "    ./bin/network-cli config init --type bootstrap     # Generate bootstrap config"
	@echo "    ./bin/network-cli config init --type node --bootstrap-peers '<peer_multiaddr>'"
	@echo "    ./bin/network-cli config init --type gateway"
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
