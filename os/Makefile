SHELL := /bin/bash
.PHONY: agent build sign test clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
ARCH ?= amd64

# Directories
AGENT_DIR := agent
BUILDROOT_DIR := buildroot
SCRIPTS_DIR := scripts
OUTPUT_DIR := output

# --- Agent ---

agent:
	@echo "=== Building orama-agent ==="
	cd $(AGENT_DIR) && GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 \
		go build -ldflags "-s -w" -o ../$(OUTPUT_DIR)/orama-agent ./cmd/orama-agent/
	@echo "Built: $(OUTPUT_DIR)/orama-agent"

agent-test:
	@echo "=== Testing orama-agent ==="
	cd $(AGENT_DIR) && go test ./...

# --- Full Image Build ---

build: agent
	@echo "=== Building OramaOS image ==="
	ORAMA_VERSION=$(VERSION) ARCH=$(ARCH) $(SCRIPTS_DIR)/build.sh
	@echo "Build complete: $(OUTPUT_DIR)/"

# --- Signing ---

sign:
	@echo "=== Signing OramaOS image ==="
	$(SCRIPTS_DIR)/sign.sh $(OUTPUT_DIR)/orama-os-$(VERSION)-$(ARCH)

# --- QEMU Testing ---

test: build
	@echo "=== Launching QEMU test VM ==="
	$(SCRIPTS_DIR)/test-vm.sh $(OUTPUT_DIR)/orama-os.qcow2

test-vm:
	@echo "=== Launching QEMU with existing image ==="
	$(SCRIPTS_DIR)/test-vm.sh $(OUTPUT_DIR)/orama-os.qcow2

# --- Clean ---

clean:
	rm -rf $(OUTPUT_DIR)
	@echo "Cleaned output directory"
