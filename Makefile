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

.PHONY: build clean test run-node run-node2 run-node3 run-example deps tidy fmt vet lint clear-ports install-hooks kill

VERSION := 0.56.0
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
	@echo "Initializing IPFS and Cluster for all nodes..."
	@if command -v ipfs >/dev/null 2>&1 && command -v ipfs-cluster-service >/dev/null 2>&1; then \
		CLUSTER_SECRET=$$HOME/.debros/cluster-secret; \
		if [ ! -f $$CLUSTER_SECRET ]; then \
			echo "  Generating shared cluster secret..."; \
			ipfs-cluster-service --version >/dev/null 2>&1 && openssl rand -hex 32 > $$CLUSTER_SECRET || echo "0000000000000000000000000000000000000000000000000000000000000000" > $$CLUSTER_SECRET; \
		fi; \
		SECRET=$$(cat $$CLUSTER_SECRET); \
		echo "  Setting up bootstrap node (IPFS: 5001, Cluster: 9094)..."; \
		if [ ! -d $$HOME/.debros/bootstrap/ipfs/repo ]; then \
			echo "    Initializing IPFS..."; \
			mkdir -p $$HOME/.debros/bootstrap/ipfs; \
			IPFS_PATH=$$HOME/.debros/bootstrap/ipfs/repo ipfs init --profile=server 2>&1 | grep -v "generating" | grep -v "peer identity" || true; \
			IPFS_PATH=$$HOME/.debros/bootstrap/ipfs/repo ipfs config --json Addresses.API '["/ip4/127.0.0.1/tcp/5001"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/bootstrap/ipfs/repo ipfs config --json Addresses.Gateway '["/ip4/127.0.0.1/tcp/8080"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/bootstrap/ipfs/repo ipfs config --json Addresses.Swarm '["/ip4/0.0.0.0/tcp/4001","/ip6/::/tcp/4001"]' 2>&1 | grep -v "generating" || true; \
		fi; \
		echo "    Initializing IPFS Cluster..."; \
		mkdir -p $$HOME/.debros/bootstrap/ipfs-cluster; \
		env IPFS_CLUSTER_PATH=$$HOME/.debros/bootstrap/ipfs-cluster ipfs-cluster-service init --force >/dev/null 2>&1 || true; \
		jq '.cluster.peername = "bootstrap" | .cluster.secret = "'$$SECRET'" | .cluster.listen_multiaddress = ["/ip4/0.0.0.0/tcp/9096"] | .consensus.crdt.cluster_name = "debros-cluster" | .consensus.crdt.trusted_peers = ["*"] | .api.restapi.http_listen_multiaddress = "/ip4/0.0.0.0/tcp/9094" | .api.ipfsproxy.listen_multiaddress = "/ip4/127.0.0.1/tcp/9095" | .api.pinsvcapi.http_listen_multiaddress = "/ip4/127.0.0.1/tcp/9097" | .ipfs_connector.ipfshttp.node_multiaddress = "/ip4/127.0.0.1/tcp/5001"' $$HOME/.debros/bootstrap/ipfs-cluster/service.json > $$HOME/.debros/bootstrap/ipfs-cluster/service.json.tmp && mv $$HOME/.debros/bootstrap/ipfs-cluster/service.json.tmp $$HOME/.debros/bootstrap/ipfs-cluster/service.json; \
		echo "  Setting up node2 (IPFS: 5002, Cluster: 9104)..."; \
		if [ ! -d $$HOME/.debros/node2/ipfs/repo ]; then \
			echo "    Initializing IPFS..."; \
			mkdir -p $$HOME/.debros/node2/ipfs; \
			IPFS_PATH=$$HOME/.debros/node2/ipfs/repo ipfs init --profile=server 2>&1 | grep -v "generating" | grep -v "peer identity" || true; \
			IPFS_PATH=$$HOME/.debros/node2/ipfs/repo ipfs config --json Addresses.API '["/ip4/127.0.0.1/tcp/5002"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/node2/ipfs/repo ipfs config --json Addresses.Gateway '["/ip4/127.0.0.1/tcp/8081"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/node2/ipfs/repo ipfs config --json Addresses.Swarm '["/ip4/0.0.0.0/tcp/4002","/ip6/::/tcp/4002"]' 2>&1 | grep -v "generating" || true; \
		fi; \
		echo "    Initializing IPFS Cluster..."; \
		mkdir -p $$HOME/.debros/node2/ipfs-cluster; \
		env IPFS_CLUSTER_PATH=$$HOME/.debros/node2/ipfs-cluster ipfs-cluster-service init --force >/dev/null 2>&1 || true; \
		jq '.cluster.peername = "node2" | .cluster.secret = "'$$SECRET'" | .cluster.listen_multiaddress = ["/ip4/0.0.0.0/tcp/9106"] | .consensus.crdt.cluster_name = "debros-cluster" | .consensus.crdt.trusted_peers = ["*"] | .api.restapi.http_listen_multiaddress = "/ip4/0.0.0.0/tcp/9104" | .api.ipfsproxy.listen_multiaddress = "/ip4/127.0.0.1/tcp/9105" | .api.pinsvcapi.http_listen_multiaddress = "/ip4/127.0.0.1/tcp/9107" | .ipfs_connector.ipfshttp.node_multiaddress = "/ip4/127.0.0.1/tcp/5002"' $$HOME/.debros/node2/ipfs-cluster/service.json > $$HOME/.debros/node2/ipfs-cluster/service.json.tmp && mv $$HOME/.debros/node2/ipfs-cluster/service.json.tmp $$HOME/.debros/node2/ipfs-cluster/service.json; \
		echo "  Setting up node3 (IPFS: 5003, Cluster: 9114)..."; \
		if [ ! -d $$HOME/.debros/node3/ipfs/repo ]; then \
			echo "    Initializing IPFS..."; \
			mkdir -p $$HOME/.debros/node3/ipfs; \
			IPFS_PATH=$$HOME/.debros/node3/ipfs/repo ipfs init --profile=server 2>&1 | grep -v "generating" | grep -v "peer identity" || true; \
			IPFS_PATH=$$HOME/.debros/node3/ipfs/repo ipfs config --json Addresses.API '["/ip4/127.0.0.1/tcp/5003"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/node3/ipfs/repo ipfs config --json Addresses.Gateway '["/ip4/127.0.0.1/tcp/8082"]' 2>&1 | grep -v "generating" || true; \
			IPFS_PATH=$$HOME/.debros/node3/ipfs/repo ipfs config --json Addresses.Swarm '["/ip4/0.0.0.0/tcp/4003","/ip6/::/tcp/4003"]' 2>&1 | grep -v "generating" || true; \
		fi; \
		echo "    Initializing IPFS Cluster..."; \
		mkdir -p $$HOME/.debros/node3/ipfs-cluster; \
		env IPFS_CLUSTER_PATH=$$HOME/.debros/node3/ipfs-cluster ipfs-cluster-service init --force >/dev/null 2>&1 || true; \
		jq '.cluster.peername = "node3" | .cluster.secret = "'$$SECRET'" | .cluster.listen_multiaddress = ["/ip4/0.0.0.0/tcp/9116"] | .consensus.crdt.cluster_name = "debros-cluster" | .consensus.crdt.trusted_peers = ["*"] | .api.restapi.http_listen_multiaddress = "/ip4/0.0.0.0/tcp/9114" | .api.ipfsproxy.listen_multiaddress = "/ip4/127.0.0.1/tcp/9115" | .api.pinsvcapi.http_listen_multiaddress = "/ip4/127.0.0.1/tcp/9117" | .ipfs_connector.ipfshttp.node_multiaddress = "/ip4/127.0.0.1/tcp/5003"' $$HOME/.debros/node3/ipfs-cluster/service.json > $$HOME/.debros/node3/ipfs-cluster/service.json.tmp && mv $$HOME/.debros/node3/ipfs-cluster/service.json.tmp $$HOME/.debros/node3/ipfs-cluster/service.json; \
		echo "Starting IPFS daemons..."; \
		if [ ! -f .dev/pids/ipfs-bootstrap.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-bootstrap.pid) 2>/dev/null; then \
			IPFS_PATH=$$HOME/.debros/bootstrap/ipfs/repo nohup ipfs daemon --enable-pubsub-experiment > $$HOME/.debros/logs/ipfs-bootstrap.log 2>&1 & echo $$! > .dev/pids/ipfs-bootstrap.pid; \
			echo "  Bootstrap IPFS started (PID: $$(cat .dev/pids/ipfs-bootstrap.pid), API: 5001)"; \
			sleep 3; \
		else \
			echo "  ✓ Bootstrap IPFS already running"; \
		fi; \
		if [ ! -f .dev/pids/ipfs-node2.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-node2.pid) 2>/dev/null; then \
			IPFS_PATH=$$HOME/.debros/node2/ipfs/repo nohup ipfs daemon --enable-pubsub-experiment > $$HOME/.debros/logs/ipfs-node2.log 2>&1 & echo $$! > .dev/pids/ipfs-node2.pid; \
			echo "  Node2 IPFS started (PID: $$(cat .dev/pids/ipfs-node2.pid), API: 5002)"; \
			sleep 3; \
		else \
			echo "  ✓ Node2 IPFS already running"; \
		fi; \
		if [ ! -f .dev/pids/ipfs-node3.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-node3.pid) 2>/dev/null; then \
			IPFS_PATH=$$HOME/.debros/node3/ipfs/repo nohup ipfs daemon --enable-pubsub-experiment > $$HOME/.debros/logs/ipfs-node3.log 2>&1 & echo $$! > .dev/pids/ipfs-node3.pid; \
			echo "  Node3 IPFS started (PID: $$(cat .dev/pids/ipfs-node3.pid), API: 5003)"; \
			sleep 3; \
		else \
			echo "  ✓ Node3 IPFS already running"; \
		fi; \
		\
		echo "Starting IPFS Cluster peers..."; \
		if [ ! -f .dev/pids/ipfs-cluster-bootstrap.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-cluster-bootstrap.pid) 2>/dev/null; then \
			env IPFS_CLUSTER_PATH=$$HOME/.debros/bootstrap/ipfs-cluster nohup ipfs-cluster-service daemon > $$HOME/.debros/logs/ipfs-cluster-bootstrap.log 2>&1 & echo $$! > .dev/pids/ipfs-cluster-bootstrap.pid; \
			echo "  Bootstrap Cluster started (PID: $$(cat .dev/pids/ipfs-cluster-bootstrap.pid), API: 9094)"; \
			sleep 3; \
		else \
			echo "  ✓ Bootstrap Cluster already running"; \
		fi; \
		if [ ! -f .dev/pids/ipfs-cluster-node2.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-cluster-node2.pid) 2>/dev/null; then \
			env IPFS_CLUSTER_PATH=$$HOME/.debros/node2/ipfs-cluster nohup ipfs-cluster-service daemon > $$HOME/.debros/logs/ipfs-cluster-node2.log 2>&1 & echo $$! > .dev/pids/ipfs-cluster-node2.pid; \
			echo "  Node2 Cluster started (PID: $$(cat .dev/pids/ipfs-cluster-node2.pid), API: 9104)"; \
			sleep 3; \
		else \
			echo "  ✓ Node2 Cluster already running"; \
		fi; \
		if [ ! -f .dev/pids/ipfs-cluster-node3.pid ] || ! kill -0 $$(cat .dev/pids/ipfs-cluster-node3.pid) 2>/dev/null; then \
			env IPFS_CLUSTER_PATH=$$HOME/.debros/node3/ipfs-cluster nohup ipfs-cluster-service daemon > $$HOME/.debros/logs/ipfs-cluster-node3.log 2>&1 & echo $$! > .dev/pids/ipfs-cluster-node3.pid; \
			echo "  Node3 Cluster started (PID: $$(cat .dev/pids/ipfs-cluster-node3.pid), API: 9114)"; \
			sleep 3; \
		else \
			echo "  ✓ Node3 Cluster already running"; \
		fi; \
	else \
		echo "  ⚠️  ipfs or ipfs-cluster-service not found - skipping IPFS setup"; \
		echo "  Install with: https://docs.ipfs.tech/install/ and https://ipfscluster.io/documentation/guides/install/"; \
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
	@echo "Starting Olric cache server..."
	@if command -v olric-server >/dev/null 2>&1; then \
		if [ ! -f $$HOME/.debros/olric-config.yaml ]; then \
			echo "  Creating Olric config..."; \
			mkdir -p $$HOME/.debros; \
		fi; \
		if ! pgrep -f "olric-server" >/dev/null 2>&1; then \
			OLRIC_SERVER_CONFIG=$$HOME/.debros/olric-config.yaml nohup olric-server > $$HOME/.debros/logs/olric.log 2>&1 & echo $$! > .dev/pids/olric.pid; \
			echo "  Olric cache server started (PID: $$(cat .dev/pids/olric.pid))"; \
			sleep 3; \
		else \
			echo "  ✓ Olric cache server already running"; \
		fi; \
	else \
		echo "  ⚠️  olric-server command not found - skipping Olric (cache endpoints will be disabled)"; \
		echo "  Install with: go install github.com/olric-data/olric/cmd/olric-server@v0.7.0"; \
	fi
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
	@if [ -f .dev/pids/ipfs-bootstrap.pid ]; then \
		echo "  Bootstrap IPFS:      PID=$$(cat .dev/pids/ipfs-bootstrap.pid) (API: 5001)"; \
	fi
	@if [ -f .dev/pids/ipfs-node2.pid ]; then \
		echo "  Node2 IPFS:          PID=$$(cat .dev/pids/ipfs-node2.pid) (API: 5002)"; \
	fi
	@if [ -f .dev/pids/ipfs-node3.pid ]; then \
		echo "  Node3 IPFS:          PID=$$(cat .dev/pids/ipfs-node3.pid) (API: 5003)"; \
	fi
	@if [ -f .dev/pids/ipfs-cluster-bootstrap.pid ]; then \
		echo "  Bootstrap Cluster:  PID=$$(cat .dev/pids/ipfs-cluster-bootstrap.pid) (API: 9094)"; \
	fi
	@if [ -f .dev/pids/ipfs-cluster-node2.pid ]; then \
		echo "  Node2 Cluster:      PID=$$(cat .dev/pids/ipfs-cluster-node2.pid) (API: 9104)"; \
	fi
	@if [ -f .dev/pids/ipfs-cluster-node3.pid ]; then \
		echo "  Node3 Cluster:      PID=$$(cat .dev/pids/ipfs-cluster-node3.pid) (API: 9114)"; \
	fi
	@if [ -f .dev/pids/olric.pid ]; then \
		echo "  Olric:     PID=$$(cat .dev/pids/olric.pid) (API: 3320)"; \
	fi
	@echo "  Bootstrap: PID=$$(cat .dev/pids/bootstrap.pid)"
	@echo "  Node2:     PID=$$(cat .dev/pids/node2.pid)"
	@echo "  Node3:     PID=$$(cat .dev/pids/node3.pid)"
	@echo "  Gateway:   PID=$$(cat .dev/pids/gateway.pid)"
	@echo ""
	@echo "Ports:"
	@echo "  Anon SOCKS:    9050 (proxy endpoint: POST /v1/proxy/anon)"
	@if [ -f .dev/pids/ipfs-bootstrap.pid ]; then \
		echo "  Bootstrap IPFS API: 5001"; \
		echo "  Node2 IPFS API:     5002"; \
		echo "  Node3 IPFS API:     5003"; \
		echo "  Bootstrap Cluster: 9094 (pin management)"; \
		echo "  Node2 Cluster:     9104 (pin management)"; \
		echo "  Node3 Cluster:     9114 (pin management)"; \
	fi
	@if [ -f .dev/pids/olric.pid ]; then \
		echo "  Olric:         3320 (cache API)"; \
	fi
	@echo "  Bootstrap P2P: 4001, HTTP: 5001, Raft: 7001"
	@echo "  Node2     P2P: 4002, HTTP: 5002, Raft: 7002"
	@echo "  Node3     P2P: 4003, HTTP: 5003, Raft: 7003"
	@echo "  Gateway:       6001"
	@echo ""
	@echo "Press Ctrl+C to stop all processes"
	@echo "============================================================"
	@echo ""
	@LOGS="$$HOME/.debros/logs/bootstrap.log $$HOME/.debros/logs/node2.log $$HOME/.debros/logs/node3.log $$HOME/.debros/logs/gateway.log"; \
	if [ -f .dev/pids/anon.pid ]; then \
		LOGS="$$LOGS $$HOME/.debros/logs/anon.log"; \
	fi; \
	if [ -f .dev/pids/ipfs-bootstrap.pid ]; then \
		LOGS="$$LOGS $$HOME/.debros/logs/ipfs-bootstrap.log $$HOME/.debros/logs/ipfs-node2.log $$HOME/.debros/logs/ipfs-node3.log"; \
	fi; \
	if [ -f .dev/pids/ipfs-cluster-bootstrap.pid ]; then \
		LOGS="$$LOGS $$HOME/.debros/logs/ipfs-cluster-bootstrap.log $$HOME/.debros/logs/ipfs-cluster-node2.log $$HOME/.debros/logs/ipfs-cluster-node3.log"; \
	fi; \
	if [ -f .dev/pids/olric.pid ]; then \
		LOGS="$$LOGS $$HOME/.debros/logs/olric.log"; \
	fi; \
	trap 'echo "Stopping all processes..."; kill $$(cat .dev/pids/*.pid) 2>/dev/null; rm -f .dev/pids/*.pid; exit 0' INT; \
	tail -f $$LOGS

# Kill all processes
kill:
	@echo "🛑 Stopping all DeBros network services..."
	@echo ""
	@echo "Stopping DeBros nodes and gateway..."
	@if [ -f .dev/pids/gateway.pid ]; then \
		kill -TERM $$(cat .dev/pids/gateway.pid) 2>/dev/null && echo "  ✓ Gateway stopped" || echo "  ✗ Gateway not running"; \
		rm -f .dev/pids/gateway.pid; \
	fi
	@if [ -f .dev/pids/bootstrap.pid ]; then \
		kill -TERM $$(cat .dev/pids/bootstrap.pid) 2>/dev/null && echo "  ✓ Bootstrap node stopped" || echo "  ✗ Bootstrap not running"; \
		rm -f .dev/pids/bootstrap.pid; \
	fi
	@if [ -f .dev/pids/node2.pid ]; then \
		kill -TERM $$(cat .dev/pids/node2.pid) 2>/dev/null && echo "  ✓ Node2 stopped" || echo "  ✗ Node2 not running"; \
		rm -f .dev/pids/node2.pid; \
	fi
	@if [ -f .dev/pids/node3.pid ]; then \
		kill -TERM $$(cat .dev/pids/node3.pid) 2>/dev/null && echo "  ✓ Node3 stopped" || echo "  ✗ Node3 not running"; \
		rm -f .dev/pids/node3.pid; \
	fi
	@echo ""
	@echo "Stopping IPFS Cluster peers..."
	@if [ -f .dev/pids/ipfs-cluster-bootstrap.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-cluster-bootstrap.pid) 2>/dev/null && echo "  ✓ Bootstrap Cluster stopped" || echo "  ✗ Bootstrap Cluster not running"; \
		rm -f .dev/pids/ipfs-cluster-bootstrap.pid; \
	fi
	@if [ -f .dev/pids/ipfs-cluster-node2.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-cluster-node2.pid) 2>/dev/null && echo "  ✓ Node2 Cluster stopped" || echo "  ✗ Node2 Cluster not running"; \
		rm -f .dev/pids/ipfs-cluster-node2.pid; \
	fi
	@if [ -f .dev/pids/ipfs-cluster-node3.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-cluster-node3.pid) 2>/dev/null && echo "  ✓ Node3 Cluster stopped" || echo "  ✗ Node3 Cluster not running"; \
		rm -f .dev/pids/ipfs-cluster-node3.pid; \
	fi
	@echo ""
	@echo "Stopping IPFS daemons..."
	@if [ -f .dev/pids/ipfs-bootstrap.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-bootstrap.pid) 2>/dev/null && echo "  ✓ Bootstrap IPFS stopped" || echo "  ✗ Bootstrap IPFS not running"; \
		rm -f .dev/pids/ipfs-bootstrap.pid; \
	fi
	@if [ -f .dev/pids/ipfs-node2.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-node2.pid) 2>/dev/null && echo "  ✓ Node2 IPFS stopped" || echo "  ✗ Node2 IPFS not running"; \
		rm -f .dev/pids/ipfs-node2.pid; \
	fi
	@if [ -f .dev/pids/ipfs-node3.pid ]; then \
		kill -TERM $$(cat .dev/pids/ipfs-node3.pid) 2>/dev/null && echo "  ✓ Node3 IPFS stopped" || echo "  ✗ Node3 IPFS not running"; \
		rm -f .dev/pids/ipfs-node3.pid; \
	fi
	@echo ""
	@echo "Stopping Olric cache..."
	@if [ -f .dev/pids/olric.pid ]; then \
		kill -TERM $$(cat .dev/pids/olric.pid) 2>/dev/null && echo "  ✓ Olric stopped" || echo "  ✗ Olric not running"; \
		rm -f .dev/pids/olric.pid; \
	fi
	@echo ""
	@echo "Stopping Anon proxy..."
	@if [ -f .dev/pids/anyone.pid ]; then \
		kill -TERM $$(cat .dev/pids/anyone.pid) 2>/dev/null && echo "  ✓ Anon proxy stopped" || echo "  ✗ Anon proxy not running"; \
		rm -f .dev/pids/anyone.pid; \
	fi
	@echo ""
	@echo "Cleaning up any remaining processes on ports..."
	@lsof -ti:7001,7002,7003,5001,5002,5003,6001,4001,4002,4003,9050,3320,3322,9094,9095,9096,9097,9104,9105,9106,9107,9114,9115,9116,9117,8080,8081,8082 2>/dev/null | xargs kill -9 2>/dev/null && echo "  ✓ Cleaned up remaining port bindings" || echo "  ✓ No lingering processes found"
	@echo ""
	@echo "✅ All services stopped!"

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
	@echo "  kill          - Stop all running services (nodes, IPFS, cluster, gateway, olric)"
	@echo "  dev-setup     - Setup development environment"
	@echo "  dev-cluster   - Show cluster startup commands"
	@echo "  dev           - Full development workflow"
	@echo "  help          - Show this help"
