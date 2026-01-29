TEST?=./...

.PHONY: test
test:
	@echo Running tests...
	go test -v $(TEST)

# Gateway-focused E2E tests assume gateway and nodes are already running
# Auto-discovers configuration from ~/.orama and queries database for API key
# No environment variables required
.PHONY: test-e2e test-e2e-deployments test-e2e-fullstack test-e2e-https test-e2e-quick test-e2e-local test-e2e-prod test-e2e-shared test-e2e-cluster test-e2e-integration test-e2e-production

# Check if gateway is running (helper)
.PHONY: check-gateway
check-gateway:
	@if ! curl -sf http://localhost:6001/v1/health > /dev/null 2>&1; then \
		echo "❌ Gateway not running on localhost:6001"; \
		echo ""; \
		echo "To run tests locally:"; \
		echo "  1. Start the dev environment: make dev"; \
		echo "  2. Wait for all services to start (~30 seconds)"; \
		echo "  3. Run tests: make test-e2e-local"; \
		echo ""; \
		echo "To run tests against production:"; \
		echo "  ORAMA_GATEWAY_URL=http://VPS-IP:6001 make test-e2e"; \
		exit 1; \
	fi
	@echo "✅ Gateway is running"

# Local E2E tests - checks gateway first
test-e2e-local: check-gateway
	@echo "Running E2E tests against local dev environment..."
	go test -v -tags e2e -timeout 30m ./e2e/...

# Production E2E tests - includes production-only tests
test-e2e-prod:
	@if [ -z "$$ORAMA_GATEWAY_URL" ]; then \
		echo "❌ ORAMA_GATEWAY_URL not set"; \
		echo "Usage: ORAMA_GATEWAY_URL=http://VPS-IP:6001 make test-e2e-prod"; \
		exit 1; \
	fi
	@echo "Running E2E tests (including production-only) against $$ORAMA_GATEWAY_URL..."
	go test -v -tags "e2e production" -timeout 30m ./e2e/...

# Generic e2e target (works with both local and production)
test-e2e:
	@echo "Running comprehensive E2E tests..."
	@echo "Auto-discovering configuration from ~/.orama..."
	@echo "Tip: Use 'make test-e2e-local' for local or 'make test-e2e-prod' for production"
	go test -v -tags e2e -timeout 30m ./e2e/...

test-e2e-deployments:
	@echo "Running deployment E2E tests..."
	go test -v -tags e2e -timeout 15m ./e2e/deployments/...

test-e2e-fullstack:
	@echo "Running fullstack E2E tests..."
	go test -v -tags e2e -timeout 20m -run "TestFullStack" ./e2e/...

test-e2e-https:
	@echo "Running HTTPS/external access E2E tests..."
	go test -v -tags e2e -timeout 10m -run "TestHTTPS" ./e2e/...

test-e2e-shared:
	@echo "Running shared E2E tests..."
	go test -v -tags e2e -timeout 10m ./e2e/shared/...

test-e2e-cluster:
	@echo "Running cluster E2E tests..."
	go test -v -tags e2e -timeout 15m ./e2e/cluster/...

test-e2e-integration:
	@echo "Running integration E2E tests..."
	go test -v -tags e2e -timeout 20m ./e2e/integration/...

test-e2e-production:
	@echo "Running production-only E2E tests..."
	go test -v -tags "e2e production" -timeout 15m ./e2e/production/...

test-e2e-quick:
	@echo "Running quick E2E smoke tests..."
	go test -v -tags e2e -timeout 5m -run "TestStatic|TestHealth" ./e2e/...

# Network - Distributed P2P Database System
# Makefile for development and build tasks

.PHONY: build clean test run-node run-node2 run-node3 run-example deps tidy fmt vet lint clear-ports install-hooks kill

VERSION := 0.100.0
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

# Build targets
build: deps
	@echo "Building network executables (version=$(VERSION))..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/identity ./cmd/identity
	go build -ldflags "$(LDFLAGS)" -o bin/orama-node ./cmd/node
	go build -ldflags "$(LDFLAGS)" -o bin/orama cmd/cli/main.go
	go build -ldflags "$(LDFLAGS)" -o bin/rqlite-mcp ./cmd/rqlite-mcp
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
	go run ./cmd/orama-node --config node.yaml

# Run second node - requires join address
run-node2:
	@echo "Starting second node..."
	@echo "Config: ~/.orama/node2.yaml"
	go run ./cmd/orama-node --config node2.yaml

# Run third node - requires join address
run-node3:
	@echo "Starting third node..."
	@echo "Config: ~/.orama/node3.yaml"
	go run ./cmd/orama-node --config node3.yaml

# Run gateway HTTP server
run-gateway:
	@echo "Starting gateway HTTP server..."
	@echo "Note: Config must be in ~/.orama/data/gateway.yaml"
	go run ./cmd/orama-gateway

# Development environment target
# Uses orama dev up to start full stack with dependency and port checking
dev: build
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
	@echo "  test          - Run unit tests"
	@echo ""
	@echo "Local Development (Recommended):"
	@echo "  make dev      - Start full development stack with one command"
	@echo "                 - Checks dependencies and available ports"
	@echo "                 - Generates configs and starts all services"
	@echo "                 - Validates cluster health"
	@echo "  make stop     - Gracefully stop all development services"
	@echo "  make kill     - Force kill all development services (use if stop fails)"
	@echo ""
	@echo "E2E Testing:"
	@echo "  make test-e2e-local       - Run E2E tests against local dev (checks gateway first)"
	@echo "  make test-e2e-prod        - Run all E2E tests incl. production-only (needs ORAMA_GATEWAY_URL)"
	@echo "  make test-e2e-shared      - Run shared E2E tests (cache, storage, pubsub, auth)"
	@echo "  make test-e2e-cluster     - Run cluster E2E tests (libp2p, olric, rqlite, namespace)"
	@echo "  make test-e2e-integration - Run integration E2E tests (fullstack, persistence, concurrency)"
	@echo "  make test-e2e-deployments - Run deployment E2E tests"
	@echo "  make test-e2e-production  - Run production-only E2E tests (DNS, HTTPS, cross-node)"
	@echo "  make test-e2e-quick       - Quick smoke tests (static deploys, health checks)"
	@echo "  make test-e2e             - Generic E2E tests (auto-discovers config)"
	@echo ""
	@echo "  Example production test:"
	@echo "    ORAMA_GATEWAY_URL=http://141.227.165.168:6001 make test-e2e-prod"
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
