TEST?=./...

.PHONY: test
test:
	@echo Running tests...
	go test -v $(TEST)

# Gateway-focused E2E tests assume gateway and nodes are already running
# Auto-discovers configuration from ~/.orama and queries database for API key
# No environment variables required
.PHONY: test-e2e
test-e2e:
	@echo "Running comprehensive E2E tests..."
	@echo "Auto-discovering configuration from ~/.orama..."
	go test -v -tags e2e ./e2e

# Network - Distributed P2P Database System
# Makefile for development and build tasks

.PHONY: build clean test run-node run-node2 run-node3 run-example deps tidy fmt vet lint clear-ports install-hooks kill

VERSION := 0.70.0
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

# Build targets
build: deps
	@echo "Building network executables (version=$(VERSION))..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/identity ./cmd/identity
	go build -ldflags "$(LDFLAGS)" -o bin/node ./cmd/node
	go build -ldflags "$(LDFLAGS)" -o bin/orama cmd/cli/main.go
	# Inject gateway build metadata via pkg path variables
	go build -ldflags "$(LDFLAGS) -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildVersion=$(VERSION)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildCommit=$(COMMIT)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildTime=$(DATE)'" -o bin/gateway ./cmd/gateway
	@echo "Build complete! Run ./bin/orama version"

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
	@echo "Starting node..."
	@echo "Config: ~/.orama/node.yaml"
	go run ./cmd/node --config node.yaml

# Run second node - requires join address
run-node2:
	@echo "Starting second node..."
	@echo "Config: ~/.orama/node2.yaml"
	go run ./cmd/node --config node2.yaml

# Run third node - requires join address
run-node3:
	@echo "Starting third node..."
	@echo "Config: ~/.orama/node3.yaml"
	go run ./cmd/node --config node3.yaml

# Run gateway HTTP server
run-gateway:
	@echo "Starting gateway HTTP server..."
	@echo "Note: Config must be in ~/.orama/data/gateway.yaml"
	go run ./cmd/gateway

# Setup local domain names for development
setup-domains:
	@echo "Setting up local domains..."
	@sudo bash scripts/setup-local-domains.sh

# Development environment target
# Uses orama dev up to start full stack with dependency and port checking
dev: build setup-domains
	@./bin/orama dev up

# Graceful shutdown of all dev services
stop:
	@if [ -f ./bin/orama ]; then \
		./bin/orama dev down || true; \
	fi
	@bash scripts/dev-kill-all.sh

# Force kill all processes (immediate termination)
kill:
	@bash scripts/dev-kill-all.sh

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
	@echo "                 - Generates configs and starts all services"
	@echo "                 - Validates cluster health"
	@echo "  make stop     - Gracefully stop all development services"
	@echo "  make kill     - Force kill all development services (use if stop fails)"
	@echo ""
	@echo "Development Management (via orama):"
	@echo "  ./bin/orama dev status  - Show status of all dev services"
	@echo "  ./bin/orama dev logs <component> [--follow]"
	@echo ""
	@echo "Individual Node Targets (advanced):"
	@echo "  run-node      - Start first node directly"
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
