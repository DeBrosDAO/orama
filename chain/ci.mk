# CI and static-analysis targets of the chain module (SA0, bugboard 3381; SA8, bugboard 3394).
#
# Included by the Makefile. The workflows in .gitea/workflows (ci.yml per pull request,
# chain-nightly.yml every night) call these targets and nothing else, so what CI runs is what a
# developer runs: `make -C chain <target>`. website/src/docs/contributor/testing.mdx lists them.
#
# Tool versions are pinned here and nowhere else (an unpinned linter turns red on a day nothing
# changed). The Go tools run through `go run` or an install pinned below, so the Go that builds
# the module also builds the linter: a linter built by an older Go refuses a newer go directive.

# --- pinned tools -----------------------------------------------------------------------------
# staticcheck is the one the fleet scanners feature builds (e2e/features/scanners/staticcheck.mod):
# one pin for both.
STATICCHECK := go run -modfile=$(CURDIR)/../e2e/features/scanners/staticcheck.mod honnef.co/go/tools/cmd/staticcheck
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.8.0
GOSEC_VERSION := v2.29.0
NILAWAY_VERSION := v0.0.0-20260918162853-acb8859b9031
GREMLINS_VERSION := v0.6.0
SEMGREP_VERSION := 1.180.0
CARGO_AUDIT_VERSION := 0.22.2
CARGO_DENY_VERSION := 0.20.2
CARGO_FUZZ_VERSION := 0.13.2
# The toolchain native/build.sh builds with. Miri and the sanitizers need a nightly one, pinned to
# a dated build (an undated `nightly` turns the nightly job red on a day nothing in the repo
# changed). 2026-10-01 is in rustup's dist index with miri and rust-src available for
# x86_64-unknown-linux-gnu. Move the date on purpose, with the job's result in hand.
RUST_TOOLCHAIN ?= 1.98.1
RUST_NIGHTLY ?= nightly-2026-10-01

# The comparison the fleet scanners feature makes: reachable vulnerabilities against the accepted
# list, staticcheck findings with the generated gateway code removed. The same code, no fleet.
SCANGATE := cd ../e2e && go run ./cmd/scangate
GOVULN_ACCEPTED := $(CURDIR)/../e2e/features/scanners/govulncheck-accepted.yaml

# Build-tag sets that compile different files: a file under a tag an analyser was not given is a file
# nobody analysed. go vet is quick and runs all three. The whole-program analysers (staticcheck,
# gosec, govulncheck) each take minutes per set and run two that together cover every file: the
# default build (CosmWasm, no Orchard) and nowasm with the Orchard library (the nowasm stubs, and the
# cgo files that only build with orchardffi).
TAGSETS := "" "nowasm" "nowasm orchardffi"
ANALYSIS_TAGSETS := "" "nowasm orchardffi"

# --- the Rust crates --------------------------------------------------------------------------
# Lint and sanitize: the crates that are compiled into oramad or used to prove for it.
RUST_CRATES := $(ORCHARD_CRATE) $(VERIFIER_CRATE) $(SHIELDED_WALLET_CRATE) native
# Audit and deny: those, plus the CosmWasm contracts the tests upload.
RUST_AUDITED := $(RUST_CRATES) app/testdata/echo app/testdata/relay app/testdata/hook
# Lints this repo does not follow, with the reason. manual_is_multiple_of wants a method stable
# only since Rust 1.90, and the crates declare rust-version 1.88. chunks_exact_to_as_chunks fires
# in the verifier library (x/shielded/orchardffi/src/tree.rs): editing it changes the recorded hash
# of the static library (native/libwasmvm_muslc.x86_64.a.sha256), so it waits for the next release
# rebuild.
CLIPPY_ALLOW := -A clippy::manual_is_multiple_of -A clippy::chunks_exact_to_as_chunks
# Advisories cargo-audit ignores. The list and the reasons are in deny.toml; keep both in step.
RUST_AUDIT_IGNORE := RUSTSEC-2026-0007 RUSTSEC-2026-0001 RUSTSEC-2026-0233 RUSTSEC-2026-0234 \
	RUSTSEC-2026-0235 RUSTSEC-2026-0204 RUSTSEC-2026-0009 RUSTSEC-2026-0067 RUSTSEC-2026-0068 \
	RUSTSEC-2026-0285 RUSTSEC-2026-0049 RUSTSEC-2026-0098 RUSTSEC-2026-0099 RUSTSEC-2026-0104

# The state-machine packages the consensus rules (.semgrep/consensus.yml) apply to.
SEMGREP_SCOPE := app $(wildcard x/*/keeper) $(wildcard x/*/types) $(wildcard x/*/ante) x/inclusion
SEMGREP_EXCLUDE := --exclude '*_test.go' --exclude '*.pb.go' --exclude '*.pb.gw.go'

# The money modules the nightly mutation run covers: emission, fees, power and the settlement
# code of storage. Generated code and (for storage) every file but settlement.go are excluded.
MUTATION_PACKAGES := x/emission/keeper x/emission/types x/fees/keeper x/power/keeper x/power/types x/storage/keeper
# gremlins matches -E against the file's path inside the package, so the names are anchored on that.
MUTATION_EXCLUDE := -E '\.pb\.go$$' -E '\.pb\.gw\.go$$'
MUTATION_EXCLUDE_STORAGE := -E '(^|/)(abci|assign|boundary|challenge|deals|genesis|grpc_query|hash|invariants|isolate|keeper|msg_server|nodes_sync|state)\.go$$'
# Efficacy is killed / (killed + lived). The gate for a package sits a few points under what the
# first full run measured, so a regression trips it and noise does not; raise it as survivors are
# killed. MUTATION_EFFICACY=<n> overrides every package (0 measures without failing).
MUTATION_EFFICACY ?=
# package=percent, space separated; a package not listed uses MUTATION_DEFAULT_MIN. Measured on a
# local machine: emission/keeper 96, fees/keeper 98, power/keeper 100, emission/types 85 (before
# params_bounds_test.go pinned the edges of Params.Validate), power/types 69. x/storage/keeper (settlement.go
# only) has not been measured: its bar is 0, so the first nightly run only reports. Set it from that
# run, then raise every bar as survivors are killed.
MUTATION_MIN := x/emission/types=80 x/power/types=65 x/storage/keeper=0
MUTATION_DEFAULT_MIN := 90

# --- fuzzing (SA2, bugboard 3388) -------------------------------------------------------------
# Go native fuzz targets (func FuzzX(f *testing.F)) in chain/ and core/, and four cargo-fuzz targets
# over the Orchard verifier crates. website/src/docs/contributor/testing.mdx ("Fuzzing") has the
# budgets and what each target checks. The Go targets run with nowasm (no libwasmvm to build) and
# the Orchard library and verifier binary linked, so the differential and cgo targets run.
FUZZ_TAGS := nowasm orchardffi
# Time per target. fuzz-short runs on every pull request: 10 s is enough to replay the whole seed
# corpus and mutate it for a few thousand executions; fuzz-long is the nightly run.
FUZZ_TIME_SHORT ?= 10s
FUZZ_TIME_LONG ?= 5m
# Targets run at once and fuzzing workers per target. Four-core runners: two targets of two workers.
FUZZ_JOBS ?= 2
FUZZ_PARALLEL ?= 2
# Where the inputs that made a target fail are copied (the workflows upload this directory).
FUZZ_ARTIFACTS ?= $(CURDIR)/$(BUILD_DIR)/fuzz-crashers
# Seconds per cargo-fuzz target in the nightly run.
FUZZ_CARGO_SECONDS ?= 600
# crate:target pairs. The corpus directory of a crate's fuzz/ is ignored by git; fuzz/seeds/<target>
# is the committed seed set, read-only to the fuzzer.
FUZZ_CARGO_TARGETS := $(ORCHARD_CRATE):ffi_verify $(ORCHARD_CRATE):tree_append \
	$(VERIFIER_CRATE):stdin_frames $(VERIFIER_CRATE):check_framing

.PHONY: rust-ignores ci ci-nightly test-race wasmvm-lib native-vendor staticcheck gosec govulncheck vet-all \
	nilaway semgrep semgrep-test mutation rust-lint rust-audit rust-deny rust-miri rust-asan \
	rust-tools tools-go fuzz-list fuzz-corpus fuzz-short fuzz-long fuzz-long-chain fuzz-long-core rust-fuzz rust-fuzz-tools

## print-<VAR>: print a variable of this Makefile (the workflows read the pinned versions from here).
print-%:
	@echo '$($*)'

## ci: everything the pull-request workflow runs, in one command (slow: build, race tests, scanners).
ci: vet-all test-race fuzz-short staticcheck gosec govulncheck semgrep-test semgrep rust-lint rust-audit rust-deny rust-ignores

## ci-nightly: the heavy checks the nightly workflow runs.
ci-nightly: nilaway mutation rust-miri rust-asan fuzz-long rust-fuzz

## wasmvm-lib: build libwasmvm from the source in the Go module cache (the version go.mod pins,
## checked against go.sum) into build/wasmvm, so the cgo tests that run CosmWasm do not need a
## downloaded binary. Pair with -tags sys_wasmvm and CGO_LDFLAGS=-L$(WASMVM_OUT).
WASMVM_OUT := $(CURDIR)/$(BUILD_DIR)/wasmvm
WASMVM_SO := $(if $(filter darwin,$(HOST_GOOS)),libwasmvm.dylib,libwasmvm.so)
wasmvm-lib:
	rm -rf $(WASMVM_OUT)
	mkdir -p $(WASMVM_OUT)
	cp -R $(WASMVM_DIR)/libwasmvm $(WASMVM_OUT)/src
	chmod -R u+w $(WASMVM_OUT)/src
	cd $(WASMVM_OUT)/src && cargo +$(RUST_TOOLCHAIN) build --release --locked
	cp $(WASMVM_OUT)/src/target/release/$(WASMVM_SO) $(WASMVM_OUT)/$(WASMVM_SO)

## test-race: every package under the race detector with all three native pieces linked: CosmWasm
## (wasmvm-lib), the Orchard verifier library through cgo and the out-of-process verifier binary
## (orchard-lib, orchard-verifier). This is the test run the pull-request workflow gates on.
test-race: orchard-lib orchard-verifier wasmvm-lib
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(WASMVM_OUT)" LD_LIBRARY_PATH="$(WASMVM_OUT)" DYLD_LIBRARY_PATH="$(WASMVM_OUT)" \
		go test -race -count=1 -timeout 30m -tags "sys_wasmvm orchardffi" ./...

## vet-all: go vet under each build-tag set.
vet-all:
	@for tags in $(TAGSETS); do echo "go vet -tags $$tags"; go vet -tags "$$tags" ./... || exit 1; done

## staticcheck: staticcheck under each build-tag set; the only findings tolerated are the
## deprecation warnings in grpc-gateway v1 generated files (e2e/harness/staticfind).
## staticcheck exits 1 when it found something and 2 or more when it could not run; only the second
## is a failed run. The output goes through a file so a crash is not read as "no findings".
staticcheck:
	@out=$$(mktemp); trap 'rm -f $$out' EXIT; for tags in $(ANALYSIS_TAGSETS); do \
		echo "staticcheck -tags $$tags"; \
		$(STATICCHECK) -f json -tags "$$tags" ./... > $$out; rc=$$?; \
		if [ $$rc -gt 1 ]; then echo "staticcheck could not run (exit $$rc)" >&2; exit 1; fi; \
		( $(SCANGATE) static ) < $$out || exit 1; \
	done

## gosec: high-severity, high-confidence findings, the bar the fleet scanners feature sets.
gosec: tools-go
	@for tags in $(ANALYSIS_TAGSETS); do \
		echo "gosec -tags $$tags"; \
		gosec -quiet -exclude-generated -exclude-dir=.semgrep -severity high -confidence high -tags "$$tags" ./... || exit 1; \
	done

## govulncheck: reachable vulnerabilities against e2e/features/scanners/govulncheck-accepted.yaml.
## With -format json govulncheck exits 0 whatever it found, so any other exit is a failed run.
govulncheck:
	@out=$$(mktemp); trap 'rm -f $$out' EXIT; for tags in $(ANALYSIS_TAGSETS); do \
		echo "govulncheck -tags $$tags"; \
		$(GOVULNCHECK) -format json -tags "$$tags" ./... > $$out || { echo "govulncheck could not run" >&2; exit 1; }; \
		( $(SCANGATE) vuln --module chain --accepted $(GOVULN_ACCEPTED) ) < $$out || exit 1; \
	done

## tools-go: install the pinned Go tools that are not run through `go run`.
tools-go:
	go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)

## nilaway: nil-flow analysis of the module (nightly: it reports false positives, see testing.mdx).
## Not a gate: the report is the output.
nilaway:
	go install go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VERSION)
	mkdir -p $(BUILD_DIR)
	@nilaway -include-pkgs="github.com/DeBrosOfficial/network/chain" -test=false -tags nowasm ./... > $(BUILD_DIR)/nilaway.txt 2>&1; rc=$$?; \
		if [ $$rc -ne 0 ] && [ $$rc -ne 3 ]; then echo "nilaway could not run (exit $$rc)" >&2; tail -20 $(BUILD_DIR)/nilaway.txt >&2; exit 1; fi; \
		echo "nilaway: $$(sed 's/\x1b\[[0-9;]*m//g' $(BUILD_DIR)/nilaway.txt | grep -c 'error:') findings in $(BUILD_DIR)/nilaway.txt (exit $$rc)"

## semgrep: the consensus-determinism rules over the state-machine packages. Exits non-zero on a finding.
semgrep:
	semgrep scan --config .semgrep/consensus.yml --metrics=off --disable-version-check --error --quiet $(SEMGREP_EXCLUDE) $(SEMGREP_SCOPE)

## semgrep-test: the rules against their annotated cases. semgrep skips dot-directories when it looks
## for tests, so the files are tested from a temporary copy.
semgrep-test:
	@dir=$$(mktemp -d) && cp .semgrep/consensus.yml .semgrep/consensus.go $$dir/ && \
		cd $$dir && semgrep --test --metrics=off --disable-version-check . ; status=$$?; rm -rf $$dir; exit $$status

## mutation: mutation testing of the money modules (nightly, not per pull request: each package takes
## minutes). A surviving mutant is a change to the code that no test notices. Fails when a package's
## efficacy is below its MUTATION_MIN (MUTATION_EFFICACY=<n> sets one bar for all).
mutation:
	go install github.com/go-gremlins/gremlins/cmd/gremlins@$(GREMLINS_VERSION)
	mkdir -p $(BUILD_DIR)
	@for p in $(MUTATION_PACKAGES); do \
		min=$(MUTATION_EFFICACY); \
		if [ -z "$$min" ]; then min=$(MUTATION_DEFAULT_MIN); for kv in $(MUTATION_MIN); do [ "$${kv%%=*}" = "$$p" ] && min=$${kv#*=}; done; fi; \
		echo "== gremlins $$p (efficacy at least $$min percent)"; \
		out=$(BUILD_DIR)/mutation-$$(echo $$p | tr / -).json; \
		if [ "$$p" = x/storage/keeper ]; then \
			CGO_ENABLED=0 gremlins unleash --tags nowasm --timeout-coefficient 10 $(MUTATION_EXCLUDE) $(MUTATION_EXCLUDE_STORAGE) --threshold-efficacy $$min --output $$out $$p || exit 1; \
		else \
			CGO_ENABLED=0 gremlins unleash --tags nowasm --timeout-coefficient 10 $(MUTATION_EXCLUDE) --threshold-efficacy $$min --output $$out $$p || exit 1; \
		fi; \
	done

## native-vendor: stage libwasmvm's source where native/Cargo.toml expects it (what native/build.sh does).
native-vendor:
	rm -rf native/vendor
	mkdir -p native/vendor
	cp -R $(WASMVM_DIR)/libwasmvm native/vendor/wasmvm
	chmod -R u+w native/vendor
	rm -rf native/vendor/wasmvm/artifacts

## rust-lint: clippy with warnings denied over every target (library, tests, examples) of each crate.
rust-lint: native-vendor
	@for c in $(RUST_CRATES); do \
		echo "clippy $$c"; \
		(cd $$c && cargo +$(RUST_TOOLCHAIN) clippy --locked --all-targets -- -D warnings $(CLIPPY_ALLOW)) || exit 1; \
	done

## rust-tools: install the pinned cargo-audit and cargo-deny.
rust-tools:
	@cargo audit --version 2>/dev/null | grep -q "$(CARGO_AUDIT_VERSION)" || cargo install --locked --version $(CARGO_AUDIT_VERSION) cargo-audit
	@cargo deny --version 2>/dev/null | grep -q "$(CARGO_DENY_VERSION)" || cargo install --locked --version $(CARGO_DENY_VERSION) cargo-deny

## rust-audit: RustSec advisories in each crate's Cargo.lock. Fails on a vulnerability that is not
## in RUST_AUDIT_IGNORE; unmaintained and unsound warnings are printed, not failed.
rust-audit: rust-tools
	@for c in $(RUST_AUDITED); do \
		echo "cargo audit $$c"; \
		(cd $$c && cargo audit -f Cargo.lock $(addprefix --ignore ,$(RUST_AUDIT_IGNORE))) || exit 1; \
	done

## rust-deny: advisories, licences, bans and sources by deny.toml for each crate.
rust-deny: rust-tools
	@for c in $(RUST_AUDITED); do \
		echo "cargo deny $$c"; \
		cargo deny --manifest-path $$c/Cargo.toml --config deny.toml --log-level error check || exit 1; \
	done

## rust-ignores: keep the ignored advisories honest. Every ignore in deny.toml carries a "review-by
## YYYY-MM-DD" in its reason and the date has not passed, deny.toml and RUST_AUDIT_IGNORE name the same
## ids, and every ignored id matches a crate in some lockfile (cargo-deny reports "advisory-not-detected"
## for an id in every crate it checked when the ignore covers nothing). scripts/rustignores does the
## comparison; cargo-deny's own failures are read from its output, so a failed run is not a clean one.
rust-ignores: rust-tools native-vendor
	@out=$$(mktemp -d); trap 'rm -rf $$out' EXIT; \
	for c in $(RUST_AUDITED); do \
		echo "cargo deny advisories $$c"; \
		cargo deny --manifest-path $$c/Cargo.toml --config deny.toml --format json check advisories 2> $$out/$$(echo $$c | tr / -).json > /dev/null || true; \
	done; \
	go run ./scripts/rustignores -deny deny.toml -ci ci.mk $$out/*.json

## rust-miri: Miri over the Orchard FFI crate's unsafe surface (nightly job): the two extern "C"
## entry points with bad arguments (tests/ffi_args.rs) and the input-framing refusals of verify
## (three tests of tests/vectors.rs that return before any cryptography). Hashing a Merkle level or
## checking a proof is not run: halo2 and Sinsemilla under an interpreter take hours. Stacked
## Borrows is on; it flags bitvec 1.1.1 (inside sinsemilla) as soon as a frontier is hashed, which is
## upstream's finding and outside what is run here.
rust-miri:
	cd $(ORCHARD_CRATE) && MIRIFLAGS="-Zmiri-disable-isolation" cargo +$(RUST_NIGHTLY) miri test --locked --test ffi_args
	cd $(ORCHARD_CRATE) && MIRIFLAGS="-Zmiri-disable-isolation" cargo +$(RUST_NIGHTLY) miri test --locked --test vectors -- wrong_proof_length empty_truncated absurd_action

## rust-asan: the Orchard FFI crate's whole test suite (frontier and real proof vectors) under
## AddressSanitizer (nightly job). -Zbuild-std rebuilds std with the sanitizer.
rust-asan:
	cd $(ORCHARD_CRATE) && RUSTFLAGS="-Zsanitizer=address" cargo +$(RUST_NIGHTLY) test --locked -Zbuild-std --target $$(rustc +$(RUST_NIGHTLY) -vV | sed -n 's/^host: //p') --tests

## fuzz-list: the Go fuzz targets of chain and core, one per line.
fuzz-list:
	go run ./scripts/fuzzrun -list -tags "$(FUZZ_TAGS)"
	go run ./scripts/fuzzrun -list -module ../core

## fuzz-short: every Go fuzz target of chain and core for FUZZ_TIME_SHORT, a few at a time (the pull
## request job). A target that fails leaves its input in the package's testdata/fuzz/<Target>/, where
## a plain `go test` replays it, and a copy under FUZZ_ARTIFACTS.
fuzz-short: orchard-lib orchard-verifier
	@rc=0; \
	CGO_ENABLED=1 go run ./scripts/fuzzrun -tags "$(FUZZ_TAGS)" -fuzztime $(FUZZ_TIME_SHORT) -jobs $(FUZZ_JOBS) -parallel $(FUZZ_PARALLEL) -artifacts $(FUZZ_ARTIFACTS)/chain || rc=1; \
	go run ./scripts/fuzzrun -module ../core -fuzztime $(FUZZ_TIME_SHORT) -jobs $(FUZZ_JOBS) -parallel $(FUZZ_PARALLEL) -artifacts $(FUZZ_ARTIFACTS)/core || rc=1; \
	exit $$rc

## fuzz-long-chain and fuzz-long-core: the Go targets of one module for FUZZ_TIME_LONG each (the
## nightly jobs, one per module so a slow one does not hide the other). FUZZ_ONLY=<regexp> picks
## targets by "package/FuzzName". fuzz-long is both.
FUZZ_ONLY ?=
fuzz-long: fuzz-long-chain fuzz-long-core

fuzz-long-chain: orchard-lib orchard-verifier
	CGO_ENABLED=1 go run ./scripts/fuzzrun -tags "$(FUZZ_TAGS)" -fuzztime $(FUZZ_TIME_LONG) -jobs $(FUZZ_JOBS) -parallel $(FUZZ_PARALLEL) -only '$(FUZZ_ONLY)' -artifacts $(FUZZ_ARTIFACTS)/chain

fuzz-long-core:
	go run ./scripts/fuzzrun -module ../core -fuzztime $(FUZZ_TIME_LONG) -jobs $(FUZZ_JOBS) -parallel $(FUZZ_PARALLEL) -only '$(FUZZ_ONLY)' -artifacts $(FUZZ_ARTIFACTS)/core

## fuzz-corpus: rebuild the committed seed corpora that come from real data in the repository
## (the Ironwood vectors, the signed transactions of client/tx, the network manifests, a Tor vote)
## with scripts/fuzzcorpus. The other targets seed themselves in code (f.Add).
fuzz-corpus:
	rm -rf x/shielded/bundle/testdata/fuzz/FuzzBundleParse x/shielded/verify/orchard/testdata/fuzz/FuzzOrchardSighash \
		x/shielded/verify/orchardproc/testdata/fuzz/FuzzOrchardVerifyDifferential app/testdata/fuzz/FuzzAnteTxDecode \
		app/testdata/fuzz/FuzzCheckTx indexer/testdata/fuzz/FuzzIndexerReadEnvelope client/tx/testdata/fuzz/FuzzClientTxDecode \
		reporter/testdata/fuzz/FuzzReporterParseVote piece/testdata/fuzz/FuzzPieceRoundTrip \
		../core/pkg/netregistry/testdata/fuzz/FuzzNetregistryManifest ../core/pkg/tornet/testdata/fuzz/FuzzTornetNetwork
	go run ./scripts/fuzzcorpus -dir x/shielded/bundle/testdata/fuzz/FuzzBundleParse -prefix vector \
		-file x/shielded/orchardffi/testdata/ironwood-1-action.bundle -file x/shielded/orchardffi/testdata/ironwood-transfer.bundle \
		-file x/shielded/orchardffi/testdata/ironwood-unshield.bundle
	go run ./scripts/fuzzcorpus -dir x/shielded/verify/orchard/testdata/fuzz/FuzzOrchardSighash -prefix vector \
		-pre string:orama-localnet-1 -pre bytes: -post uint16:0 -post uint8:1 \
		-file x/shielded/orchardffi/testdata/ironwood-1-action.bundle -file x/shielded/orchardffi/testdata/ironwood-unshield.bundle
	go run ./scripts/fuzzcorpus -dir x/shielded/verify/orchardproc/testdata/fuzz/FuzzOrchardVerifyDifferential -prefix vector \
		-post bytes: -file x/shielded/orchardffi/testdata/ironwood-1-action.bundle -file x/shielded/orchardffi/testdata/ironwood-unshield.bundle
	go run ./scripts/fuzzcorpus -dir app/testdata/fuzz/FuzzAnteTxDecode -prefix tx -hexjson client/tx/testdata/tx_vectors.json#tx_hex
	go run ./scripts/fuzzcorpus -dir app/testdata/fuzz/FuzzCheckTx -prefix tx -hexjson client/tx/testdata/tx_vectors.json#tx_hex
	go run ./scripts/fuzzcorpus -dir indexer/testdata/fuzz/FuzzIndexerReadEnvelope -prefix tx -hexjson client/tx/testdata/tx_vectors.json#tx_hex
	go run ./scripts/fuzzcorpus -dir client/tx/testdata/fuzz/FuzzClientTxDecode -prefix tx -post string:orama-vectors-1 -post uint64:7 \
		-hexjson client/tx/testdata/tx_vectors.json#tx_hex
	go run ./scripts/fuzzcorpus -dir reporter/testdata/fuzz/FuzzReporterParseVote -prefix vote -file reporter/testdata/vote-sample.vote
	go run ./scripts/fuzzcorpus -dir piece/testdata/fuzz/FuzzPieceRoundTrip -prefix vector -post uint64:1 -post uint16:3 \
		-hexjson piece/testdata/vectors.json#data_hex
	go run ./scripts/fuzzcorpus -dir ../core/pkg/netregistry/testdata/fuzz/FuzzNetregistryManifest -prefix stagenet -file ../networks/stagenet/manifest.json
	go run ./scripts/fuzzcorpus -dir ../core/pkg/tornet/testdata/fuzz/FuzzTornetNetwork -prefix stagenet -file ../networks/stagenet/tor-network.json

## rust-fuzz-tools: install the pinned cargo-fuzz and the dated nightly Rust it runs on.
rust-fuzz-tools:
	@cargo fuzz --version 2>/dev/null | grep -q "$(CARGO_FUZZ_VERSION)" || cargo install --locked --version $(CARGO_FUZZ_VERSION) cargo-fuzz
	rustup toolchain install $(RUST_NIGHTLY) --profile minimal

## rust-fuzz: the four cargo-fuzz targets of the Orchard verifier crates, FUZZ_CARGO_SECONDS each
## (nightly): the C ABI verify entry, the tree append entry, the verifier process's stdin frame
## parser, and the framing check of the out-of-process verifier against the library's. The fuzz
## crates have their own Cargo.lock (seeded from the crate's pinned one) and are never built into oramad.
rust-fuzz: rust-fuzz-tools
	@rc=0; for pair in $(FUZZ_CARGO_TARGETS); do \
		crate=$${pair%%:*}; target=$${pair##*:}; \
		echo "cargo fuzz $$crate $$target ($(FUZZ_CARGO_SECONDS)s)"; \
		mkdir -p $$crate/fuzz/corpus/$$target; \
		(cd $$crate && cargo +$(RUST_NIGHTLY) fuzz run $$target fuzz/corpus/$$target fuzz/seeds/$$target -- -max_total_time=$(FUZZ_CARGO_SECONDS) -rss_limit_mb=4096) || rc=1; \
	done; exit $$rc
