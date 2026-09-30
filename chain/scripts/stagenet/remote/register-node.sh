#!/usr/bin/env bash
# Runs on a stagenet node, as root (deploy.sh register stages and runs it with sudo).
# Registers this node's operator, node and roles on the stagenet chain through the real product
# commands: `orama global bind`, `register`, `bond` and `capacity`. Those commands sign through the
# RootWallet agent protocol, so this script runs stagenet-node's stand-in agent for the length of the
# run, fed the operator key straight from oramad's test keyring: the key crosses one pipe on this
# host and is never printed, stored or copied off it. Stagenet only (deploy.sh checks the chain id).
#
# Arguments: chain-id node-id public-ip asn storage-bond archiver-bond capacity-bytes hot-key-fund
#            tx-gas   (amounts in norama; deploy.sh validates every one)
#
# The chain's RPC (31001) and REST API (31003) listen on the orama-global namespace address 198.18.0.2,
# which this host reaches directly. Every step is idempotent: a re-run skips what the chain already
# holds and never repeats a transaction.
set -euo pipefail

CHAIN_ID=$1 NODE_ID=$2 PUBLIC_IP=$3 ASN=$4 STORAGE_BOND=$5 ARCHIVER_BOND=$6
CAPACITY_BYTES=$7 HOT_KEY_FUND=$8 TX_GAS=$9

BIN_DIR=/usr/lib/orama-global/bin
ORAMAD=$BIN_DIR/oramad
ORAMA=$BIN_DIR/orama
HELPER=/usr/local/lib/orama-stagenet/stagenet-node
CHAIN_HOME=/var/lib/orama-global/chain
PROVIDER_HOME=/var/lib/orama-global/provider
ARCHIVER_HOME=/var/lib/orama-global/archiver
AGENT_DIR=/run/orama-stagenet
AGENT_SOCK=$AGENT_DIR/agent.sock
# The chain listens on the orama-global namespace address, which this host reaches over the veth
# pair (core/pkg/constants, GlobalNetnsAddr); its loopback is the namespace's own.
CHAIN_HOST=198.18.0.2
RPC=tcp://$CHAIN_HOST:31001
RPC_HTTP=http://$CHAIN_HOST:31001
REST=http://$CHAIN_HOST:31003
PROVIDER_PORT=31013
# What the node record's deposit and the transaction fees can take beyond the bonds and the hot-key
# fund, in norama (5 ORAMA). A shortfall is reported before anything is sent.
EARNINGS_MARGIN=5000000000
POLL_SECONDS=90
# The agent stops by itself after this long even if this script is killed without cleaning up.
AGENT_TTL=1h
POLL_STEP=2

log() { printf '[%s] %s\n' "$NODE_ID" "$*"; }
die() { printf '[%s] ERROR: %s\n' "$NODE_ID" "$*" >&2; exit 1; }

# Every number below goes into shell arithmetic and onto a command line. deploy.sh validates them
# before it stages this script, but this script runs as root on the node and does not rely on that:
# a value that is not a plain decimal of at most 15 digits with no leading zero (bash would read 010 as octal, and no sum of them may overflow) is refused
# before anything is changed.
for v in ASN STORAGE_BOND ARCHIVER_BOND CAPACITY_BYTES HOT_KEY_FUND TX_GAS; do
	[[ ${!v} =~ ^(0|[1-9][0-9]{0,14})$ ]] || die "$v is not a plain number of at most 15 digits without leading zeros: ${!v}"
done

# tx_fee is the fee of one `orama global` transaction at the chain's current base fee, with no tip:
# the operator pays from earnings, and x/fees pays a tip only from a bank balance. It is read right
# before each transaction, since the base fee moves with load.
tx_fee() {
	local f
	f=$("$HELPER" tx-fee --rpc "$RPC" --gas "$TX_GAS")
	[[ $f =~ ^[1-9][0-9]{0,14}$ ]] || die "the transaction fee read as '$f', not a positive number of at most 15 digits"
	printf '%s\n' "$f"
}

# The operator key, as one hex line, from oramad's test keyring. Anything else it prints is ignored
# by the helper.
operator_key() {
	runuser -u orama-chain -- "$ORAMAD" keys export validator --unarmored-hex --unsafe -y \
		--keyring-backend test --home "$CHAIN_HOME" 2>&1
}
node_field() { "$HELPER" node-status --rpc "$RPC" --id "$NODE_ID" | sed -n "s/^$1=//p"; }

# poll_field <field> <want> <what>: waits for the chain to show the transaction's effect. The next
# transaction reads the account sequence from the chain, so it must not go out before this lands.
poll_field() {
	local waited=0 got
	while [ "$waited" -lt "$POLL_SECONDS" ]; do
		got=$(node_field "$1")
		[ "$got" = "$2" ] && return 0
		sleep "$POLL_STEP"
		waited=$((waited + POLL_STEP))
	done
	die "$3: the chain shows $1=$got after ${POLL_SECONDS}s, want $2"
}

work=$(mktemp -d /run/orama-stagenet-reg.XXXXXX)
agent_pid=
cleanup() {
	# A failed run must not leave a registered node's services stopped.
	if [ "$(node_field exists 2>/dev/null || true)" = true ]; then
		"$ORAMA" global start provider archiver || true
	fi
	if [ -n "$agent_pid" ]; then kill "$agent_pid" 2>/dev/null || true; fi
	rm -rf "$work" "$AGENT_SOCK"
}
trap cleanup EXIT

[ -x "$HELPER" ] || die "$HELPER is not installed (deploy.sh stages it)"
operator=$(runuser -u orama-chain -- "$ORAMAD" keys show validator -a --keyring-backend test --home "$CHAIN_HOME")
[ -n "$operator" ] || die "no validator key in oramad's test keyring at $CHAIN_HOME"
log "operator $operator"

# --- the hot key: the provider's, shared with the archiver -----------------------------------
# A node has one hot key, and both services sign as it. The provider creates it on first start;
# the archiver is given a copy before it is ever pointed at a node id.
"$ORAMA" global stop provider archiver
if [ ! -f "$PROVIDER_HOME/hot-key" ]; then
	"$ORAMA" global start provider
	waited=0
	while [ ! -f "$PROVIDER_HOME/hot-key" ]; do
		[ "$waited" -lt "$POLL_SECONDS" ] || die "the provider did not create $PROVIDER_HOME/hot-key"
		sleep "$POLL_STEP"
		waited=$((waited + POLL_STEP))
	done
	"$ORAMA" global stop provider
fi
[ ! -L "$PROVIDER_HOME/hot-key" ] || die "$PROVIDER_HOME/hot-key is a symlink"
install -d -o orama-archiver -g orama-archiver -m 0700 "$ARCHIVER_HOME"
# Read as the provider and write as the archiver, never as root: root would follow a symlink either
# service planted in its own directory.
# shellcheck disable=SC2016 # the inner shell expands $1 and $tmp
runuser -u orama-provider -- cat "$PROVIDER_HOME/hot-key" |
	runuser -u orama-archiver -- sh -c 'umask 077; tmp=$(mktemp "$1/.hk.XXXXXX") && cat >"$tmp" && mv -f "$tmp" "$1/hot-key"' _ "$ARCHIVER_HOME"
hot_key=$("$HELPER" address --key-file "$PROVIDER_HOME/hot-key")
log "hot key $hot_key"

# --- the signing agent -----------------------------------------------------------------------
install -d -m 0700 "$AGENT_DIR"
"$HELPER" agent --ttl "$AGENT_TTL" --listen "$AGENT_SOCK:0" < <(operator_key) &
agent_pid=$!
for _ in $(seq 30); do
	[ -S "$AGENT_SOCK" ] && break
	sleep 1
done
[ -S "$AGENT_SOCK" ] || die "the signing agent did not start"

# --- earnings must cover the bonds --------------------------------------------------------------
earnings=$("$HELPER" earnings --rpc "$RPC" --address "$operator")
[[ $earnings =~ ^(0|[1-9][0-9]{0,14})$ ]] || die "the operator's earnings read as '$earnings', not a number the chain could hold"
fee=$(tx_fee)
need=$((STORAGE_BOND + ARCHIVER_BOND + HOT_KEY_FUND + EARNINGS_MARGIN + 6 * fee))
if [ "$(node_field exists)" != true ] && [ "$earnings" -lt "$need" ]; then
	die "the operator's earnings are $earnings norama; registering needs about $need. Let the chain run longer (more epochs) and retry"
fi

# --- operator, node, bonds, capacity ----------------------------------------------------------
operator_key | "$HELPER" register-operator --rpc "$RPC"

if [ "$(node_field exists)" != true ]; then
	"$ORAMA" global bind --chain-id "$CHAIN_ID" --operator "$operator" --service hot-key \
		--key-file "$PROVIDER_HOME/hot-key" --key-type secp256k1 >"$work/hot-key.binding.json"
	fee=$(tx_fee)
	env RW_AGENT_SOCK="$AGENT_SOCK" "$ORAMA" global register \
		--chain-id "$CHAIN_ID" --operator "$operator" --id "$NODE_ID" --hot-key "$hot_key" \
		--role storage --role archiver --binding "$work/hot-key.binding.json" \
		--endpoint "http://$PUBLIC_IP:$PROVIDER_PORT" --asn "$ASN" \
		--fee "$fee" --gas "$TX_GAS" --node "$REST"
	poll_field exists true "register"
else
	log "node already registered"
fi

bond() {
	local role=$1 amount=$2 field=$3
	if [ "$(node_field "$field")" = 0 ]; then
		fee=$(tx_fee)
		env RW_AGENT_SOCK="$AGENT_SOCK" "$ORAMA" global bond \
			--chain-id "$CHAIN_ID" --operator "$operator" --id "$NODE_ID" --role "$role" --amount "$amount" \
			--fee "$fee" --gas "$TX_GAS" --node "$REST"
		poll_field "$field" "$amount" "bond $role"
	else
		log "$role already bonded"
	fi
}
bond storage "$STORAGE_BOND" storage_bond
bond archiver "$ARCHIVER_BOND" archiver_bond

# The hot key pays the provider's and the archiver's transaction fees from a fee-only balance the
# operator gives it, once.
if [ "$("$HELPER" fee-balance --rpc "$RPC" --address "$hot_key")" = 0 ]; then
	"$HELPER" fund-hot-key --rpc "$RPC" --node-id "$NODE_ID" --amount "$HOT_KEY_FUND" < <(operator_key)
else
	log "hot key already funded"
fi

if [ "$(node_field capacity)" != "$CAPACITY_BYTES" ]; then
	fee=$(tx_fee)
	env RW_AGENT_SOCK="$AGENT_SOCK" "$ORAMA" global capacity \
		--chain-id "$CHAIN_ID" --operator "$operator" --id "$NODE_ID" --bytes "$CAPACITY_BYTES" \
		--fee "$fee" --gas "$TX_GAS" --node "$REST"
	poll_field capacity "$CAPACITY_BYTES" "declare capacity"
fi

# --- point the services at the node and start them -----------------------------------------------
for svc in provider archiver; do
	dir=/var/lib/orama-global/$svc
	# shellcheck disable=SC2016 # the inner shell expands $1 and $tmp
	printf '%s\n' "$NODE_ID" |
		runuser -u "orama-$svc" -- sh -c 'umask 022; tmp=$(mktemp "$1/.nid.XXXXXX") && cat >"$tmp" && mv -f "$tmp" "$1/node-id"' _ "$dir"
done
"$ORAMA" global start provider archiver

log "node record:"
"$ORAMA" chain node "$NODE_ID" --rpc "$RPC_HTTP"
