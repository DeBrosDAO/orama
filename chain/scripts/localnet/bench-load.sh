#!/usr/bin/env bash
# Run the oramad-bench load generator against a fresh N-validator localnet and tear it down.
#
# Usage: bench-load.sh            (normally through `make bench-load`)
#
# It refuses to run when a localnet already exists (its data would be lost); run
# `make localnet-clean` first. Everything stays on this machine: no network, no deploy.
#
# Env overrides:
#   BENCH_PROFILE       default: smoke. `full` looks for saturation and takes a long time; see
#                       website/src/docs/contributor/testing.mdx ("Load and saturation tests").
#   BENCH_TYPES         default: all the profile's types. A comma-separated subset, e.g. bank-send,cnft-mint.
#   N                   default: 4. Validators.
#   TIMEOUT_COMMIT      default: 1s. CometBFT's own default is 5s, which makes a short run show only a handful
#                       of blocks; the report states the block interval it measured.
#   VOTE_EXTENSIONS_ENABLE_HEIGHT
#                       default: 0 (vote extensions off). Set it to 2 for a run with C13 inclusion lists on.
#   WITH_WASM           default: 1 when libwasmvm is installed, else 0 (wasm-call is then skipped).
#   WITH_SHIELDED       default: 1 when cargo is installed, else 0 (the shielded types are then skipped).
#   KEEP_LOCALNET       set to 1 to leave the localnet running afterwards.
#   BENCH_OUT           default: build/bench-report.json.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="$(cd "$here/../.." && pwd)"
localnet="$here/localnet.sh"
data_dir="$here/.localnet"
bench="$chain_root/build/oramad-bench"
key_file="$data_dir/funder.key"
cache_file="$data_dir/bench-shielded.json"

if ! [[ "${N:-4}" =~ ^[0-9]+$ ]] || [ "${N:-4}" -lt 2 ] || [ "${N:-4}" -gt 10 ]; then
	echo "N must be a number from 2 to 10 (at least two validators are needed to compare app hashes), got: ${N:-}" >&2
	exit 1
fi
export N="${N:-4}"
export TIMEOUT_COMMIT="${TIMEOUT_COMMIT:-1s}"
export VOTE_EXTENSIONS_ENABLE_HEIGHT="${VOTE_EXTENSIONS_ENABLE_HEIGHT:-0}"
export FAUCET_ENABLED=1
profile="${BENCH_PROFILE:-smoke}"
out="${BENCH_OUT:-$chain_root/build/bench-report.json}"

log() { printf '==> %s\n' "$*"; }

if [ -d "$data_dir" ]; then
	echo "a localnet already exists at $data_dir; run 'make localnet-clean' first (this script would wipe it)" >&2
	exit 1
fi

if [ -z "${WITH_WASM:-}" ]; then
	if ls "$(cd "$chain_root" && go list -m -f '{{.Dir}}' github.com/CosmWasm/wasmvm/v3)"/internal/api/libwasmvm.* >/dev/null 2>&1; then WITH_WASM=1; else WITH_WASM=0; fi
fi
if [ -z "${WITH_SHIELDED:-}" ]; then
	if command -v cargo >/dev/null 2>&1; then WITH_SHIELDED=1; else WITH_SHIELDED=0; fi
fi
export WITH_WASM WITH_SHIELDED

cleanup() {
	if [ "${KEEP_LOCALNET:-0}" = "1" ]; then
		log "leaving the localnet running (KEEP_LOCALNET=1); 'make localnet-clean' removes it"
	else
		"$localnet" clean >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

log "building oramad-bench"
(cd "$chain_root" && go build -o "$bench" ./cmd/oramad-bench)

"$localnet" start "$N"

log "waiting for $N validators to produce blocks"
for ((i = 0; i < N; i++)); do
	port=$((31001 + i * 10))
	for _ in $(seq 1 180); do
		height="$(curl -s --max-time 2 "http://127.0.0.1:$port/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])' 2>/dev/null || echo 0)"
		[ "${height:-0}" -ge 3 ] && break
		sleep 2
	done
	if [ "${height:-0}" -lt 3 ]; then
		echo "node$i did not reach height 3" >&2
		exit 1
	fi
done

# The key is a localnet test key, but it is still written readable by its owner only.
(umask 077 && "$localnet" funder-key 2>/dev/null | grep -E '^[0-9a-f]{64}$' | tail -n 1 >"$key_file")

common=(-profile "$profile" -localnet-nodes "$N" -funder-key-file "$key_file" -cache "$cache_file" -module-dir "$chain_root")
if [ -n "${BENCH_TYPES:-}" ]; then common+=(-types "$BENCH_TYPES"); fi

if [ "$WITH_SHIELDED" = "1" ]; then
	"$bench" prepare "${common[@]}"
fi

mkdir -p "$(dirname "$out")"
"$bench" run "${common[@]}" -out "$out"
