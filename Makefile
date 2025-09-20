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

VERSION := 0.43.7-beta
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

# Build targets
build: deps
	@echo "Building network executables (version=$(VERSION))..."
	@mkdir -p bin
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
	@echo "Starting bootstrap node with config..."
	go run ./cmd/node --config configs/bootstrap.yaml

# Run second node (regular) - requires join address of bootstrap node
# Usage: make run-node2 JOINADDR=/ip4/127.0.0.1/tcp/5001 HTTP=5002 RAFT=7002 P2P=4002
run-node2:
	@echo "Starting regular node2 with config..."
	go run ./cmd/node --config configs/node.yaml

# Run third node (regular) - requires join address of bootstrap node
# Usage: make run-node3 JOINADDR=/ip4/127.0.0.1/tcp/5001 HTTP=5003 RAFT=7003 P2P=4003
run-node3:
	@echo "Starting regular node3 with config..."
	go run ./cmd/node --config configs/node.yaml

# Run gateway HTTP server
# Usage examples:
#   make run-gateway                                   # uses defaults (:8080, namespace=default)
#   GATEWAY_ADDR=":8081" make run-gateway              # override listen addr via env
#   GATEWAY_NAMESPACE=myapp make run-gateway           # set namespace
#   GATEWAY_BOOTSTRAP_PEERS="/ip4/127.0.0.1/tcp/4001/p2p/<ID>" make run-gateway
#   GATEWAY_REQUIRE_AUTH=1 GATEWAY_API_KEYS="key1:ns1,key2:ns2" make run-gateway
run-gateway:
	@echo "Starting gateway HTTP server..."
	GATEWAY_ADDR=$(or $(ADDR),$(GATEWAY_ADDR)) \
	GATEWAY_NAMESPACE=$(or $(NAMESPACE),$(GATEWAY_NAMESPACE)) \
	GATEWAY_BOOTSTRAP_PEERS=$(GATEWAY_BOOTSTRAP_PEERS) \
	GATEWAY_REQUIRE_AUTH=$(GATEWAY_REQUIRE_AUTH) \
	GATEWAY_API_KEYS=$(GATEWAY_API_KEYS) \
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
	@echo "To start a development cluster, run these commands in separate terminals:"
	@echo "1. make run-node           # Start bootstrap node (uses configs/bootstrap.yaml)"
	@echo "2. make run-node2          # Start second node (uses configs/node.yaml)"
	@echo "3. make run-node3          # Start third node (uses configs/node.yaml)"
	@echo "4. make run-example        # Test basic functionality"
	@echo "5. make cli-health         # Check network health"
	@echo "6. make cli-peers          # List peers"
	@echo "7. make cli-storage-test   # Test storage"
	@echo "8. make cli-pubsub-test    # Test messaging"

# Full development workflow
dev: clean build test
	@echo "Development workflow complete!"

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build all executables"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run tests"
	@echo "  run-node      - Start bootstrap node"
	@echo "  run-node2     - Start second node (requires JOINADDR, optional HTTP/RAFT/P2P)"
	@echo "  run-node3     - Start third node (requires JOINADDR, optional HTTP/RAFT/P2P)"
	@echo "  run-gateway   - Start HTTP gateway (flags via env: GATEWAY_ADDR, GATEWAY_NAMESPACE, GATEWAY_BOOTSTRAP_PEERS, GATEWAY_REQUIRE_AUTH, GATEWAY_API_KEYS)"
	@echo "  run-example   - Run usage example"
	@echo "  run-cli       - Run network CLI help"
	@echo "  show-bootstrap - Show example bootstrap usage with flags"
	@echo "  cli-health    - Check network health"
	@echo "  cli-peers     - List network peers"
	@echo "  cli-status    - Get network status"
	@echo "  cli-storage-test - Test storage operations"
	@echo "  cli-pubsub-test  - Test pub/sub operations"
	@echo "  test-multinode   - Full multi-node test with 1 bootstrap + 2 nodes"
	@echo "  test-peer-discovery - Test peer discovery (requires running nodes)"
	@echo "  test-replication - Test data replication (requires running nodes)"
	@echo "  test-consensus   - Test database consensus (requires running nodes)"
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
