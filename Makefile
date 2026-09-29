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

sdk-test:
	cd sdk && pnpm test

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
# The coverage gate does not block `make test` until the feature packages
# land; set it to 1 then (e2e/README.md, "Coverage gate").
E2E_COVERAGE_ENFORCE ?= 0
# Extra flags for `e2e-fleet run`, e.g. E2E_FLAGS=--keep-on-fail
E2E_FLAGS ?=

e2e-fleet:
	cd e2e && infisical run --projectId $(E2E_INFISICAL_PROJECT) --env=e2e --domain $(E2E_INFISICAL_DOMAIN) -- go run ./cmd/e2e-fleet run $(E2E_FLAGS)

e2e-coverage:
	cd e2e && E2E_COVERAGE_ENFORCE=$(E2E_COVERAGE_ENFORCE) go run ./cmd/e2e-fleet coverage

e2e-lint:
	cd e2e && go vet ./... && go vet -tags e2e_fleet ./... && go test ./lint/...

e2e-test-unit:
	cd e2e && go test ./...

# === Aggregate ===
build: core-build
test: core-test e2e-lint e2e-coverage e2e-test-unit
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
	@echo "  Aggregate:     make build | test | clean  (delegates to core)"
