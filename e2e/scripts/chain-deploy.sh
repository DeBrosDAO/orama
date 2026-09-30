#!/usr/bin/env bash
# Stand up (or reset) Orama L1 validators on the servers of one e2e fleet run.
#
# Adapted from chain/scripts/stagenet/deploy.sh (see docs/CHAIN.md, "The stagenet deploy script"),
# with the fleet passed in instead of hardcoded host aliases: every node is reached by its public IP
# with the run's own SSH key and pinned known_hosts, and the chain's p2p runs over the WireGuard
# overlay. It only touches the chain's own user, binary, state directory and systemd unit, never an
# Orama cluster service. Devnet only: CHAIN_ID must contain -devnet-. keyring-backend test (an
# unencrypted, on-disk keyring) is used throughout: a devnet-only convenience for throwaway keys.
#
# Environment (all required unless a default is given):
#   CHAIN_ID          e.g. orama-devnet-e2e-ab12cd; must match [a-z0-9-]{1,48} and contain -devnet-
#   E2E_CHAIN_NODES   space-separated name:public-ip:wireguard-ip, e.g. "node-1:203.0.113.1:10.0.0.1 ..."
#   E2E_SSH_KEY       the run's private key file
#   E2E_KNOWN_HOSTS   the run's pinned known_hosts file
#   E2E_SSH_USER      default root
#   EPOCH_DURATION    default 60s; EPOCH_MIN_BLOCKS default 5
#   CHAIN_ROOT        the chain module; default ../../chain from this script
#   CHAIN_READY_TIMEOUT  seconds `up` waits for every node to produce blocks; default 300
#
# Usage: chain-deploy.sh up | status | invariants | reset
set -euo pipefail

die() {
	echo "chain-deploy: $*" >&2
	exit 1
}

CHAIN_ID="${CHAIN_ID:-}"
if ! [[ "$CHAIN_ID" =~ ^[a-z0-9-]{1,48}$ ]]; then
	die "invalid CHAIN_ID (expected [a-z0-9-]{1,48}): '$CHAIN_ID'"
fi
case "$CHAIN_ID" in
*-devnet-*) ;;
*) die "refusing to run: CHAIN_ID must contain -devnet-, got: $CHAIN_ID" ;;
esac

DENOM="norama"
P2P_PORT=31000
RPC_PORT=31001
GRPC_PORT=31002
API_PORT=31003
PROM_PORT=31004
BIN_DIR="/usr/lib/orama-global/bin"
HOME_DIR="/var/lib/orama-global/chain"
SVC_USER="orama-chain"
UNIT="orama-global-chain.service"
EPOCH_DURATION="${EPOCH_DURATION:-60s}"
EPOCH_MIN_BLOCKS="${EPOCH_MIN_BLOCKS:-5}"
SSH_USER="${E2E_SSH_USER:-root}"
SSH_KEY="${E2E_SSH_KEY:-}"
KNOWN_HOSTS="${E2E_KNOWN_HOSTS:-}"
READY_TIMEOUT="${CHAIN_READY_TIMEOUT:-300}"
READY_POLL=5
SSH_CONNECT_TIMEOUT=15

# Formats every value is checked against before it reaches a remote command line. Validator
# addresses, node IDs and consensus pubkeys come back from remote commands; they are validated
# before being substituted into another. CONSENSUS_PUBKEY_RE is base64 of exactly 32 bytes.
ADDR_RE='^orama1[02-9ac-hj-np-z]{38}$'
NODE_ID_RE='^[0-9a-f]{40}$'
CONSENSUS_PUBKEY_RE='^[A-Za-z0-9+/]{43}=$'
NAME_RE='^[a-z0-9-]{1,32}$'
IPV4_RE='^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$'
WG_RE='^10\.0\.0\.(25[0-4]|2[0-4][0-9]|1[0-9]{2}|[1-9][0-9]?)$'
USER_RE='^[a-z_][a-z0-9_-]{0,31}$'
HEIGHT_RE='^[0-9]+$'

validate() {
	local value="$1" re="$2" what="$3"
	[[ "$value" =~ $re ]] || die "invalid $what: '$value'"
}

validate "$EPOCH_DURATION" '^[0-9]+(h|m|s)$' "EPOCH_DURATION (expected e.g. 60s)"
validate "$EPOCH_MIN_BLOCKS" '^[0-9]+$' "EPOCH_MIN_BLOCKS (expected a plain integer)"
validate "$READY_TIMEOUT" '^[0-9]+$' "CHAIN_READY_TIMEOUT (expected seconds)"
validate "$SSH_USER" "$USER_RE" "E2E_SSH_USER"
[ -f "$SSH_KEY" ] || die "E2E_SSH_KEY must name the run's private key file"
[ -f "$KNOWN_HOSTS" ] || die "E2E_KNOWN_HOSTS must name the run's known_hosts file"

# NODES holds name:public-ip:wireguard-ip entries, each field validated.
NODES=()
read -r -a raw_nodes <<<"${E2E_CHAIN_NODES:-}"
[ "${#raw_nodes[@]}" -ge 1 ] || die "E2E_CHAIN_NODES is empty"
for entry in "${raw_nodes[@]}"; do
	IFS=: read -r n_name n_ip n_wg n_extra <<<"$entry"
	[ -z "${n_extra:-}" ] || die "invalid E2E_CHAIN_NODES entry (want name:public-ip:wireguard-ip): '$entry'"
	validate "$n_name" "$NAME_RE" "node name in '$entry'"
	validate "$n_ip" "$IPV4_RE" "public IP in '$entry'"
	validate "${n_wg:-}" "$WG_RE" "WireGuard IP in '$entry'"
	NODES+=("$n_name:$n_ip:$n_wg")
done

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="${CHAIN_ROOT:-$here/../../chain}"
[ -f "$chain_root/go.mod" ] || die "CHAIN_ROOT '$chain_root' holds no go.mod"
chain_root="$(cd "$chain_root" && pwd)"
VERSION="$(git -C "$chain_root" describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git -C "$chain_root" rev-parse --short HEAD 2>/dev/null || echo unknown)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

log() { printf '==> %s\n' "$*"; }
field() { echo "$1" | cut -d: -f"$2"; }

# on <ip> <command-string>: runs a fixed, script-authored command string on the node, over SSH with
# the run's key and pinned host keys only. Never pass a value derived from remote output here; use
# remote_run for that.
on() {
	local ip="$1"
	shift
	ssh -F /dev/null -i "$SSH_KEY" -o IdentitiesOnly=yes -o BatchMode=yes \
		-o StrictHostKeyChecking=yes -o UserKnownHostsFile="$KNOWN_HOSTS" \
		-o ConnectTimeout="$SSH_CONNECT_TIMEOUT" -o ServerAliveInterval=15 \
		"$SSH_USER@$ip" "$@"
}

# remote_run <ip> <arg>...: shell-quotes every argument with printf %q, so a value with shell
# metacharacters is passed through literally.
remote_run() {
	local ip="$1"
	shift
	local quoted="" a
	for a in "$@"; do
		quoted="$quoted $(printf '%q' "$a")"
	done
	on "$ip" "$quoted"
}

as_chain() {
	local ip="$1"
	shift
	remote_run "$ip" sudo -u "$SVC_USER" "$BIN_DIR/oramad" --home "$HOME_DIR" "$@"
}

# put_file <ip> <mode> <dest>: writes stdin to <dest> under $HOME_DIR as $SVC_USER (never root),
# through a temp file renamed over <dest>.
put_file() {
	local ip="$1" mode="$2" dest="$3"
	on "$ip" "sudo -u $SVC_USER sh -c 'umask 077; tmp=\$(mktemp \"\$(dirname $dest)/.put.XXXXXX\") && cat > \"\$tmp\" && chmod $mode \"\$tmp\" && mv -f \"\$tmp\" $dest'"
}

build() {
	log "building oramad for linux/amd64 ($VERSION, $COMMIT)"
	(cd "$chain_root" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-trimpath \
		-ldflags "-s -w -X github.com/cosmos/cosmos-sdk/version.Name=oramad -X github.com/cosmos/cosmos-sdk/version.AppName=oramad -X github.com/cosmos/cosmos-sdk/version.Version=$VERSION -X github.com/cosmos/cosmos-sdk/version.Commit=$COMMIT" \
		-o "$work/oramad" ./cmd/oramad)
}

install_node() {
	local ip="$1" name="$2"
	log "[$name] installing binary, user and state directory"
	on "$ip" "sudo install -d -m 0755 $BIN_DIR"
	gzip -c "$work/oramad" | on "$ip" "gunzip -c | sudo sh -c 'umask 022; tmp=\$(mktemp $BIN_DIR/.oramad.XXXXXX) && cat > \"\$tmp\" && chmod 0755 \"\$tmp\" && mv -f \"\$tmp\" $BIN_DIR/oramad'"
	on "$ip" "id $SVC_USER >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin $SVC_USER"
	on "$ip" "sudo install -d -m 0700 -o $SVC_USER -g $SVC_USER $HOME_DIR"
	if ! on "$ip" "sudo -u $SVC_USER test -f $HOME_DIR/config/genesis.json"; then
		remote_run "$ip" sudo -u "$SVC_USER" "$BIN_DIR/oramad" init "$name" --chain-id "$CHAIN_ID" --default-denom "$DENOM" --home "$HOME_DIR"
	fi
	if ! on "$ip" "sudo -u $SVC_USER $BIN_DIR/oramad keys show validator --keyring-backend test --home $HOME_DIR >/dev/null 2>&1"; then
		as_chain "$ip" keys add validator --keyring-backend test --no-backup >/dev/null
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

# consensus_pubkey_of reads only the public half of priv_validator_key.json.
consensus_pubkey_of() {
	local key
	key="$(as_chain "$1" comet show-validator | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')"
	validate "$key" "$CONSENSUS_PUBKEY_RE" "consensus pubkey from $1"
	echo "$key"
}

build_genesis() {
	local first_ip
	first_ip="$(field "${NODES[0]}" 2)"
	log "building genesis on $(field "${NODES[0]}" 1)"
	as_chain "$first_ip" genesis set-emission-params \
		--epoch-duration "$EPOCH_DURATION" --min-blocks-per-epoch "$EPOCH_MIN_BLOCKS" --allow-bootstrap-stake
	local first=true n ip name addr pubkey
	for n in "${NODES[@]}"; do
		ip="$(field "$n" 2)"
		name="$(field "$n" 1)"
		addr="$(address_of "$ip")"
		pubkey="$(consensus_pubkey_of "$ip")"
		if [ "$first" = true ]; then
			first=false
			as_chain "$first_ip" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey" --min-committee-size "${#NODES[@]}"
		else
			as_chain "$first_ip" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey"
		fi
	done
	as_chain "$first_ip" genesis validate
	on "$first_ip" "sudo -u $SVC_USER cat $HOME_DIR/config/genesis.json" >"$work/genesis.json"
	GENESIS_PATH="$work/genesis.json" python3 -c '
import json, os
path = os.environ["GENESIS_PATH"]
with open(path) as f:
    doc = json.load(f)
doc.setdefault("consensus", {}).setdefault("params", {}).setdefault("block", {})["max_gas"] = "100000000"
with open(path, "w") as f:
    json.dump(doc, f, indent=2)
'
}

# assert_set fails loudly when a config edit did not take effect.
assert_set() {
	local ip="$1" file="$2" pattern="$3" what="$4"
	if ! on "$ip" "sudo -u $SVC_USER grep -q -- $(printf '%q' "$pattern") $file"; then
		die "failed to set $what in $file on $ip (pattern not found after edit: $pattern)"
	fi
}

configure_node() {
	local ip="$1" name="$2" wgip="$3" peers="$4"
	log "[$name] distributing genesis and writing config"
	put_file "$ip" 0600 "$HOME_DIR/config/genesis.json" <"$work/genesis.json"
	on "$ip" "sudo -u $SVC_USER sed -i \
		-e 's#^laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://$wgip:$P2P_PORT\"#' \
		-e 's#^laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:$RPC_PORT\"#' \
		-e 's#^persistent_peers = .*#persistent_peers = \"$peers\"#' \
		-e 's#^addr_book_strict = true#addr_book_strict = false#' \
		-e 's#^pex = true#pex = false#' \
		-e 's#^external_address = .*#external_address = \"$wgip:$P2P_PORT\"#' \
		-e 's#^prometheus_listen_addr = .*#prometheus_listen_addr = \"127.0.0.1:$PROM_PORT\"#' \
		-e 's#^prometheus = false#prometheus = true#' \
		$HOME_DIR/config/config.toml"
	assert_set "$ip" "$HOME_DIR/config/config.toml" "laddr = \"tcp://$wgip:$P2P_PORT\"" "the p2p listen address"
	assert_set "$ip" "$HOME_DIR/config/config.toml" "laddr = \"tcp://127.0.0.1:$RPC_PORT\"" "the RPC listen address"
	assert_set "$ip" "$HOME_DIR/config/config.toml" "pex = false" "pex"
	on "$ip" "sudo -u $SVC_USER sed -i \
		-e '/^\[api\]/,/^\[/ s#^enable = false#enable = true#' \
		-e 's#^address = \"tcp://localhost:1317\"#address = \"tcp://127.0.0.1:$API_PORT\"#' \
		-e 's#^address = \"localhost:9090\"#address = \"127.0.0.1:$GRPC_PORT\"#' \
		-e 's#^pruning = .*#pruning = \"custom\"#' \
		-e 's#^pruning-keep-recent = .*#pruning-keep-recent = \"100\"#' \
		-e 's#^pruning-interval = .*#pruning-interval = \"10\"#' \
		-e 's#^app-db-backend = .*#app-db-backend = \"pebbledb\"#' \
		$HOME_DIR/config/app.toml"
	assert_set "$ip" "$HOME_DIR/config/app.toml" "address = \"127.0.0.1:$GRPC_PORT\"" "the gRPC listen address"
	assert_set "$ip" "$HOME_DIR/config/app.toml" "app-db-backend = \"pebbledb\"" "the app-db-backend"
	assert_set "$ip" "$HOME_DIR/config/app.toml" "address = \"tcp://127.0.0.1:$API_PORT\"" "the REST API listen address"
	assert_api_enabled "$ip"
}

# assert_api_enabled fails unless the [api] section of app.toml says enable = true: the gateway's
# /v1/chain/* routes and the CLI's --node paths read the REST API on 127.0.0.1:$API_PORT.
assert_api_enabled() {
	local ip="$1"
	if ! on "$ip" "sudo -u $SVC_USER awk '/^\\[/{s=(\$0==\"[api]\")} s && /^enable = true/{f=1} END{exit !f}' $HOME_DIR/config/app.toml"; then
		die "failed to enable the REST API in $HOME_DIR/config/app.toml on $ip ([api] enable is not true)"
	fi
}

# write_unit <ip> <wg-ip>...: IPAddressAllow is localhost plus the fleet's own overlay addresses.
write_unit() {
	local ip="$1"
	shift
	local wg_ips="$*"
	on "$ip" "sudo tee /etc/systemd/system/$UNIT >/dev/null" <<EOF
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
	on "$ip" "sudo systemctl daemon-reload && sudo systemctl enable --now $UNIT"
}

# height_of prints a node's latest block height, or nothing when its RPC does not answer.
height_of() {
	local h
	h="$(on "$1" "curl -s --max-time 5 http://127.0.0.1:$RPC_PORT/status | python3 -c 'import json,sys; print(json.load(sys.stdin)[\"result\"][\"sync_info\"][\"latest_block_height\"])' 2>/dev/null" || true)"
	if [[ "$h" =~ $HEIGHT_RE ]]; then
		echo "$h"
	fi
}

# wait_blocks polls every node until each reports a height of at least 1, or READY_TIMEOUT passes.
wait_blocks() {
	local deadline=$((SECONDS + READY_TIMEOUT)) n ip h pending
	while :; do
		pending=""
		for n in "${NODES[@]}"; do
			ip="$(field "$n" 2)"
			h="$(height_of "$ip")"
			if [ -z "$h" ] || [ "$h" -lt 1 ]; then
				pending="$pending $(field "$n" 1)"
			fi
		done
		[ -n "$pending" ] || return 0
		[ "$SECONDS" -lt "$deadline" ] || die "no blocks after ${READY_TIMEOUT}s from:$pending"
		sleep "$READY_POLL"
	done
}

cmd_up() {
	build
	local n
	for n in "${NODES[@]}"; do install_node "$(field "$n" 2)" "$(field "$n" 1)"; done
	build_genesis
	local peers=() all_wg=() id
	for n in "${NODES[@]}"; do
		id="$(node_id "$(field "$n" 2)")"
		peers+=("$id@$(field "$n" 3):$P2P_PORT")
		all_wg+=("$(field "$n" 3)")
	done
	local peer_list
	peer_list="$(IFS=,; echo "${peers[*]}")"
	for n in "${NODES[@]}"; do
		configure_node "$(field "$n" 2)" "$(field "$n" 1)" "$(field "$n" 3)" "$peer_list"
		write_unit "$(field "$n" 2)" "${all_wg[@]}"
	done
	wait_blocks
	cmd_status
}

# api_answers succeeds when a node's REST API answers node_info on 127.0.0.1:$API_PORT.
api_answers() {
	on "$1" "curl -fsS --max-time 5 -o /dev/null http://127.0.0.1:$API_PORT/cosmos/base/tendermint/v1beta1/node_info"
}

# cmd_status prints each node's height and REST API state, and fails if any node's RPC or REST API
# does not answer.
cmd_status() {
	local n h ip api failed=0
	for n in "${NODES[@]}"; do
		ip="$(field "$n" 2)"
		h="$(height_of "$ip")"
		if api_answers "$ip"; then api="api ok"; else api="api not responding on :$API_PORT"; failed=1; fi
		if [ -n "$h" ]; then
			printf '%-12s height %s, %s\n' "$(field "$n" 1)" "$h" "$api"
		else
			printf '%-12s not responding, %s\n' "$(field "$n" 1)" "$api"
			failed=1
		fi
	done
	return "$failed"
}

# INVARIANT_MODULES must hold on every node (docs/SECURITY_PLAYBOOKS.md). A literal list.
INVARIANT_MODULES=(emission fees storage nodes relay houses token market power shielded)

cmd_invariants() {
	local failed=0 n ip name m out
	for n in "${NODES[@]}"; do
		ip="$(field "$n" 2)"
		name="$(field "$n" 1)"
		for m in "${INVARIANT_MODULES[@]}"; do
			if ! out="$(as_chain "$ip" query "$m" invariants --node "tcp://127.0.0.1:$RPC_PORT" --output json 2>&1)"; then
				printf '%-12s %-9s query failed: %s\n' "$name" "$m" "$out"
				failed=1
				continue
			fi
			# x/shielded is printed by autocli, which leaves a false boolean out: its members must be present and true.
			if echo "$out" | python3 -c 'import json,sys
d=json.load(sys.stdin)
req={"shielded":["balance_matches","pools_non_negative","accumulator_matches"]}.get(sys.argv[1],[])
sys.exit(1 if req and [k for k in req if d.get(k) is not True] or [k for k,v in d.items() if isinstance(v,bool) and not v] else 0)' "$m"; then
				printf '%-12s %-9s ok\n' "$name" "$m"
			else
				printf '%-12s %-9s BROKEN %s\n' "$name" "$m" "$out"
				failed=1
			fi
		done
	done
	return "$failed"
}

# cmd_reset tears the chain install down, including one left half-done by an aborted `up`.
cmd_reset() {
	local n ip
	for n in "${NODES[@]}"; do
		ip="$(field "$n" 2)"
		log "[$(field "$n" 1)] stopping and wiping the chain install"
		on "$ip" "if sudo systemctl cat $UNIT >/dev/null 2>&1; then sudo systemctl disable --now $UNIT; fi"
		on "$ip" "sudo rm -f /etc/systemd/system/$UNIT"
		on "$ip" "sudo systemctl daemon-reload"
		on "$ip" "sudo rm -rf $HOME_DIR"
		on "$ip" "sudo rm -f $BIN_DIR/oramad"
	done
}

case "${1:-}" in
up) cmd_up ;;
status) cmd_status ;;
invariants) cmd_invariants ;;
reset) cmd_reset ;;
*)
	echo "usage: $0 up|status|invariants|reset" >&2
	exit 2
	;;
esac
