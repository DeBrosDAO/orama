TEST?=./...

.PHONY: test
test:
	@echo Running tests...
	go test -v $(TEST)

# Gateway-focused E2E tests assume gateway and nodes are already running
# Auto-discovers configuration from ~/.orama and queries database for API key
# No environment variables required
.PHONY: test-e2e test-e2e-deployments test-e2e-fullstack test-e2e-https test-e2e-quick test-e2e-prod test-e2e-shared test-e2e-cluster test-e2e-integration test-e2e-production

# Production E2E tests - includes production-only tests
test-e2e-prod:
	@if [ -z "$$ORAMA_GATEWAY_URL" ]; then \
		echo "❌ ORAMA_GATEWAY_URL not set"; \
		echo "Usage: ORAMA_GATEWAY_URL=https://dbrs.space make test-e2e-prod"; \
		exit 1; \
	fi
	@echo "Running E2E tests (including production-only) against $$ORAMA_GATEWAY_URL..."
	go test -v -tags "e2e production" -timeout 30m ./e2e/...

# Generic e2e target
test-e2e:
	@echo "Running comprehensive E2E tests..."
	@echo "Auto-discovering configuration from ~/.orama..."
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

.PHONY: build clean test deps tidy fmt vet lint install-hooks upload-devnet upload-testnet redeploy-devnet redeploy-testnet release health

VERSION := 0.112.5
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'
LDFLAGS_LINUX := -s -w $(LDFLAGS)

# Build targets
build: deps
	@echo "Building network executables (version=$(VERSION))..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/identity ./cmd/identity
	go build -ldflags "$(LDFLAGS)" -o bin/orama-node ./cmd/node
	go build -ldflags "$(LDFLAGS)" -o bin/orama ./cmd/cli/
	# Inject gateway build metadata via pkg path variables
	go build -ldflags "$(LDFLAGS) -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildVersion=$(VERSION)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildCommit=$(COMMIT)' -X 'github.com/DeBrosOfficial/network/pkg/gateway.BuildTime=$(DATE)'" -o bin/gateway ./cmd/gateway
	go build -ldflags "$(LDFLAGS)" -o bin/sfu ./cmd/sfu
	go build -ldflags "$(LDFLAGS)" -o bin/turn ./cmd/turn
	@echo "Build complete! Run ./bin/orama version"

# Cross-compile CLI for Linux (only binary needed locally; VPS builds everything else from source)
build-linux: deps
	@echo "Cross-compiling CLI for linux/amd64 (version=$(VERSION))..."
	@mkdir -p bin-linux
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS_LINUX)" -trimpath -o bin-linux/orama ./cmd/cli/
	@echo "✓ CLI built at bin-linux/orama"
	@echo ""
	@echo "Next steps:"
	@echo "  ./scripts/generate-source-archive.sh"
	@echo "  ./bin/orama install --vps-ip <ip> --nameserver --domain ..."

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

# Upload source to devnet using fanout (upload to 1 node, parallel distribute to rest)
upload-devnet:
	@bash scripts/upload-source-fanout.sh --env devnet

# Upload source to testnet using fanout
upload-testnet:
	@bash scripts/upload-source-fanout.sh --env testnet

# Deploy to devnet (build + rolling upgrade all nodes)
redeploy-devnet:
	@bash scripts/redeploy.sh --devnet

# Deploy to devnet without rebuilding
redeploy-devnet-quick:
	@bash scripts/redeploy.sh --devnet --no-build

# Deploy to testnet (build + rolling upgrade all nodes)
redeploy-testnet:
	@bash scripts/redeploy.sh --testnet

# Deploy to testnet without rebuilding
redeploy-testnet-quick:
	@bash scripts/redeploy.sh --testnet --no-build

# Interactive release workflow (tag + push)
release:
	@bash scripts/release.sh

# Check health of all nodes in an environment
# Usage: make health ENV=devnet
health:
	@if [ -z "$(ENV)" ]; then \
		echo "Usage: make health ENV=devnet|testnet"; \
		exit 1; \
	fi
	@while IFS='|' read -r env host pass role key; do \
		[ -z "$$env" ] && continue; \
		case "$$env" in \#*) continue;; esac; \
		env="$$(echo "$$env" | xargs)"; \
		[ "$$env" != "$(ENV)" ] && continue; \
		role="$$(echo "$$role" | xargs)"; \
		bash scripts/check-node-health.sh "$$host" "$$pass" "$$host ($$role)"; \
	done < scripts/remote-nodes.conf

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build all executables"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run unit tests"
	@echo ""
	@echo "E2E Testing:"
	@echo "  make test-e2e-prod        - Run all E2E tests incl. production-only (needs ORAMA_GATEWAY_URL)"
	@echo "  make test-e2e-shared      - Run shared E2E tests (cache, storage, pubsub, auth)"
	@echo "  make test-e2e-cluster     - Run cluster E2E tests (libp2p, olric, rqlite, namespace)"
	@echo "  make test-e2e-integration - Run integration E2E tests (fullstack, persistence, concurrency)"
	@echo "  make test-e2e-deployments - Run deployment E2E tests"
	@echo "  make test-e2e-production  - Run production-only E2E tests (DNS, HTTPS, cross-node)"
	@echo "  make test-e2e-quick       - Quick smoke tests (static deploys, health checks)"
	@echo "  make test-e2e             - Generic E2E tests (auto-discovers config)"
	@echo ""
	@echo "  Example:"
	@echo "    ORAMA_GATEWAY_URL=https://orama-devnet.network make test-e2e-prod"
	@echo ""
	@echo "Deployment:"
	@echo "  make redeploy-devnet       - Build + rolling deploy to all devnet nodes"
	@echo "  make redeploy-devnet-quick - Deploy to devnet without rebuilding"
	@echo "  make redeploy-testnet      - Build + rolling deploy to all testnet nodes"
	@echo "  make redeploy-testnet-quick- Deploy to testnet without rebuilding"
	@echo "  make health ENV=devnet     - Check health of all nodes in an environment"
	@echo "  make release               - Interactive release workflow (tag + push)"
	@echo ""
	@echo "Maintenance:"
	@echo "  deps          - Download dependencies"
	@echo "  tidy          - Tidy dependencies"
	@echo "  fmt           - Format code"
	@echo "  vet           - Vet code"
	@echo "  lint          - Lint code (fmt + vet)"
	@echo "  help          - Show this help"
