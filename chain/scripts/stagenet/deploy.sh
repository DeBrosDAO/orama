#!/usr/bin/env bash
# Stand up (or reset) the Orama L1 devnet on the stagenet nodes.
#
# This is the interim deployment path until the global-node role (plan B2/B3) lets
# `orama node install --role global` manage oramad, and it drives its own systemd unit directly
# (see docs/CHAIN.md) rather than the `orama` CLI's node lifecycle commands, because that role does
# not exist yet. It only touches the chain's own user, binary, state directory and systemd unit;
# it never touches an Orama cluster service. Stagenet/devnet only: CHAIN_ID must say so, and the
# script refuses to run otherwise. keyring-backend test (an unencrypted, on-disk keyring) is used
# throughout: it is a devnet-only convenience, never appropriate once real value is at stake.
#
# Usage: deploy.sh up | status | invariants | reset
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

DENOM="norama"
# name:ssh-alias:wireguard-ip
NODES=("athena:athena:10.0.0.1" "superman:superman:10.0.0.2" "poseidon:poseidon:10.0.0.3")
P2P_PORT=31000
RPC_PORT=31001
GRPC_PORT=31002
API_PORT=31003
PROM_PORT=31004
BIN_DIR="/usr/lib/orama-global/bin"
HOME_DIR="/var/lib/orama-global/chain"
SVC_USER="orama-chain"
UNIT="orama-global-chain.service"
EPOCH_DURATION="${EPOCH_DURATION:-300s}"
EPOCH_MIN_BLOCKS="${EPOCH_MIN_BLOCKS:-10}"
# C13 inclusion lists: the height vote extensions turn on at, patched into genesis (see build_genesis).
VOTE_EXTENSIONS_ENABLE_HEIGHT="${VOTE_EXTENSIONS_ENABLE_HEIGHT:-2}"

# EPOCH_DURATION/EPOCH_MIN_BLOCKS reach a remote CLI flag value: keep them to a safe, boring
# syntax (Go duration / plain integer) before they do.
if ! [[ "$EPOCH_DURATION" =~ ^[0-9]+(h|m|s)$ ]]; then
	echo "invalid EPOCH_DURATION (expected e.g. 300s, 5m, 1h): $EPOCH_DURATION" >&2
	exit 1
fi
if ! [[ "$EPOCH_MIN_BLOCKS" =~ ^[0-9]+$ ]]; then
	echo "invalid EPOCH_MIN_BLOCKS (expected a plain integer): $EPOCH_MIN_BLOCKS" >&2
	exit 1
fi

# Validator addresses, node IDs and consensus pubkeys come back from commands run on the remote
# nodes; they are validated against these before ever being substituted into another remote
# command. CONSENSUS_PUBKEY_RE matches the base64 encoding of exactly 32 bytes (a raw ed25519 key):
# ceil(32/3)*4 = 44 characters, with one '=' padding character since 32 mod 3 == 2.
ADDR_RE='^orama1[02-9ac-hj-np-z]{38}$'
NODE_ID_RE='^[0-9a-f]{40}$'
CONSENSUS_PUBKEY_RE='^[A-Za-z0-9+/]{43}=$'

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="$(cd "$here/../.." && pwd)"
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
# what actually makes it safe to use address_of/node_id output as command arguments.
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

# put_file <alias> <mode> <dest>: writes stdin to <dest>, which must be under $HOME_DIR. It runs as
# $SVC_USER, never root: the chain user owns $HOME_DIR, so a root process following a symlink the
# chain process planted there could be tricked into reading or writing a host file. The bytes go
# to a temp file next to <dest>, get their mode, then are renamed over <dest>. `install /dev/stdin`
# isn't used because the uutils coreutils `install` shipped with Ubuntu 26.04 fails when <dest>
# already exists.
put_file() {
	local alias="$1" mode="$2" dest="$3"
	on "$alias" "sudo -u $SVC_USER sh -c 'umask 077; tmp=\$(mktemp \"\$(dirname $dest)/.put.XXXXXX\") && cat > \"\$tmp\" && chmod $mode \"\$tmp\" && mv -f \"\$tmp\" $dest'"
}

# build compiles oramad for linux/amd64 with CosmWasm AND the Orchard verifier in one static binary
# (`make build-linux-amd64-full`): the genesis stores the standard contracts, so a node without libwasmvm
# could not start (a nowasm binary refuses a genesis that has a wasm module). The native library is
# built from source, libwasmvm from the module cache at the pinned version plus the Orchard crate,
# through zig (chain/native/build.sh); nothing prebuilt is downloaded, and its sha256 is recorded in
# chain/native/libwasmvm_muslc.x86_64.a.sha256.
build() {
	log "building oramad for linux/amd64 with libwasmvm and the Orchard verifier ($VERSION, $COMMIT)"
	(cd "$chain_root" && make build-linux-amd64-full)
	cp "$chain_root/build/oramad-linux-amd64-full" "$work/oramad"
}

install_node() {
	local alias="$1" name="$2"
	log "[$name] installing binary, user and state directory"
	# Transferred gzip-compressed into a temp file inside BIN_DIR (root-only), then renamed: no intermediate
	# file of any name (predictable or not) is ever written to the remote disk, avoiding both the
	# earlier /tmp size stall and any TOCTOU/symlink risk a shared /tmp path would carry.
	on "$alias" "sudo install -d -m 0755 $BIN_DIR"
	gzip -c "$work/oramad" | on "$alias" "gunzip -c | sudo sh -c 'umask 022; tmp=\$(mktemp $BIN_DIR/.oramad.XXXXXX) && cat > \"\$tmp\" && chmod 0755 \"\$tmp\" && mv -f \"\$tmp\" $BIN_DIR/oramad'"
	on "$alias" "id $SVC_USER >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin $SVC_USER"
	on "$alias" "sudo install -d -m 0700 -o $SVC_USER -g $SVC_USER $HOME_DIR"
	# The state directory is mode 0700 and owned by the chain user, so the SSH
	# login cannot see genesis.json. The check has to run as that user or a
	# second deploy tries to init again and dies on the file that is already there.
	if ! on "$alias" "sudo -u $SVC_USER test -f $HOME_DIR/config/genesis.json"; then
		remote_run "$alias" sudo -u "$SVC_USER" "$BIN_DIR/oramad" init "$name" --chain-id "$CHAIN_ID" --default-denom "$DENOM" --home "$HOME_DIR"
	fi
	if ! on "$alias" "sudo -u $SVC_USER $BIN_DIR/oramad keys show validator --keyring-backend test --home $HOME_DIR >/dev/null 2>&1"; then
		as_chain "$alias" keys add validator --keyring-backend test --no-backup >/dev/null
	fi
}

address_of() {
	local addr
	addr="$(as_chain "$1" keys show validator -a --keyring-backend test)"
	validate "$addr" "$ADDR_RE" "validator address from $1"
	echo "$addr"
}

node_id() {
	local id
	id="$(as_chain "$1" comet show-node-id)"
	validate "$id" "$NODE_ID_RE" "node id from $1"
	echo "$id"
}

# consensus_pubkey_of extracts a node's raw base64 ed25519 consensus pubkey via `oramad comet
# show-validator` (which reads only priv_validator_key.json's PUBLIC half) - its private key
# material never leaves the node, and never touches this script's local disk.
consensus_pubkey_of() {
	local key
	key="$(as_chain "$1" comet show-validator | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
	validate "$key" "$CONSENSUS_PUBKEY_RE" "consensus pubkey from $1"
	echo "$key"
}

build_genesis() {
	local first_alias; first_alias="$(field "${NODES[0]}" 2)"
	log "building genesis on $first_alias"

	# Genesis starts at exactly zero norama supply: every node is a member of x/power's bootstrap
	# committee (plans/open-network.md D16), which needs no self-bond and no gentx - replacing the
	# old devnet-only self-bonded-validator exception (see docs/CHAIN.md).
	as_chain "$first_alias" genesis set-emission-params \
		--epoch-duration "$EPOCH_DURATION" --min-blocks-per-epoch "$EPOCH_MIN_BLOCKS" --allow-bootstrap-stake

	local first=true
	for n in "${NODES[@]}"; do
		local alias name
		alias="$(field "$n" 2)"
		name="$(field "$n" 1)"
		local addr pubkey
		addr="$(address_of "$alias")"
		pubkey="$(consensus_pubkey_of "$alias")"
		# An empty array expansion is an unbound variable under bash 3.2 with set -u,
		# so the first node (the only one that sets the committee size) is a separate call.
		if [ "$first" = true ]; then
			first=false
			as_chain "$first_alias" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey" \
				--min-committee-size "${#NODES[@]}"
		else
			as_chain "$first_alias" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey"
		fi
	done

	# The standard contracts (CW20, CW721, escrow, CW3 multisig, vesting) are stored in genesis so they
	# exist from height 1 although upload is closed until the sunset height (docs/CHAIN.md).
	as_chain "$first_alias" genesis add-standard-contracts

	as_chain "$first_alias" genesis validate
	on "$first_alias" "sudo -u $SVC_USER cat $HOME_DIR/config/genesis.json" > "$work/genesis.json"

	# x/consensus has no genesis state of its own (it's driven by the top-level "consensus" field
	# of genesis.json, which oramad's own module wiring can't default): set a finite block max_gas
	# here, locally, before this genesis is distributed to every node, so the devnet doesn't run
	# with CometBFT's own unlimited default.
	python3 -c "
import json
path = '$work/genesis.json'
with open(path) as f:
    doc = json.load(f)
params = doc.setdefault('consensus', {}).setdefault('params', {})
params.setdefault('block', {})['max_gas'] = '100000000'
# C13 inclusion lists: vote extensions are a genesis-only switch here (every consensus-param
# authority is UnreachableAuthority), so this is where stagenet turns them on. Height 2 leaves
# block 1 as an ordinary block and exercises the enable transition (extensions from 2, the
# injected extended commit from 3). See docs/CHAIN.md, C13.
params.setdefault('abci', {})['vote_extensions_enable_height'] = '$VOTE_EXTENSIONS_ENABLE_HEIGHT'
with open(path, 'w') as f:
    json.dump(doc, f, indent=2)
"
}

# assert_set <alias> <file> <grep-pattern> <what>: fails loudly if a config edit didn't actually
# take effect (e.g. because the upstream template changed and a sed pattern no longer matches),
# instead of silently leaving a node's config unset while the script marches on.
assert_set() {
	local alias="$1" file="$2" pattern="$3" what="$4"
	if ! on "$alias" "sudo -u $SVC_USER grep -q -- $(printf '%q' "$pattern") $file"; then
		echo "failed to set $what in $file on $alias (pattern not found after edit: $pattern)" >&2
		exit 1
	fi
}

configure_node() {
	local alias="$1" name="$2" wgip="$3" peers="$4"
	log "[$name] distributing genesis and writing config"
	put_file "$alias" 0600 "$HOME_DIR/config/genesis.json" < "$work/genesis.json"
	on "$alias" "sudo -u $SVC_USER sed -i \
		-e 's#^laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://$wgip:$P2P_PORT\"#' \
		-e 's#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:$RPC_PORT\"#' \
		-e 's#^persistent_peers = .*#persistent_peers = \"$peers\"#' \
		-e 's#^addr_book_strict = true#addr_book_strict = false#' \
		-e 's#^pex = true#pex = false#' \
		-e 's#^external_address = .*#external_address = \"$wgip:$P2P_PORT\"#' \
		-e 's#^prometheus_listen_addr = .*#prometheus_listen_addr = \"127.0.0.1:$PROM_PORT\"#' \
		-e 's#^prometheus = false#prometheus = true#' \
		$HOME_DIR/config/config.toml"
	assert_set "$alias" "$HOME_DIR/config/config.toml" "laddr = \"tcp://$wgip:$P2P_PORT\"" "the p2p listen address"
	assert_set "$alias" "$HOME_DIR/config/config.toml" "laddr = \"tcp://127.0.0.1:$RPC_PORT\"" "the RPC listen address"
	assert_set "$alias" "$HOME_DIR/config/config.toml" "pex = false" "pex"

	on "$alias" "sudo -u $SVC_USER sed -i \
		-e 's#^address = \"tcp://localhost:1317\"#address = \"tcp://127.0.0.1:$API_PORT\"#' \
		-e 's#^address = \"localhost:9090\"#address = \"127.0.0.1:$GRPC_PORT\"#' \
		-e 's#^pruning = .*#pruning = \"custom\"#' \
		-e 's#^pruning-keep-recent = .*#pruning-keep-recent = \"100\"#' \
		-e 's#^pruning-interval = .*#pruning-interval = \"10\"#' \
		-e 's#^min-retain-blocks = .*#min-retain-blocks = 201600#' \
		-e 's#^app-db-backend = .*#app-db-backend = \"pebbledb\"#' \
		-e '/^\[api\]/,/^\[/ s#^enable = false#enable = true#' \
		$HOME_DIR/config/app.toml"
	assert_set "$alias" "$HOME_DIR/config/app.toml" "address = \"127.0.0.1:$GRPC_PORT\"" "the gRPC listen address"
	assert_set "$alias" "$HOME_DIR/config/app.toml" "app-db-backend = \"pebbledb\"" "the app-db-backend"
	# 201600 blocks is 14 days at 6 seconds (x/archive DefaultBlocksIn14Days). oramad's Commit
	# never returns a retain height above the last archived height, so this prunes nothing while
	# no range is archived.
	assert_set "$alias" "$HOME_DIR/config/app.toml" "min-retain-blocks = 201600" "min-retain-blocks"
	# The REST API (loopback 31003) is off by default; the gateway's /v1/chain/
	# proxy and the node monitor read it.
	if ! on "$alias" "sudo -u $SVC_USER awk '/^\[api\]/{a=1;next} /^\[/{a=0} a && /^enable = true/{f=1} END{exit !f}' $HOME_DIR/config/app.toml"; then
		echo "failed to enable the REST API in $HOME_DIR/config/app.toml on $alias" >&2
		exit 1
	fi
}

write_unit() {
	local alias="$1"
	shift
	# The remaining args are this node's own WireGuard IP plus every other node's, so
	# IPAddressAllow is exactly localhost plus the cluster's own overlay addresses - never the
	# whole 10.0.0.0/24.
	local wg_ips="$*"
	on "$alias" "sudo tee /etc/systemd/system/$UNIT >/dev/null" <<EOF
[Unit]
Description=Orama L1 node (oramad)
After=network-online.target wg-quick@wg0.service
Wants=network-online.target

[Service]
User=$SVC_USER
Group=$SVC_USER
ExecStart=$BIN_DIR/oramad start --home $HOME_DIR
Restart=always
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectClock=yes
ProtectKernelLogs=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectHostname=yes
ProtectProc=invisible
RestrictNamespaces=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=yes
UMask=0077
CapabilityBoundingSet=
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
ReadWritePaths=$HOME_DIR
TemporaryFileSystem=/opt/orama:ro
InaccessiblePaths=-/var/lib/orama-unit-env -/etc/wireguard -/etc/orama -/var/lib/orama-gateway-keys -/var/lib/orama-deploy -/var/lib/orama-deploy-acme-api -/var/lib/orama-tor -/var/lib/caddy
IPAddressDeny=any
IPAddressAllow=localhost $wg_ips

[Install]
WantedBy=multi-user.target
EOF
	on "$alias" "sudo systemctl daemon-reload && sudo systemctl enable --now $UNIT"
}

cmd_up() {
	build
	for n in "${NODES[@]}"; do install_node "$(field "$n" 2)" "$(field "$n" 1)"; done
	build_genesis
	local peers=() all_ips=()
	for n in "${NODES[@]}"; do
		local id; id="$(node_id "$(field "$n" 2)")"
		peers+=("$id@$(field "$n" 3):$P2P_PORT")
		all_ips+=("$(field "$n" 3)")
	done
	local peer_list; peer_list="$(IFS=,; echo "${peers[*]}")"
	for n in "${NODES[@]}"; do
		configure_node "$(field "$n" 2)" "$(field "$n" 1)" "$(field "$n" 3)" "$peer_list"
		write_unit "$(field "$n" 2)" "${all_ips[@]}"
	done
	cmd_status
}

cmd_status() {
	for n in "${NODES[@]}"; do
		local alias; alias="$(field "$n" 2)"
		printf '%-9s ' "$(field "$n" 1)"
		on "$alias" "curl -s --max-time 5 http://127.0.0.1:$RPC_PORT/status | python3 -c 'import json,sys; s=json.load(sys.stdin)[\"result\"]; print(\"height\", s[\"sync_info\"][\"latest_block_height\"], \"catching_up\", s[\"sync_info\"][\"catching_up\"], \"peers-ok\")' 2>/dev/null || echo 'not responding'"
	done
}

# INVARIANT_MODULES are the modules whose `oramad query <module> invariants` must hold on every
# node after a deploy (docs/SECURITY_PLAYBOOKS.md). A literal list: nothing from remote output is
# spliced into the remote command.
#
# Every module that holds or moves norama has an invariants query and is listed here:
# emission, fees, storage, nodes, relay, houses, token, market (bid escrow) and power (its
# pass-through account is empty). x/cnft and x/archive hold no norama of their own: cNFT deposits
# sit in x/fees' deposits account and archive payments go through x/storage.
INVARIANT_MODULES=(emission fees storage nodes relay houses token market power shielded)

# cmd_invariants runs every module's invariant query on every node and fails if any query fails
# or reports a broken invariant (each response carries booleans that must all be true).
cmd_invariants() {
	local failed=0
	for n in "${NODES[@]}"; do
		local alias name; alias="$(field "$n" 2)"; name="$(field "$n" 1)"
		for m in "${INVARIANT_MODULES[@]}"; do
			local out
			# stdout only: a warning on stderr must not be parsed as the answer.
			if ! out="$(as_chain "$alias" query "$m" invariants --node "tcp://127.0.0.1:$RPC_PORT" --output json)"; then
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

# cmd_reset fully tears a node back down, including a node left in a partial state by an earlier
# aborted run (binary and/or state directory present but no unit installed yet, or vice versa):
# every step here tolerates the thing it's removing already being absent.
cmd_reset() {
	for n in "${NODES[@]}"; do
		local alias; alias="$(field "$n" 2)"
		log "[$(field "$n" 1)] stopping and wiping the chain install"
		on "$alias" "sudo systemctl disable --now $UNIT 2>/dev/null || true"
		on "$alias" "sudo rm -f /etc/systemd/system/$UNIT"
		on "$alias" "sudo systemctl daemon-reload"
		on "$alias" "sudo rm -rf $HOME_DIR"
		on "$alias" "sudo rm -f $BIN_DIR/oramad"
	done
}

case "${1:-}" in
	up) cmd_up ;;
	status) cmd_status ;;
	invariants) cmd_invariants ;;
	reset) cmd_reset ;;
	*) echo "usage: $0 up|status|invariants|reset" >&2; exit 2 ;;
esac
