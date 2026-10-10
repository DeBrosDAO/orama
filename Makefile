# Orama Monorepo
# Delegates to sub-project Makefiles

.PHONY: help build test clean

# === Core (Go network) ===
.PHONY: core core-build core-test core-clean core-lint
core: core-build

core-build:
	$(MAKE) -C core build

core-test:
	$(MAKE) -C core test

core-lint:
	$(MAKE) -C core lint

core-clean:
	$(MAKE) -C core clean

# === Caddy modules (built into the node's Caddy by `orama maint build`) ===
.PHONY: caddy-test
caddy-test:
	cd caddy && go vet ./... && go test ./...

# === Cluster guide e2e (executes website/src/docs/operator/run-your-own-cluster.mdx on machines you provide) ===
.PHONY: e2e-cluster
e2e-cluster:
	$(MAKE) -C core e2e-cluster

# === Website ===
.PHONY: website website-dev website-build
website-dev:
	cd website && pnpm dev

website-build:
	cd website && pnpm build

# === SDK (TypeScript) ===
.PHONY: sdk sdk-build sdk-test
sdk: sdk-build

sdk-build:
	cd sdk && pnpm install && pnpm build

# sdk-test is part of `make test`: the SDK's unit tests include checks against the
# gateway's Go source (the auth error codes), so a gateway change can break them.
sdk-test:
	@test -d sdk/node_modules || { echo "sdk/node_modules is missing: run 'cd sdk && pnpm install --frozen-lockfile' first, then make test again"; exit 1; }
	pnpm --dir sdk test:unit

# === Vault (Zig) ===
.PHONY: vault vault-build vault-test
vault-build:
	cd vault && zig build

vault-test:
	cd vault && zig build test

# === OS ===
.PHONY: os os-build
os-build:
	$(MAKE) -C os

# === Fleet e2e (e2e/, see e2e/README.md) ===
.PHONY: e2e-fleet e2e-coverage e2e-lint e2e-test-unit
E2E_INFISICAL_PROJECT := cea224be-3999-4e8d-bd9b-f9a89c94e7fa
E2E_INFISICAL_DOMAIN  := https://infisical.debros.io/api
# The coverage gate blocks `make test`: every CLI command, gateway route, chain
# Msg/Query and systemd unit needs an e2e feature or a waiver (e2e/README.md,
# "Coverage gate").
E2E_COVERAGE_ENFORCE ?= 1
# Extra flags for `e2e-fleet run`, e.g. E2E_FLAGS=--keep-on-fail
E2E_FLAGS ?=

# The runner is built first so infisical starts the binary itself: `go run`
# would keep the cloud tokens in the go process's environment. The SSH rule of
# the run's firewall is limited to E2E_RUNNER_CIDR (your public address, e.g.
# 203.0.113.7/32); E2E_ALLOW_OPEN_SSH=1 opens it to the internet instead.
e2e-fleet:
	@test -n "$$E2E_RUNNER_CIDR" || test "$$E2E_ALLOW_OPEN_SSH" = "1" || { echo "set E2E_RUNNER_CIDR to this machine's public address (a.b.c.d/32), or E2E_ALLOW_OPEN_SSH=1"; exit 1; }
	cd e2e && go build -o e2e-fleet ./cmd/e2e-fleet && infisical run --projectId $(E2E_INFISICAL_PROJECT) --env=e2e --domain $(E2E_INFISICAL_DOMAIN) -- ./e2e-fleet run $(E2E_FLAGS)

e2e-coverage:
	cd e2e && E2E_COVERAGE_ENFORCE=$(E2E_COVERAGE_ENFORCE) go run ./cmd/e2e-fleet coverage

e2e-lint:
	cd e2e && go vet ./... && go vet -tags e2e_fleet ./... && go test ./lint/...

e2e-test-unit:
	cd e2e && go test ./...

# === Aggregate ===
build: core-build
# === Whitepaper — Technical Reference (docs/whitepaper/technical-reference) ===
# whitepaper-check runs the book's gates (ownership, anchors, structure,
# diagrams, generated appendices, version stamps) and is part of `make test`.
# whitepaper-gen regenerates the appendices built from code, including the CLI
# reference. whitepaper typesets the volumes to PDF (needs pandoc, typst, d2).
.PHONY: whitepaper whitepaper-check whitepaper-gen whitepaper-diagrams whitepaper-short whitepaper-short-check
whitepaper-check: whitepaper-short-check
	cd core && go run ./tools/whitepaper check

whitepaper-gen:
	$(MAKE) -C core docs
	cd core && go run ./tools/whitepaper gen

whitepaper-diagrams:
	cd core && go run ./tools/whitepaper diagrams

# The short edition (docs/whitepaper/orama-whitepaper): one ~100-page volume
# derived from the Technical Reference. whitepaper-short-check runs its gates
# (anchors, structure, links, words), whitepaper-short typesets it to
# dist/orama-whitepaper-v<version>.pdf and prints the page count.
whitepaper-short-check:
	cd core && go run ./tools/whitepaper check -book docs/whitepaper/orama-whitepaper

whitepaper-short: whitepaper-diagrams whitepaper-short-check
	cd core && go run ./tools/whitepaper build -book docs/whitepaper/orama-whitepaper

whitepaper: whitepaper-diagrams whitepaper-gen whitepaper-check
	cd core && go run ./tools/whitepaper build

test: core-test caddy-test sdk-test e2e-lint e2e-coverage e2e-test-unit whitepaper-check
clean: core-clean

help:
	@echo "Orama Monorepo"
	@echo ""
	@echo "  Core (Go):     make core-build | core-test | core-lint | core-clean"
	@echo "  Website:       make website-dev | website-build"
	@echo "  Vault (Zig):   make vault-build | vault-test"
	@echo "  OS:            make os-build"
	@echo "  Fleet e2e:     make e2e-fleet | e2e-coverage | e2e-lint | e2e-test-unit"
	@echo ""
	@echo "  SDK (TS):      make sdk-build | sdk-test"
	@echo ""
	@echo "  Aggregate:     make build | clean  (delegates to core); make test runs core, caddy, SDK unit, fleet-e2e lint/coverage and whitepaper checks"
