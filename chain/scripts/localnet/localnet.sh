#!/usr/bin/env bash
# Run an N-validator Orama L1 localnet on localhost, no Docker.
#
# Usage:
#   localnet.sh [start] [N]   build oramad, generate a fresh N-validator genesis (default N=4)
#                             and start every node in the background
#   localnet.sh stop          stop every running node (keeps chain data on disk)
#   localnet.sh clean         stop every running node and delete all chain data
#   localnet.sh status        print each node's height and catching-up state
#
# Env overrides:
#   CHAIN_ID              default: orama-localnet-1 (must contain "-localnet-": x/power's
#                         bootstrap-committee chain-id gate only allows a committee smaller than
#                         the 30-member production floor on a devnet/stagenet/localnet chain-id)
#   EPOCH_DURATION        default: 30s  (x/emission genesis param, Go duration syntax)
#   EPOCH_MIN_BLOCKS      default: 5    (x/emission genesis param)
#   BLOCK_MAX_GAS         default: 100000000 (consensus block gas limit patched into genesis)
#
# Genesis starts at exactly zero norama supply: every node is a member of x/power's bootstrap
# committee (plans/open-network.md D16), which needs no self-bond and no gentx - each committee
# member gets an equal share of genesis voting power straight from its own priv_validator_key.json
# (see `oramad genesis add-bootstrap-validator --help`). This replaced the old devnet-only
# self-bonded-validator exception (x/emission's now-vestigial allow_bootstrap_stake premine gate,
# kept only for the epoch-duration/min-blocks-per-epoch floor relaxation below - see docs/CHAIN.md).
set -euo pipefail

CHAIN_ID="${CHAIN_ID:-orama-localnet-1}"
DENOM="norama"
EPOCH_DURATION="${EPOCH_DURATION:-30s}"
EPOCH_MIN_BLOCKS="${EPOCH_MIN_BLOCKS:-5}"
BLOCK_MAX_GAS="${BLOCK_MAX_GAS:-100000000}"

case "$CHAIN_ID" in
*-stagenet-*|*-devnet-*|*-localnet-*) ;;
*)
	echo "CHAIN_ID must contain -stagenet-, -devnet- or -localnet- (x/power's bootstrap-committee chain-id gate requires it below the 30-member production floor), got: $CHAIN_ID" >&2
	exit 1
	;;
esac

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="$(cd "$here/../.." && pwd)"
data_dir="$here/.localnet"
bin="$data_dir/oramad"

log() { printf '==> %s\n' "$*"; }

# setup_log is where every setup command's stdout/stderr goes, so a failure (this script runs
# under `set -e`, so any of these aborts it) can actually be diagnosed instead of silently
# vanishing into /dev/null.
setup_log() { echo "$data_dir/setup.log"; }

# run_logged runs its arguments, appending their combined output to setup_log, and prints that
# log's tail plus its path if the command fails.
run_logged() {
	if ! "$@" >>"$(setup_log)" 2>&1; then
		echo "command failed: $*" >&2
		echo "--- last 40 lines of $(setup_log) ---" >&2
		tail -n 40 "$(setup_log)" >&2 || true
		return 1
	fi
}

node_home() { echo "$data_dir/node$1"; }

# Port scheme: each node i (0-based) offsets every port by i*10 from a 31000 base, keeping every
# node's ports inside the 31000-31099 range requested for this script (supports up to 10 nodes).
p2p_port()   { echo $((31000 + $1 * 10)); }
rpc_port()   { echo $((31001 + $1 * 10)); }
grpc_port()  { echo $((31002 + $1 * 10)); }
api_port()   { echo $((31003 + $1 * 10)); }
prom_port()  { echo $((31004 + $1 * 10)); }
pprof_port() { echo $((31005 + $1 * 10)); }

pid_file() { echo "$(node_home "$1")/localnet.pid"; }

build() {
	log "building oramad"
	(cd "$chain_root" && CGO_ENABLED=0 go build -o "$bin" ./cmd/oramad)
}

node_count() {
	local n=0
	while [ -d "$(node_home "$n")" ]; do n=$((n + 1)); done
	echo "$n"
}

init_node() {
	local i="$1"
	local home; home="$(node_home "$i")"
	log "[node$i] init"
	run_logged "$bin" init "node$i" --chain-id "$CHAIN_ID" --default-denom "$DENOM" --home "$home"
	if ! "$bin" keys show validator --keyring-backend test --home "$home" >/dev/null 2>&1; then
		run_logged "$bin" keys add "validator" --keyring-backend test --home "$home"
	fi
}

addr_of() { "$bin" keys show validator -a --keyring-backend test --home "$(node_home "$1")"; }

build_genesis() {
	local n="$1"
	local first; first="$(node_home 0)"

	log "setting emission params: epoch-duration=$EPOCH_DURATION min-blocks-per-epoch=$EPOCH_MIN_BLOCKS allow-bootstrap-stake=true"
	# allow-bootstrap-stake only relaxes the epoch-duration/min-blocks-per-epoch floors here -
	# genesis supply is exactly zero either way (no genesis account is ever funded), so its premine
	# gate is satisfied trivially (see docs/CHAIN.md).
	run_logged "$bin" genesis set-emission-params \
		--epoch-duration "$EPOCH_DURATION" \
		--min-blocks-per-epoch "$EPOCH_MIN_BLOCKS" \
		--allow-bootstrap-stake \
		--home "$first"

	log "adding all $n nodes as x/power bootstrap committee members (zero self-bond, no gentx)"
	local i
	for ((i = 0; i < n; i++)); do
		# An empty array expansion under `set -u` is an unbound variable on bash 3.2
		# (the bash macOS ships). Pass the extra flag only on the node that needs it.
		if [ "$i" -eq 0 ]; then
			run_logged "$bin" genesis add-bootstrap-validator "$(addr_of "$i")" \
				--moniker "node$i" \
				--home "$first" \
				--consensus-pubkey-file "$(node_home "$i")/config/priv_validator_key.json" \
				--min-committee-size "$n"
		else
			run_logged "$bin" genesis add-bootstrap-validator "$(addr_of "$i")" \
				--moniker "node$i" \
				--home "$first" \
				--consensus-pubkey-file "$(node_home "$i")/config/priv_validator_key.json"
		fi
	done

	# x/consensus has no genesis state of its own (it's driven by the top-level "consensus" field
	# of genesis.json, which oramad's own module wiring can't default): set a finite block max_gas
	# here so a localnet doesn't run with CometBFT's own unlimited default.
	python3 -c "
import json
path = '$first/config/genesis.json'
with open(path) as f:
    doc = json.load(f)
doc.setdefault('consensus', {}).setdefault('params', {}).setdefault('block', {})['max_gas'] = '$BLOCK_MAX_GAS'
with open(path, 'w') as f:
    json.dump(doc, f, indent=2)
"

	"$bin" genesis validate --home "$first" >/dev/null

	log "distributing genesis.json to all nodes"
	for ((i = 1; i < n; i++)); do
		cp "$first/config/genesis.json" "$(node_home "$i")/config/genesis.json"
	done
}

configure_node() {
	local i="$1" n="$2" peers="$3"
	local home; home="$(node_home "$i")"
	local p2p; p2p="$(p2p_port "$i")"
	local rpc; rpc="$(rpc_port "$i")"
	local grpc; grpc="$(grpc_port "$i")"
	local api; api="$(api_port "$i")"
	local prom; prom="$(prom_port "$i")"
	local pprof; pprof="$(pprof_port "$i")"

	sed -i.bak \
		-e "s#^laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://127.0.0.1:$p2p\"#" \
		-e "s#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:$rpc\"#" \
		-e "s#^persistent_peers = .*#persistent_peers = \"$peers\"#" \
		-e "s#^addr_book_strict = true#addr_book_strict = false#" \
		-e "s#^allow_duplicate_ip = false#allow_duplicate_ip = true#" \
		-e "s#^prometheus_listen_addr = .*#prometheus_listen_addr = \"127.0.0.1:$prom\"#" \
		-e "s#^pprof_laddr = .*#pprof_laddr = \"localhost:$pprof\"#" \
		"$home/config/config.toml"
	rm -f "$home/config/config.toml.bak"

	sed -i.bak \
		-e "s#^address = \"tcp://localhost:1317\"#address = \"tcp://127.0.0.1:$api\"#" \
		-e "s#^address = \"localhost:9090\"#address = \"127.0.0.1:$grpc\"#" \
		"$home/config/app.toml"
	rm -f "$home/config/app.toml.bak"
}

cmd_start() {
	local n="${1:-4}"
	if [ -d "$data_dir" ]; then
		log "$data_dir already exists; run '$0 clean' first for a fresh localnet"
		exit 1
	fi
	mkdir -p "$data_dir"

	build

	local i
	for ((i = 0; i < n; i++)); do init_node "$i"; done
	build_genesis "$n"

	local peers=""
	for ((i = 0; i < n; i++)); do
		local node_id; node_id="$("$bin" comet show-node-id --home "$(node_home "$i")")"
		if [ -n "$peers" ]; then peers="$peers,"; fi
		peers="$peers$node_id@127.0.0.1:$(p2p_port "$i")"
	done

	for ((i = 0; i < n; i++)); do
		# Each node's own address must not be in its own persistent_peers list.
		local own_id; own_id="$("$bin" comet show-node-id --home "$(node_home "$i")")"
		# grep exits 1 when this node is the only peer. Under `set -o pipefail` that
		# aborts a one-node localnet, so an empty peer list is a successful result.
		local other_peers; other_peers="$(echo "$peers" | tr ',' '\n' | grep -v "^$own_id@" | paste -sd, - || true)"
		configure_node "$i" "$n" "$other_peers"
	done

	log "starting $n nodes"
	for ((i = 0; i < n; i++)); do
		local home; home="$(node_home "$i")"
		nohup "$bin" start --home "$home" >"$home/node.log" 2>&1 &
		echo $! >"$(pid_file "$i")"
		log "[node$i] pid=$(cat "$(pid_file "$i")") rpc=127.0.0.1:$(rpc_port "$i") log=$home/node.log"
	done

	log "localnet up. '$0 status' to check height, '$0 stop' to stop, '$0 clean' to wipe."
}

cmd_status() {
	local n; n="$(node_count)"
	if [ "$n" -eq 0 ]; then
		echo "no localnet found under $data_dir"
		return 1
	fi
	local i
	for ((i = 0; i < n; i++)); do
		local rpc; rpc="$(rpc_port "$i")"
		printf 'node%-3d ' "$i"
		curl -s --max-time 3 "http://127.0.0.1:$rpc/status" |
			python3 -c 'import json,sys; s=json.load(sys.stdin)["result"]["sync_info"]; print("height", s["latest_block_height"], "catching_up", s["catching_up"])' \
			2>/dev/null || echo "not responding"
	done
}

cmd_stop() {
	local n; n="$(node_count)"
	local i
	for ((i = 0; i < n; i++)); do
		local pf; pf="$(pid_file "$i")"
		if [ -f "$pf" ]; then
			local pid; pid="$(cat "$pf")"
			if kill -0 "$pid" 2>/dev/null; then
				log "[node$i] stopping pid=$pid"
				kill "$pid" 2>/dev/null || true
			fi
			rm -f "$pf"
		fi
	done
}

cmd_clean() {
	cmd_stop
	log "removing $data_dir"
	rm -rf "$data_dir"
}

case "${1:-start}" in
start)
	shift || true
	cmd_start "${1:-4}"
	;;
stop) cmd_stop ;;
clean) cmd_clean ;;
status) cmd_status ;;
*)
	echo "usage: $0 [start [N]|stop|clean|status]" >&2
	exit 2
	;;
esac
