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

.PHONY: build build-wasm contracts-verify test chain-test lint chain-sim localnet localnet-stop localnet-clean orchard-lib orchard-lib-linux-amd64 proto-shielded orchard-verifier orchard-verifier-linux-amd64 orchard-test build-linux-amd64-orchard orchard-smoke orchard-smoke-linux-amd64 native-lib-linux-amd64 build-linux-amd64-full build-linux-amd64-global

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

## contracts-verify: rebuild the genesis standard contracts from their pinned upstream commits and fail
## if any sha256 differs from contracts/standard/manifest.json. Slow (it compiles five contracts) and
## needs the Rust toolchain and wasm-opt named in the manifest; see docs/CHAIN.md, "x/wasm: contracts".
contracts-verify:
	./contracts/standard/build.sh verify

## test: pure-Go tests on the nowasm build, which does not need libwasmvm.
## When libwasmvm is present, also run the cgo build (wasm keeper, contract packet refusal).
test:
	CGO_ENABLED=0 go test -tags nowasm ./...
	@if [ -n "$(LIBWASMVM)" ]; then CGO_ENABLED=1 go test ./...; fi

# Orchard/Ironwood verifier (x/shielded/orchardffi, Rust). The Go side links it only under
# -tags orchardffi with cgo; without it a build fails closed with ErrVerifierNotLinked.
ORCHARD_CRATE := x/shielded/orchardffi
# Wallet-side shielded core (F7): key tree and bundle builder. Never linked into oramad.
SHIELDED_WALLET_CRATE := x/shielded/wallet
ORCHARD_LIBDIR := x/shielded/verify/orchard/lib
# The static release build links musl through zig, the toolchain `orama build` already requires.
ORAMA_ZIG ?= zig
ORCHARD_LINUX_AMD64_ZIG_TARGET := x86_64-linux-musl
ORCHARD_LINUX_AMD64_RUST_TARGET := x86_64-unknown-linux-musl

## orchard-lib: build the host static library the cgo tests link.
orchard-lib:
	cd $(ORCHARD_CRATE) && cargo build --release --locked
	mkdir -p $(ORCHARD_LIBDIR)/$(shell go env GOOS)_$(shell go env GOARCH)
	cp $(ORCHARD_CRATE)/target/release/liborama_orchard.a $(ORCHARD_LIBDIR)/$(shell go env GOOS)_$(shell go env GOARCH)/

## orchard-lib-linux-amd64: build the static library for linux/amd64 (musl, C parts via zig).
## Needs: rustup target add $(ORCHARD_LINUX_AMD64_RUST_TARGET)
orchard-lib-linux-amd64:
	cd $(ORCHARD_CRATE) && \
		ORAMA_ZIG="$(ORAMA_ZIG)" ZIG_TARGET=$(ORCHARD_LINUX_AMD64_ZIG_TARGET) \
		CC_x86_64_unknown_linux_musl="$(CURDIR)/scripts/zigcc.sh" \
		AR_x86_64_unknown_linux_musl="$(ORAMA_ZIG) ar" \
		cargo build --release --locked --target $(ORCHARD_LINUX_AMD64_RUST_TARGET)
	mkdir -p $(ORCHARD_LIBDIR)/linux_amd64
	cp $(ORCHARD_CRATE)/target/$(ORCHARD_LINUX_AMD64_RUST_TARGET)/release/liborama_orchard.a $(ORCHARD_LIBDIR)/linux_amd64/

## orchard-verifier: build the out-of-process verifier binary for this host, the second verifier of
## x/shielded. It has its own crate and its own Cargo.lock, and shares no source with the library.
VERIFIER_CRATE := x/shielded/orchardverifier-bin
VERIFIER_NAME := orama-orchard-verifier
# The pin linked into oramad: the sha256 file orchard-verifier-linux-amd64 writes.
VERIFIER_PIN_LDFLAG = -X github.com/DeBrosOfficial/network/chain/app.ShieldedVerifierSHA256=$$(cat $(BUILD_DIR)/$(VERIFIER_NAME)-linux-amd64.sha256)
orchard-verifier:
	cd $(VERIFIER_CRATE) && cargo build --release --locked
	@echo "sha256 (give it to oramad as --shielded-verifier-sha256):"
	@shasum -a 256 $(VERIFIER_CRATE)/target/release/$(VERIFIER_NAME) | cut -d' ' -f1

## orchard-verifier-linux-amd64: the same binary for linux/amd64, static (musl, C parts via zig).
## It lands in build/ next to oramad-linux-amd64-orchard; oramad finds it with --shielded-verifier or
## at <home>/bin/$(VERIFIER_NAME). The .sha256 file beside it is the pin: pass it as
## --shielded-verifier-sha256 (or link it in with -X .../app.ShieldedVerifierSHA256=<hex>).
orchard-verifier-linux-amd64:
	cd $(VERIFIER_CRATE) && \
		ORAMA_ZIG="$(ORAMA_ZIG)" ZIG_TARGET=$(ORCHARD_LINUX_AMD64_ZIG_TARGET) \
		CC_x86_64_unknown_linux_musl="$(CURDIR)/scripts/zigcc.sh" \
		AR_x86_64_unknown_linux_musl="$(ORAMA_ZIG) ar" \
		CARGO_TARGET_X86_64_UNKNOWN_LINUX_MUSL_LINKER="$(CURDIR)/scripts/zigcc.sh" \
		RUSTFLAGS="-C target-feature=+crt-static -C link-self-contained=no" \
		cargo build --release --locked --target $(ORCHARD_LINUX_AMD64_RUST_TARGET)
	mkdir -p $(BUILD_DIR)
	cp $(VERIFIER_CRATE)/target/$(ORCHARD_LINUX_AMD64_RUST_TARGET)/release/$(VERIFIER_NAME) $(BUILD_DIR)/$(VERIFIER_NAME)-linux-amd64
	shasum -a 256 $(BUILD_DIR)/$(VERIFIER_NAME)-linux-amd64 | cut -d' ' -f1 > $(BUILD_DIR)/$(VERIFIER_NAME)-linux-amd64.sha256

## orchard-test: Rust tests of all three crates (library, verifier binary, wallet), then the Go tests with both verifiers linked (the
## library through cgo, the binary out of process).
orchard-test: orchard-lib orchard-verifier
	cd $(ORCHARD_CRATE) && cargo test --release --locked
	cd $(VERIFIER_CRATE) && cargo test --release --locked
	cd $(SHIELDED_WALLET_CRATE) && cargo test --release --locked
	CGO_ENABLED=1 go test -count=1 -tags "nowasm orchardffi" ./x/shielded/... ./app/...

## build-linux-amd64-orchard: oramad for linux/amd64 with the Rust verifier library linked (static,
## musl), and the out-of-process verifier binary it needs beside it. A chain node accepts a shielded
## bundle only with both. Cross-compiles through zig cc, like the cgo binaries of `orama build`.
## nowasm: no CosmWasm. -lunwind: the Rust staticlib carries its panic personality, which needs
## zig's libunwind.
build-linux-amd64-orchard: orchard-lib-linux-amd64 orchard-verifier-linux-amd64
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		CGO_LDFLAGS="-lunwind" \
		CC="$(ORAMA_ZIG) cc -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		CXX="$(ORAMA_ZIG) c++ -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		go build -tags "nowasm orchardffi netgo osusergo" -trimpath -ldflags "$(LDFLAGS) $(VERIFIER_PIN_LDFLAG) -linkmode external -extldflags -static" \
		-o $(BUILD_DIR)/$(BINARY)-linux-amd64-orchard $(PKG)

## orchard-smoke: build and run the smoke binary on this machine (host static library).
orchard-smoke: orchard-lib
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -tags "nowasm orchardffi" -trimpath -o $(BUILD_DIR)/orchard-smoke ./cmd/orchard-smoke
	$(BUILD_DIR)/orchard-smoke

## orchard-smoke-linux-amd64: the smoke binary for linux/amd64, built with the same zig/musl
## toolchain and flags as build-linux-amd64-orchard. It is a static ELF and cannot run on darwin.
## Copy it to a linux/amd64 host and run it: exit 0 means both committed vectors verify and a
## tampered copy is rejected. It takes no arguments and needs no files (the vectors are embedded).
orchard-smoke-linux-amd64: orchard-lib-linux-amd64
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		CGO_LDFLAGS="-lunwind" \
		CC="$(ORAMA_ZIG) cc -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		CXX="$(ORAMA_ZIG) c++ -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		go build -tags "nowasm orchardffi netgo osusergo" -trimpath -ldflags "$(LDFLAGS) -linkmode external -extldflags -static" \
		-o $(BUILD_DIR)/orchard-smoke-linux-amd64 ./cmd/orchard-smoke

## native-lib-linux-amd64: libwasmvm and the Orchard verifier in one static musl library, built from
## source (libwasmvm from the module cache at the pinned wasmvm version) through zig. Needs
## `rustup target add x86_64-unknown-linux-musl`. Records the sha256 in native/.
native-lib-linux-amd64:
	./native/build.sh build

## build-linux-amd64-full: the static linux/amd64 oramad with CosmWasm and the Orchard verifier library in one
## binary (what scripts/stagenet/deploy.sh builds), and beside it build/orama-orchard-verifier-linux-amd64, the
## out-of-process second verifier whose sha256 is linked into oramad (the pin: oramad runs no other file).
## One native library, so one copy of the Rust std.
build-linux-amd64-full: native-lib-linux-amd64 orchard-verifier-linux-amd64
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		CGO_LDFLAGS="-L$(CURDIR)/$(BUILD_DIR)/native -lunwind" \
		CC="$(ORAMA_ZIG) cc -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		CXX="$(ORAMA_ZIG) c++ -target $(ORCHARD_LINUX_AMD64_ZIG_TARGET)" \
		go build -tags "muslc orchardffi netgo osusergo" -trimpath -ldflags "$(LDFLAGS) $(VERIFIER_PIN_LDFLAG) -linkmode external -extldflags -static" \
		-o $(BUILD_DIR)/$(BINARY)-linux-amd64-full $(PKG)

## build-linux-amd64-global: the static linux/amd64 orama-global (the provider, archiver, indexer and repair
## services that run beside oramad, staged by `orama global install`) and stagenet-node (the helper
## scripts/stagenet/deploy.sh runs on a stagenet node: signing agent, operator registration). Both are pure Go,
## so no native library is involved.
build-linux-amd64-global:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags nowasm $(BUILD_FLAGS) -o $(BUILD_DIR)/orama-global-linux-amd64 ./cmd/orama-global
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags nowasm $(BUILD_FLAGS) -o $(BUILD_DIR)/stagenet-node-linux-amd64 ./scripts/stagenet/node

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

## proto-shielded: regenerate x/shielded's gogoproto code from proto/orama/shielded/v1.
## Needs protoc and protoc-gen-gocosmos (go install github.com/cosmos/gogoproto/protoc-gen-gocosmos).
GOPROTO_MOD := $(shell go env GOMODCACHE)/github.com/cosmos
proto-shielded:
	@out=$$(mktemp -d) && \
	protoc -I proto \
		-I $(GOPROTO_MOD)/cosmos-sdk@$(shell go list -m -f '{{.Version}}' github.com/cosmos/cosmos-sdk)/proto \
		-I $(GOPROTO_MOD)/gogoproto@$(shell go list -m -f '{{.Version}}' github.com/cosmos/gogoproto) \
		-I $(GOPROTO_MOD)/cosmos-proto@$(shell go list -m -f '{{.Version}}' github.com/cosmos/cosmos-proto)/proto \
		--gocosmos_out=plugins=grpc,Mgoogle/protobuf/any.proto=github.com/cosmos/gogoproto/types/any:$$out \
		proto/orama/shielded/v1/*.proto && \
	cp $$out/github.com/DeBrosOfficial/network/chain/x/shielded/types/*.pb.go x/shielded/types/ && rm -rf $$out
