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

# === Aggregate ===
build: core-build
test: core-test
clean: core-clean

help:
	@echo "Orama Monorepo"
	@echo ""
	@echo "  Core (Go):     make core-build | core-test | core-lint | core-clean"
	@echo "  Website:       make website-dev | website-build"
	@echo "  Vault (Zig):   make vault-build | vault-test"
	@echo "  OS:            make os-build"
	@echo ""
	@echo "  Aggregate:     make build | test | clean  (delegates to core)"
