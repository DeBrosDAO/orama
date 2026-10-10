#!/usr/bin/env bash
# Maintenance of the stagenet nodes: reset them, read their status, check the chain's invariants,
# run the live smoke, and build the shielded wallet's scenario.
#
# The network itself is not made here. A network is created, and its nodes joined, by `orama setup`
# (website/src/docs/operator/setup.mdx, "Create a network"):
#
#   orama setup --create-network stagenet --chain-id orama-stagenet-N --release-root release-root.json ...
#
# It installs the signed release on every machine, makes each machine's chain keys, builds the genesis
# from all of them, starts the chains, registers the operator and the nodes, and writes
# networks/stagenet/ for the maintainer to publish. This script is for the stagenet that runs, and
# for `reset`, which returns the machines to a clean state before the next creation.
#
# Each stagenet node runs an Orama private-cluster node (orama-node, namespace gateways, RQLite,
# WireGuard) beside the global services, which run in their own network namespace (orama-global) as
# website/src/docs/blockchain/run-a-global-node.mdx describes. Nothing here starts, stops or
# reconfigures a cluster service; `reset` edits only the two global lines the install added to the
# cluster's preferences.yaml.
#
# Stagenet/devnet only: CHAIN_ID must say so, and the script refuses to run otherwise. The operator
# key of each node is the seat's "validator" key in oramad's test keyring (an unencrypted, on-disk
# keyring), a devnet-only convenience never appropriate once real value is at stake. It never leaves
# the node: `smoke` pipes it, on the node, into a signing agent that speaks the RootWallet agent
# protocol.
#
# Usage: deploy.sh reset | status | invariants | smoke | gen-shielded
#
#   reset        stop and remove the global install (and any legacy direct-unit chain install) and
#                wipe chain, provider, archiver, indexer and IPFS state. Idempotent.
#   status       services, height and peers of every node.
#   invariants   every module's invariants query on every node.
#   smoke        live checks, PASS/FAIL/SKIP each: blocks and inclusion lists, invariants, standard
#                contracts and a CW20, a private storage deal, archive ranges, shielded, and the
#                gateway's chain read.
#   gen-shielded build the wallet scenario for this chain (needs the Rust toolchain).
#
# Environment (all optional):
#   CHAIN_ID                        orama-stagenet-1; must contain -stagenet- or -devnet-.
#   VOTE_EXTENSIONS_ENABLE_HEIGHT   the height vote extensions turn on at; `orama setup` writes 2 (smoke checks it).
#   CA_FILE                         PEM bundle that signs the gateway certificate (smoke).
#   GATEWAY_URL                     the gateway smoke reads through (https://stagenet.dbrsteting.bid).
#   SHIELDED_SCENARIO               scenario JSON from `gen-shielded` (smoke).
set -euo pipefail

CHAIN_ID="${CHAIN_ID:-orama-stagenet-1}"

# Refuse anything that isn't clearly a stagenet or devnet chain-id, and reject anything that isn't
# a plain, safe identifier before it ever reaches a remote command line.
if ! [[ "$CHAIN_ID" =~ ^[a-z0-9-]{1,48}$ ]]; then
	echo "invalid CHAIN_ID (expected [a-z0-9-]{1,48}): $CHAIN_ID" >&2
	exit 1
fi
case "$CHAIN_ID" in
*-stagenet-*|*-devnet-*) ;;
*)
	echo "refusing to run: CHAIN_ID must contain -stagenet- or -devnet-, got: $CHAIN_ID" >&2
	exit 1
	;;
esac

# name:ssh-alias:public-ip. The public address is what peers and clients dial: the global services
# run in a network namespace that cannot reach the WireGuard mesh, so the chain peers over the
# public network (website/src/docs/blockchain/run-a-global-node.mdx, "Sharing a machine with a cluster node").
# Every loop below runs over NODES.
# The name is the node's id on the chain: `orama setup --name seed` with these five addresses, in this
# order, registers seed, seed-2, ... seed-5. The ssh alias is how this script reaches the machine.
NODES=("seed:mew:57.129.166.16" "seed-2:mewtwo:57.129.166.17" "seed-3:gengar:161.97.184.199" "seed-4:magicarp:161.97.184.202" "seed-5:froakie:161.97.151.255")
BIN_DIR="/usr/lib/orama-global/bin"
HOME_DIR="/var/lib/orama-global/chain"
SVC_USER="orama-chain"
TOOLS_DIR="/usr/local/lib/orama-stagenet"
# The chain's RPC and REST API listen on the orama-global namespace address (core/pkg/constants,
# GlobalNetnsAddr), which the host reaches over the veth pair; the namespace firewall admits only the host.
NS_ADDR="198.18.0.2"
RPC_ADDR="tcp://$NS_ADDR:31001"
RPC_HTTP="http://$NS_ADDR:31001"
# The height vote extensions turn on at: `orama setup` writes 2 into every genesis it builds
# (core/cmd/orama/internal/setup/create_genesis.go, voteExtensionsEnableHeight), and smoke checks it.
VOTE_EXTENSIONS_ENABLE_HEIGHT="${VOTE_EXTENSIONS_ENABLE_HEIGHT:-2}"

CA_FILE="${CA_FILE:-/Users/pen/orama-stagenet-handoff/le-roots.pem}"
GATEWAY_URL="${GATEWAY_URL:-https://stagenet.dbrsteting.bid}"
SHIELDED_SCENARIO="${SHIELDED_SCENARIO:-}"

if ! [[ "$VOTE_EXTENSIONS_ENABLE_HEIGHT" =~ ^[0-9]{1,15}$ ]]; then
	echo "invalid VOTE_EXTENSIONS_ENABLE_HEIGHT (expected a plain integer): $VOTE_EXTENSIONS_ENABLE_HEIGHT" >&2
	exit 1
fi

# Validator addresses come back from commands run on the remote nodes; they are validated against
# this before ever being substituted into another remote command.
ADDR_RE='^orama1[02-9ac-hj-np-z]{38}$'

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="$(cd "$here/../.." && pwd)"
core_root="$(cd "$chain_root/../core" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

log() { printf '==> %s\n' "$*"; }
field() { echo "$1" | cut -d: -f"$2"; }

validate() {
	local value="$1" re="$2" what="$3"
	if ! [[ "$value" =~ $re ]]; then
		echo "invalid $what: $value" >&2
		exit 1
	fi
}

# on <alias> <command-string>: runs a fixed, script-authored command string on the remote host.
# Only ever pass a literal string built from this script's own constants here - never a value
# derived from remote output or user input; use remote_run for that instead.
on() {
	local alias="$1"
	shift
	ssh -o BatchMode=yes -o ServerAliveInterval=15 "$alias" "$@"
}

# remote_run <alias> <arg>...: shell-quotes every argument with printf %q before joining them into
# the command line ssh sends, so a value that happens to contain shell metacharacters (spaces,
# quotes, `;`, backticks, ...) is passed through literally instead of being interpreted - this is
# what actually makes it safe to use address_of/node_id output as command arguments. Stdin passes
# through to the remote command.
remote_run() {
	local alias="$1"
	shift
	local quoted="" a
	for a in "$@"; do
		quoted="$quoted $(printf '%q' "$a")"
	done
	on "$alias" "$quoted"
}

as_chain() {
	local alias="$1"
	shift
	remote_run "$alias" sudo -u "$SVC_USER" "$BIN_DIR/oramad" --home "$HOME_DIR" "$@"
}

# put_root_file <alias> <mode> <dest>: writes stdin, gzip-compressed on the wire, to <dest> as root.
# <dest> must be in a root-owned directory nobody else writes (the staging directory, the tools
# directory): no intermediate file of any predictable name ever touches the remote disk.
put_root_file() {
	local alias="$1" mode="$2" dest="$3"
	gzip -c | on "$alias" "gunzip -c | sudo sh -c 'umask 022; tmp=\$(mktemp $dest.XXXXXX) && cat > \"\$tmp\" && chmod $mode \"\$tmp\" && mv -f \"\$tmp\" $dest'"
}

# run_remote_script <alias> <script> <arg>...: puts a script of remote/ in the root-owned tools
# directory and runs it as root with the arguments. It is a file, not `bash -s`, so no command in it
# can read the rest of the script from stdin.
run_remote_script() {
	local alias="$1" script="$2"
	shift 2
	on "$alias" "sudo install -d -m 0755 -o root -g root $TOOLS_DIR"
	put_root_file "$alias" 0755 "$TOOLS_DIR/$script" < "$here/remote/$script"
	remote_run "$alias" sudo "$TOOLS_DIR/$script" "$@" < /dev/null
}

# ------------------------------------------------------------------------------------------------
# host tools

# build_host_tools compiles what runs on this machine: stagenetctl, and the orama CLI for this OS
# (`orama storage` runs here, against the nodes).
build_host_tools() {
	log "building stagenetctl and the orama CLI for this machine"
	(cd "$chain_root" && go build -o "$work/stagenetctl" ./scripts/stagenet/smoke)
	(cd "$core_root" && go build -o "$work/orama-host" ./cmd/orama/)
}

# ------------------------------------------------------------------------------------------------
# node tools

# stage_tools installs the helper `register` and `smoke` run on the node, in a root-owned
# directory of its own.
stage_tools() {
	local alias="$1" name="$2"
	log "[$name] installing stagenet-node in $TOOLS_DIR"
	on "$alias" "sudo install -d -m 0755 -o root -g root $TOOLS_DIR"
	put_root_file "$alias" 0755 "$TOOLS_DIR/stagenet-node" < "$work/stagenet-node"
}

address_of() {
	local addr
	addr="$(as_chain "$1" keys show validator -a --keyring-backend test)"
	validate "$addr" "$ADDR_RE" "validator address from $1"
	echo "$addr"
}


cmd_status() {
	local n alias
	for n in "${NODES[@]}"; do
		alias="$(field "$n" 2)"
		printf '%-9s ' "$(field "$n" 1)"
		remote_run "$alias" curl -s --max-time 5 "$RPC_HTTP/status" | python3 -c 'import json,sys; s=json.load(sys.stdin)["result"]; print("height", s["sync_info"]["latest_block_height"], "catching_up", s["sync_info"]["catching_up"])' 2>/dev/null || echo 'not responding'
		remote_run "$alias" sudo "$BIN_DIR/orama" global status < /dev/null || echo "orama global status failed on $(field "$n" 1)"
	done
}

# INVARIANT_MODULES are the modules whose `oramad query <module> invariants` must hold on every
# node after a deploy (docs/SECURITY_PLAYBOOKS.md). A literal list: nothing from remote output is
# spliced into the remote command. The same list is stagenetctl's (scripts/stagenet/smoke).
#
# Every module that holds or moves norama has an invariants query and is listed here:
# emission, fees, storage, nodes, relay, houses, token, market (bid escrow) and power (its
# pass-through account is empty), and shielded (the pool). x/cnft and x/archive hold no norama of
# their own: cNFT deposits sit in x/fees' deposits account and archive payments go through x/storage.
INVARIANT_MODULES=(emission fees storage nodes relay houses token market power shielded)

# cmd_invariants runs every module's invariant query on every node and fails if any query fails
# or reports a broken invariant (each response carries booleans that must all be true).
cmd_invariants() {
	local failed=0
	for n in "${NODES[@]}"; do
		local alias name; alias="$(field "$n" 2)"; name="$(field "$n" 1)"
		for m in "${INVARIANT_MODULES[@]}"; do
			local out
			# stdout only: a warning on stderr must not be parsed as the answer. The query runs as the ssh
			# login user, an allowed chain client: the co-located host rules drop the chain's own account
			# (RUN_A_GLOBAL_NODE.md), and a query needs nothing from the chain's home.
			if ! out="$(remote_run "$alias" "$BIN_DIR/oramad" query "$m" invariants --node "$RPC_ADDR" --output json)"; then
				printf '%-9s %-9s query failed: %s\n' "$name" "$m" "$out"
				failed=1
				continue
			fi
			if echo "$out" | python3 -c 'import json,sys
d=json.load(sys.stdin)
checks=[v for v in d.values() if isinstance(v,bool)]
sys.exit(0 if checks and all(checks) else 1)'; then
				printf '%-9s %-9s ok\n' "$name" "$m"
			else
				printf '%-9s %-9s BROKEN %s\n' "$name" "$m" "$out"
				failed=1
			fi
		done
	done
	return "$failed"
}

# ------------------------------------------------------------------------------------------------
# smoke

nodes_spec() {
	local spec="" n
	for n in "${NODES[@]}"; do
		spec="$spec${spec:+,}$(field "$n" 1)=$(field "$n" 2)=$(field "$n" 3)"
	done
	echo "$spec"
}

cmd_smoke() {
	(cd "$chain_root" && make build-linux-amd64-global)
	cp "$chain_root/build/stagenet-node-linux-amd64" "$work/stagenet-node"
	build_host_tools
	local n
	for n in "${NODES[@]}"; do stage_tools "$(field "$n" 2)" "$(field "$n" 1)"; done
	local extra=()
	[ -n "$SHIELDED_SCENARIO" ] && extra+=(--scenario "$SHIELDED_SCENARIO")
	"$work/stagenetctl" smoke --chain-id "$CHAIN_ID" --nodes "$(nodes_spec)" --orama "$work/orama-host" \
		--ca-file "$CA_FILE" --gateway "$GATEWAY_URL" --vote-ext-height "$VOTE_EXTENSIONS_ENABLE_HEIGHT" ${extra[@]+"${extra[@]}"}
}

# cmd_gen_shielded builds the shielded wallet's scenario for this chain: the operator that signs
# the unshield, amounts that clear the chain's floors and a transfer fee sized to its parameters.
cmd_gen_shielded() {
	build_host_tools
	local first_alias operator out="$chain_root/build/stagenet-shielded-scenario.json"
	first_alias="$(field "${NODES[0]}" 2)"
	operator="$(address_of "$first_alias")"
	local envlines line assignments=()
	envlines="$("$work/stagenetctl" shielded-env --chain-id "$CHAIN_ID" --nodes "$(nodes_spec)" --operator "$operator")"
	while IFS= read -r line; do assignments+=("$line"); done <<< "$envlines"
	log "building the scenario (about a minute of proving)"
	mkdir -p "$chain_root/build"
	(cd "$chain_root/x/shielded/wallet" && env "${assignments[@]}" cargo run --release --example gen_scenario) > "$out"
	echo "scenario written to $out; run: SHIELDED_SCENARIO=$out $0 smoke"
}

# ------------------------------------------------------------------------------------------------
# reset

# cmd_reset removes the global install from every node: see remote/reset-node.sh. It is safe to run
# on a node with no install, with a half-finished one, or with the legacy direct-unit chain.
cmd_reset() {
	local n
	for n in "${NODES[@]}"; do
		log "[$(field "$n" 1)] removing the global install and its state"
		run_remote_script "$(field "$n" 2)" reset-node.sh
	done
}

case "${1:-}" in
	reset) cmd_reset ;;
	status) cmd_status ;;
	invariants) cmd_invariants ;;
	smoke) cmd_smoke ;;
	gen-shielded) cmd_gen_shielded ;;
	*) echo "usage: $0 reset|status|invariants|smoke|gen-shielded" >&2; exit 2 ;;
esac
