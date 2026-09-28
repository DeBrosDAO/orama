# Makefile for the Orama L1 chain module (github.com/DeBrosOfficial/network/chain).
#
# This module is self-contained: it never touches core/go.mod, and core/ never imports it (see
# plans/open-network.md, "Repository and component boundaries").

BUILD_DIR := build
BINARY := oramad
PKG := ./cmd/oramad

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
VERSION_PKG := github.com/cosmos/cosmos-sdk/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Name=oramad \
	-X $(VERSION_PKG).AppName=oramad \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT)
BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"

# Static cross-compiles pass -tags nowasm. They do not link libwasmvm and do not include CosmWasm.
# The default host build (build-wasm, and `go test` without -tags nowasm when cgo is on) links
# github.com/CosmWasm/wasmvm/v3 v3.0.7's libwasmvm when that library is present.
WASMVM_DIR := $(shell go list -m -f '{{.Dir}}' github.com/CosmWasm/wasmvm/v3 2>/dev/null)
LIBWASMVM := $(firstword $(wildcard $(WASMVM_DIR)/internal/api/libwasmvm.dylib $(WASMVM_DIR)/internal/api/libwasmvm.aarch64.so $(WASMVM_DIR)/internal/api/libwasmvm.x86_64.so))

.PHONY: build build-wasm test chain-test lint chain-sim localnet localnet-stop localnet-clean

## build: cross-compile oramad for linux/amd64, darwin/amd64 and darwin/arm64.
## CGO_ENABLED=0 and -tags nowasm: these binaries are static and do not include CosmWasm.
## A nowasm binary refuses to start a node that claims the wasm module.
## -trimpath drops local filesystem paths from the binary; the version ldflags embed `git describe`
## and the short commit hash, so `oramad version` reports something meaningful.
build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -tags nowasm $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -tags nowasm $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags nowasm $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(PKG)

## build-wasm: host oramad with cgo and libwasmvm. Fails with the install command when the
## library is not in the wasmvm module cache.
build-wasm:
	@test -n "$(LIBWASMVM)" || { \
		echo "libwasmvm is not installed."; \
		echo "darwin: curl -fsSL -o \"$$(go env GOPATH)/pkg/mod/github.com/!cosm!wasm/wasmvm/v3@v3.0.7/internal/api/libwasmvm.dylib\" https://github.com/CosmWasm/wasmvm/releases/download/v3.0.7/libwasmvm.dylib"; \
		echo "linux aarch64: curl -fsSL -o \"$$(go env GOPATH)/pkg/mod/github.com/!cosm!wasm/wasmvm/v3@v3.0.7/internal/api/libwasmvm.aarch64.so\" https://github.com/CosmWasm/wasmvm/releases/download/v3.0.7/libwasmvm.aarch64.so"; \
		echo "linux x86_64: curl -fsSL -o \"$$(go env GOPATH)/pkg/mod/github.com/!cosm!wasm/wasmvm/v3@v3.0.7/internal/api/libwasmvm.x86_64.so\" https://github.com/CosmWasm/wasmvm/releases/download/v3.0.7/libwasmvm.x86_64.so"; \
		exit 1; \
	}
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY) $(PKG)

## test: pure-Go tests on the nowasm build, which does not need libwasmvm.
## When libwasmvm is present, also run the cgo build (wasm keeper, contract packet refusal).
test:
	CGO_ENABLED=0 go test -tags nowasm ./...
	@if [ -n "$(LIBWASMVM)" ]; then CGO_ENABLED=1 go test ./...; fi

## chain-test: alias for test, matching plans/open-network/track-c-chain.md's `make chain-test`.
chain-test: test

## chain-sim: alias reserved for a future randomized simulation harness
## (plans/open-network/track-c-chain.md's `make chain-sim`). x/emission has no Msg service to
## fuzz yet, so today this just runs the same unit tests, including the 4,000-epoch simulated
## invariant run in x/emission/keeper.
chain-sim: test

## lint: go vet (the project's Go style rules - no magic numbers, wrapped errors, short
## functions/files - are enforced by review, not a separate linter here).
lint:
	CGO_ENABLED=0 go vet -tags nowasm ./...

## localnet: build oramad and start a fresh N-validator localnet (default N=4) with a shortened
## epoch. Pass N=<n> to change the validator count, e.g. `make localnet N=6`.
N ?= 4
localnet:
	./scripts/localnet/localnet.sh start $(N)

## localnet-stop: stop every running localnet node without deleting chain data.
localnet-stop:
	./scripts/localnet/localnet.sh stop

## localnet-clean: stop every running localnet node and delete all chain data.
localnet-clean:
	./scripts/localnet/localnet.sh clean
