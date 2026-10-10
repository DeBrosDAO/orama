#!/usr/bin/env bash
# Stand up (or reset) the Orama L1 and its global services on the stagenet nodes through the
# product's own install path: `orama global install --colocated`, `orama global start`, and the
# `orama global bind|register|bond|capacity` commands. What runs here is what an operator runs, so
# a stagenet deploy exercises the code users will.
#
# Each stagenet node already runs an Orama private-cluster node (orama-node, namespace gateways,
# RQLite, WireGuard). That stays untouched: the global services run co-located, in their own
# network namespace (orama-global), as docs/RUN_A_GLOBAL_NODE.md describes. Nothing here starts,
# stops or reconfigures a cluster service; `reset` edits only the two global lines the install
# added to the cluster's preferences.yaml.
#
# Stagenet/devnet only: CHAIN_ID must say so, and the script refuses to run otherwise. The operator
# key of each node is a validator key in oramad's test keyring (an unencrypted, on-disk keyring),
# a devnet-only convenience never appropriate once real value is at stake. It is generated on the
# node and never copied off it: `register` pipes it, on the node, into a signing agent that speaks
# the RootWallet agent protocol.
#
# Usage: deploy.sh reset | up | start | status | invariants | register | smoke | gen-shielded
#
#   reset        stop and remove the global install (and any legacy direct-unit chain install) and
#                wipe chain, provider, archiver, indexer and IPFS state. Idempotent.
#   up           build, stage a root-owned release directory, build the genesis, `orama global
#                install --colocated`, and start the services.
#   start        `orama global start` on every node (up runs it).
#   status       services, height and peers of every node.
#   invariants   every module's invariants query on every node.
#   register     once the chain has run 2 epochs: register an operator and a node per machine, bond
#                STORAGE and ARCHIVER from earnings, fund the hot key, declare capacity, start the
#                provider and archiver.
#   smoke        live checks, PASS/FAIL/SKIP each: blocks and inclusion lists, invariants, standard
#                contracts and a CW20, a private storage deal, archive ranges, shielded, and the
#                gateway's chain read.
#   gen-shielded build the wallet scenario for this chain (needs the Rust toolchain).
#
# Environment (all optional except where noted):
#   CHAIN_ID                 orama-stagenet-1; must contain -stagenet- or -devnet-.
#   ASN_mew ASN_mewtwo ASN_gengar ASN_magicarp ASN_froakie
#                            the autonomous system each node declares; the true one of its provider:
#                            16276 (OVH) for mew and mewtwo, 51167 (Contabo) for gengar, magicarp, froakie.
#   PUBLIC_STORAGE_GB        capacity each provider declares, in GB (10).
#   STORAGE_BOND_NORAMA      the STORAGE bond; default is the least that backs the capacity.
#   ARCHIVER_BOND_NORAMA     the ARCHIVER bond (1 ORAMA, the role minimum).
#   HOT_KEY_FUND_NORAMA      the fee-only balance given to each hot key (2 ORAMA).
#   TX_GAS                   gas limit of each `orama global` transaction; its fee is gas x the base fee.
#   EPOCH_DURATION EPOCH_MIN_BLOCKS VOTE_EXTENSIONS_ENABLE_HEIGHT   as before (genesis).
#   FAUCET_ENABLED           1 (default) sets emission.params.faucet_enabled in genesis, so `orama chain faucet` works; 0 leaves it off.
#   CA_FILE                  PEM bundle that signs the gateway certificate (smoke).
#   GATEWAY_URL              the gateway smoke reads through (https://stagenet.dbrsteting.bid).
#   SHIELDED_SCENARIO        scenario JSON from `gen-shielded` (smoke).
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
# name:ssh-alias:public-ip. The public address is what peers and clients dial: the global services
# run in a network namespace that cannot reach the WireGuard mesh, so the chain peers over the
# public network (docs/RUN_A_GLOBAL_NODE.md, "Sharing a machine with a cluster node").
# All five stagenet nodes are validators: nothing here fixes the committee size (build_genesis sets it
# to the number of NODES) and every loop below runs over NODES.
NODES=("mew:mew:57.129.166.16" "mewtwo:mewtwo:57.129.166.17" "gengar:gengar:161.97.184.199" "magicarp:magicarp:161.97.184.202" "froakie:froakie:161.97.151.255")
P2P_PORT=31000
PROVIDER_PORT=31013
BIN_DIR="/usr/lib/orama-global/bin"
HOME_DIR="/var/lib/orama-global/chain"
GENESIS_WORK="/var/lib/orama-global/genesis-work"
SVC_USER="orama-chain"
STAGE_DIR="/root/orama-global-release"
TOOLS_DIR="/usr/local/lib/orama-stagenet"
# The chain's RPC and REST API listen on the orama-global namespace address (core/pkg/constants,
# GlobalNetnsAddr), which the host reaches over the veth pair; the namespace firewall admits only the host.
NS_ADDR="198.18.0.2"
RPC_ADDR="tcp://$NS_ADDR:31001"
RPC_HTTP="http://$NS_ADDR:31001"
MIN_EPOCH=2
EPOCH_POLL_SECONDS=10
EPOCH_WAIT_SECONDS=2400
EPOCH_DURATION="${EPOCH_DURATION:-300s}"
EPOCH_MIN_BLOCKS="${EPOCH_MIN_BLOCKS:-10}"
# C13 inclusion lists: the height vote extensions turn on at, patched into genesis (see build_genesis).
VOTE_EXTENSIONS_ENABLE_HEIGHT="${VOTE_EXTENSIONS_ENABLE_HEIGHT:-2}"
# The test-network faucet (x/emission MsgFaucet): on by default on stagenet, patched into genesis (see build_genesis).
FAUCET_ENABLED="${FAUCET_ENABLED:-1}"

PUBLIC_STORAGE_GB="${PUBLIC_STORAGE_GB:-10}"
ARCHIVER_BOND_NORAMA="${ARCHIVER_BOND_NORAMA:-1000000000}"
HOT_KEY_FUND_NORAMA="${HOT_KEY_FUND_NORAMA:-2000000000}"
TX_GAS="${TX_GAS:-600000}"
CA_FILE="${CA_FILE:-/Users/pen/orama-stagenet-handoff/le-roots.pem}"
GATEWAY_URL="${GATEWAY_URL:-https://stagenet.dbrsteting.bid}"
SHIELDED_SCENARIO="${SHIELDED_SCENARIO:-}"
# The true ASN of each node's provider: mew and mewtwo are OVH (AS16276), gengar, magicarp and froakie
# are Contabo (AS51167; whois -h whois.radb.net <ip>). ASN_<name> overrides it.
ASN_OVH=16276
ASN_CONTABO=51167

# Pinned release artifacts. Each digest is the release's own: the Kubo sha512 is the release's
# published kubo_<version>_linux-amd64.tar.gz.sha512 (the same file dist.ipfs.tech serves), and its
# sha256 is the one core/pkg/constants/release_digests.go pins (checked against it below); the cosmovisor sha256 is the one
# core/pkg/constants/cosmovisor.go pins (checked against it below) and the release's SHA256SUMS.
KUBO_VERSION="v0.43.1"
KUBO_TARBALL="kubo_${KUBO_VERSION}_linux-amd64.tar.gz"
KUBO_URL="https://github.com/ipfs/kubo/releases/download/${KUBO_VERSION}/${KUBO_TARBALL}"
KUBO_SHA256="3f2bf974ab2a3ec6d997fac7d8cb46f59983a7cddd0b55ef998e6ce379155fb2"
KUBO_SHA512="ff53b2428794fc8cca39505d28c15b4cceaef4b90a09284f291f611fcdc8c06399690575fc89cbd4d85ef71f3652581296c6f2e710386f887c8edf36b6e89d71"
COSMOVISOR_VERSION="v1.7.3"
COSMOVISOR_TARBALL="cosmovisor-${COSMOVISOR_VERSION}-linux-amd64.tar.gz"
COSMOVISOR_URL="https://github.com/cosmos/cosmos-sdk/releases/download/cosmovisor%2F${COSMOVISOR_VERSION}/${COSMOVISOR_TARBALL}"
COSMOVISOR_SHA256="3df6ef38cf976b00d226f391dc6866b8dc4040fc2f1b4a780d248f6e1cc9332e"

# EPOCH_DURATION/EPOCH_MIN_BLOCKS reach a remote CLI flag value: keep them to a safe, boring
# syntax (Go duration / plain integer) before they do.
if ! [[ "$EPOCH_DURATION" =~ ^[0-9]+(h|m|s)$ ]]; then
	echo "invalid EPOCH_DURATION (expected e.g. 300s, 5m, 1h): $EPOCH_DURATION" >&2
	exit 1
fi
for v in EPOCH_MIN_BLOCKS VOTE_EXTENSIONS_ENABLE_HEIGHT PUBLIC_STORAGE_GB ARCHIVER_BOND_NORAMA HOT_KEY_FUND_NORAMA TX_GAS; do
	if ! [[ "${!v}" =~ ^[0-9]{1,15}$ ]]; then
		echo "invalid $v (expected a plain integer): ${!v}" >&2
		exit 1
	fi
done

if ! [[ "$FAUCET_ENABLED" =~ ^[01]$ ]]; then
	echo "invalid FAUCET_ENABLED (expected 0 or 1): $FAUCET_ENABLED" >&2
	exit 1
fi

# The bond that backs the declared capacity: 1 ORAMA of STORAGE bond backs 1 GiB (bond_per_gib), so
# it is the capacity in GiB rounded up, in whole ORAMA.
if [ "$PUBLIC_STORAGE_GB" -gt 999999 ]; then
	echo "PUBLIC_STORAGE_GB is too large: $PUBLIC_STORAGE_GB" >&2
	exit 1
fi
CAPACITY_BYTES=$((PUBLIC_STORAGE_GB * 1000000000))
capacity_gib=$(((CAPACITY_BYTES + 1073741823) / 1073741824))
STORAGE_BOND_NORAMA="${STORAGE_BOND_NORAMA:-$((capacity_gib * 1000000000))}"
if ! [[ "$STORAGE_BOND_NORAMA" =~ ^[0-9]{1,15}$ ]]; then
	echo "invalid STORAGE_BOND_NORAMA (expected a plain integer): $STORAGE_BOND_NORAMA" >&2
	exit 1
fi

# The autonomous system each node declares (docs/CHAIN.md, "Node network identity"): the operator's
# true ASN. It is read from ASN_<name> and validated before it reaches a remote command.
asn_of() {
	local var="ASN_$1" default value
	case "$1" in
	mew|mewtwo) default=$ASN_OVH ;;
	gengar|magicarp|froakie) default=$ASN_CONTABO ;;
	*)
		echo "no default ASN for node $1; set $var" >&2
		exit 1
		;;
	esac
	value="${!var:-$default}"
	if ! [[ "$value" =~ ^[0-9]{1,10}$ ]] || [ "$value" -lt 1 ] || [ "$value" -gt 4294967295 ]; then
		echo "invalid $var (expected an ASN, 1 to 4294967295): $value" >&2
		exit 1
	fi
	echo "$value"
}

# Validator addresses, node IDs and consensus pubkeys come back from commands run on the remote
# nodes; they are validated against these before ever being substituted into another remote
# command. CONSENSUS_PUBKEY_RE matches the base64 encoding of exactly 32 bytes (a raw ed25519 key):
# ceil(32/3)*4 = 44 characters, with one '=' padding character since 32 mod 3 == 2.
ADDR_RE='^orama1[02-9ac-hj-np-z]{38}$'
NODE_ID_RE='^[0-9a-f]{40}$'
CONSENSUS_PUBKEY_RE='^[A-Za-z0-9+/]{43}=$'

here="$(cd "$(dirname "$0")" && pwd)"
chain_root="$(cd "$here/../.." && pwd)"
core_root="$(cd "$chain_root/../core" && pwd)"
cache_dir="$chain_root/build/stagenet-cache"
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

# login_user <alias>: the account ssh logs into the node as, from the ssh config this script already
# uses. It is allowed to reach the chain on the namespace address (smoke tunnels to it and operators
# run `orama chain` from it), so `global install` is told about it with --chain-client-user.
login_user() {
	local user
	user="$(ssh -G "$1" | awk '$1 == "user" { print $2; exit }')"
	validate "$user" '^[a-z_][a-z0-9_-]{0,31}$' "ssh login user of $1"
	echo "$user"
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

# as_chain_at <alias> <home> <arg>...: as_chain against another home (the scratch genesis home).
as_chain_at() {
	local alias="$1" home="$2"
	shift 2
	remote_run "$alias" sudo -u "$SVC_USER" "$BIN_DIR/oramad" --home "$home" "$@"
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
# build

sha256_of() { shasum -a 256 "$1" | cut -d' ' -f1; }
sha512_of() { shasum -a 512 "$1" | cut -d' ' -f1; }

# verify_pins checks the cosmovisor digest above against the one the CLI itself pins, so the two
# cannot drift apart unnoticed.
verify_pins() {
	if ! grep -q "\"amd64\": \"$COSMOVISOR_SHA256\"" "$core_root/pkg/constants/cosmovisor.go"; then
		echo "the cosmovisor digest in this script is not the one core/pkg/constants/cosmovisor.go pins" >&2
		exit 1
	fi
	if ! grep -q "IPFSKuboVersion *= \"$KUBO_VERSION\"" "$core_root/pkg/constants/versions.go"; then
		echo "core/pkg/constants/versions.go pins another Kubo than $KUBO_VERSION" >&2
		exit 1
	fi
	if ! grep -q "\"amd64\": \"$KUBO_SHA256\"" "$core_root/pkg/constants/release_digests.go"; then
		echo "the Kubo digest in this script is not the one core/pkg/constants/release_digests.go pins" >&2
		exit 1
	fi
}

# fetch <url> <dest> <sha256> [<sha512>]: downloads once into the cache and refuses a file whose
# digest is not the pinned one. A cached file is re-verified, never trusted.
fetch() {
	local url="$1" dest="$2" want256="$3" want512="${4:-}"
	mkdir -p "$cache_dir"
	if [ ! -f "$dest" ]; then
		log "downloading $url"
		curl -fsSL --proto '=https' --tlsv1.2 -o "$dest.part" "$url"
		mv "$dest.part" "$dest"
	fi
	if [ "$(sha256_of "$dest")" != "$want256" ]; then
		echo "sha256 of $dest is not the pinned $want256; deleting it" >&2
		rm -f "$dest"
		exit 1
	fi
	if [ -n "$want512" ] && [ "$(sha512_of "$dest")" != "$want512" ]; then
		echo "sha512 of $dest is not the pinned digest; deleting it" >&2
		rm -f "$dest"
		exit 1
	fi
}

# build compiles everything the install stages, and the tools this script runs:
#   oramad (static, with libwasmvm and the Orchard verifier) and the pinned out-of-process verifier
#   orama-global and stagenet-node (static Go), the orama CLI for linux/amd64 (the chain unit runs
#   its sign-floor check), Kubo and cosmovisor from their official releases, and the host's own orama
#   and stagenetctl. Nothing prebuilt is downloaded except the two pinned tarballs.
build() {
	verify_pins
	log "building oramad for linux/amd64 with libwasmvm and the Orchard verifier"
	(cd "$chain_root" && make build-linux-amd64-full build-linux-amd64-global)
	cp "$chain_root/build/oramad-linux-amd64-full" "$work/oramad"
	# The second shielded verifier: a separately built and pinned Rust binary, run out of process. The
	# same make target links its sha256 into oramad, so oramad refuses any other file.
	cp "$chain_root/build/orama-orchard-verifier-linux-amd64" "$work/orama-orchard-verifier"
	cp "$chain_root/build/orama-orchard-verifier-linux-amd64.sha256" "$work/orama-orchard-verifier.sha256"
	cp "$chain_root/build/orama-global-linux-amd64" "$work/orama-global"
	cp "$chain_root/build/stagenet-node-linux-amd64" "$work/stagenet-node"
	log "building the orama CLI for linux/amd64"
	(cd "$core_root" && make build-linux)
	cp "$core_root/bin-linux/orama" "$work/orama"
	fetch "$COSMOVISOR_URL" "$cache_dir/$COSMOVISOR_TARBALL" "$COSMOVISOR_SHA256"
	cp "$cache_dir/$COSMOVISOR_TARBALL" "$work/$COSMOVISOR_TARBALL"
	fetch "$KUBO_URL" "$cache_dir/$KUBO_TARBALL" "$KUBO_SHA256" "$KUBO_SHA512"
	tar -xzf "$cache_dir/$KUBO_TARBALL" -C "$work" kubo/ipfs
	mv "$work/kubo/ipfs" "$work/ipfs"
	write_manifest
}

# write_manifest lists the staged release files with their digests, the manifest.json that
# `orama global install --manifest` holds the staged directory to (a release archive carries the
# same file, written and signed by `orama build`).
write_manifest() {
	local f first=1
	{
		printf '{"version":"stagenet","checksums":{'
		for f in oramad orama orama-global ipfs orama-orchard-verifier orama-orchard-verifier.sha256 "$COSMOVISOR_TARBALL"; do
			[ "$first" = 1 ] || printf ','
			first=0
			printf '"%s":"%s"' "$f" "$(sha256_of "$work/$f")"
		done
		printf '}}\n'
	} > "$work/manifest.json"
}

# build_host_tools compiles what runs on this machine: stagenetctl, and the orama CLI for this OS
# (`orama storage` runs here, against the nodes).
build_host_tools() {
	log "building stagenetctl and the orama CLI for this machine"
	(cd "$chain_root" && go build -o "$work/stagenetctl" ./scripts/stagenet/smoke)
	(cd "$core_root" && go build -o "$work/orama-host" ./cmd/orama/)
}

# ------------------------------------------------------------------------------------------------
# install

# preflight refuses, before anything changes, a node that cannot take a co-located install or that
# already has a global install (run reset first).
preflight() {
	local alias="$1" name="$2"
	log "[$name] checking the node"
	on "$alias" "test -f /opt/orama/.orama/preferences.yaml" || {
		echo "[$name] no cluster node here (/opt/orama/.orama/preferences.yaml is missing); --colocated needs one" >&2
		exit 1
	}
	on "$alias" "sudo ufw status | grep -q 'Status: active'" || {
		echo "[$name] ufw is inactive. The install refuses an inactive ufw, and enabling it would change the cluster's firewall: not doing that here." >&2
		exit 1
	}
	# ip and nft are not checked here: orama global install --colocated installs iproute2 and
	# nftables itself when they are missing (globalnetns.InstallTools).
	if on "$alias" "sudo test -e $HOME_DIR/config/genesis.json"; then
		echo "[$name] a chain home already exists at $HOME_DIR; run '$0 reset' first" >&2
		exit 1
	fi
}

# stage_release makes a root-owned, root-only directory on the node and puts the release in it. The
# installer copies from there and refuses a directory another account could change.
stage_release() {
	local alias="$1" name="$2" f
	log "[$name] staging the release in $STAGE_DIR"
	on "$alias" "sudo rm -rf $STAGE_DIR && sudo install -d -m 0700 -o root -g root $STAGE_DIR"
	for f in oramad orama orama-global ipfs orama-orchard-verifier; do
		put_root_file "$alias" 0755 "$STAGE_DIR/$f" < "$work/$f"
	done
	put_root_file "$alias" 0644 "$STAGE_DIR/orama-orchard-verifier.sha256" < "$work/orama-orchard-verifier.sha256"
	put_root_file "$alias" 0644 "$STAGE_DIR/manifest.json" < "$work/manifest.json"
	put_root_file "$alias" 0644 "$STAGE_DIR/$COSMOVISOR_TARBALL" < "$work/$COSMOVISOR_TARBALL"
	# --init-chain wants a genesis of the right chain id; the real one needs this node's own keys, so
	# a placeholder goes in first and the real genesis replaces it before anything starts.
	printf '{"chain_id":"%s"}\n' "$CHAIN_ID" | put_root_file "$alias" 0600 "$STAGE_DIR/genesis.json"
	stage_tools "$alias" "$name"
}

# stage_tools installs the helper `register` and `smoke` run on the node, in a root-owned
# directory of its own.
stage_tools() {
	local alias="$1" name="$2"
	log "[$name] installing stagenet-node in $TOOLS_DIR"
	on "$alias" "sudo install -d -m 0755 -o root -g root $TOOLS_DIR"
	put_root_file "$alias" 0755 "$TOOLS_DIR/stagenet-node" < "$work/stagenet-node"
}

# global_install runs `orama global install --colocated` from the staged release. Phase 1 creates
# the chain home (and with it the node's own keys) with a placeholder genesis; phase 2 runs it
# again with the chain's persistent peers, which are the node ids phase 1 made, and rewrites the
# units. Both are the documented command; running it twice with the same flags is a supported no-op.
global_install() {
	# The indexer runs on every node: a gateway proxies the indexer beside it, and a client reaches
	# the public name on any of them.
	local alias="$1" name="$2" phase="$3" peers="${4:-}" ip="${5:-}" services="chain,ipfs,provider,archiver,indexer"
	# The login user is resolved on its own line: inside the array a failing command substitution
	# would not stop the script (the status of `local` and of an array assignment hides it).
	local login
	login="$(login_user "$alias")"
	local args=(sudo "$STAGE_DIR/orama" global install --colocated --services "$services"
		--public-storage-gb "$PUBLIC_STORAGE_GB" --staged-dir "$STAGE_DIR" --manifest "$STAGE_DIR/manifest.json")
	# root is always allowed to reach the chain, and the install refuses it as a client user.
	if [ "$login" != root ]; then
		args+=(--chain-client-user "$login")
	fi
	if [ "$phase" = 1 ]; then
		args+=(--init-chain --chain-id "$CHAIN_ID" --moniker "$name" --genesis "$STAGE_DIR/genesis.json")
	else
		args+=(--persistent-peers "$peers" --external-address "$ip:$P2P_PORT")
	fi
	log "[$name] orama global install (phase $phase)"
	remote_run "$alias" "${args[@]}"
}

create_operator_key() {
	local alias="$1" name="$2"
	log "[$name] creating the operator key in oramad's test keyring (it stays on the node)"
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

# build_genesis builds the network's genesis on the first node, in a scratch home (the node's own
# chain home holds the placeholder), from every node's own public keys, and leaves it in
# $work/genesis.json.
build_genesis() {
	local first_alias; first_alias="$(field "${NODES[0]}" 2)"
	log "building genesis on $first_alias"
	on "$first_alias" "sudo rm -rf $GENESIS_WORK && sudo install -d -m 0700 -o $SVC_USER -g $SVC_USER $GENESIS_WORK"
	as_chain_at "$first_alias" "$GENESIS_WORK" init genesis-work --chain-id "$CHAIN_ID" --default-denom "$DENOM" >/dev/null

	# Genesis starts at exactly zero norama supply: every node is a member of x/power's bootstrap
	# committee (plans/open-network.md D16), which needs no self-bond and no gentx - replacing the
	# old devnet-only self-bonded-validator exception (see docs/CHAIN.md).
	# The test-network faucet is a genesis-only switch (x/emission has no Msg that changes params).
	local faucet_flag=()
	if [ "$FAUCET_ENABLED" = 1 ]; then faucet_flag=(--faucet-enabled); fi
	as_chain_at "$first_alias" "$GENESIS_WORK" genesis set-emission-params \
		--epoch-duration "$EPOCH_DURATION" --min-blocks-per-epoch "$EPOCH_MIN_BLOCKS" --allow-bootstrap-stake \
		${faucet_flag[@]+"${faucet_flag[@]}"}

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
			as_chain_at "$first_alias" "$GENESIS_WORK" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey" \
				--min-committee-size "${#NODES[@]}"
		else
			as_chain_at "$first_alias" "$GENESIS_WORK" genesis add-bootstrap-validator "$addr" \
				--moniker "$name" --consensus-pubkey-base64 "$pubkey"
		fi
	done

	# The standard contracts (CW20, CW721, escrow, CW3 multisig, vesting) are stored in genesis so they
	# exist from height 1 although upload is closed until the sunset height (docs/CHAIN.md).
	as_chain_at "$first_alias" "$GENESIS_WORK" genesis add-standard-contracts

	as_chain_at "$first_alias" "$GENESIS_WORK" genesis validate
	on "$first_alias" "sudo -u $SVC_USER cat $GENESIS_WORK/config/genesis.json" > "$work/genesis.json"
	on "$first_alias" "sudo rm -rf $GENESIS_WORK"

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

# configure_node distributes the genesis. The rest of the node's chain configuration (the external
# address, peer exchange, pruning, snapshots, query gas limit and IAVL cache) is written by
# `orama global install --external-address` (core/pkg/install ChainConfig), which refuses a template
# whose key it cannot find; the chain unit's own flags set the listeners and the persistent peers.
configure_node() {
	local alias="$1" name="$2"
	log "[$name] distributing genesis"
	put_file "$alias" 0600 "$HOME_DIR/config/genesis.json" < "$work/genesis.json"
}

cmd_up() {
	build
	local n alias name ip
	for n in "${NODES[@]}"; do preflight "$(field "$n" 2)" "$(field "$n" 1)"; done
	for n in "${NODES[@]}"; do stage_release "$(field "$n" 2)" "$(field "$n" 1)"; done
	for n in "${NODES[@]}"; do
		global_install "$(field "$n" 2)" "$(field "$n" 1)" 1
		create_operator_key "$(field "$n" 2)" "$(field "$n" 1)"
	done
	build_genesis
	local peers=()
	for n in "${NODES[@]}"; do
		peers+=("$(node_id "$(field "$n" 2)")@$(field "$n" 3):$P2P_PORT")
	done
	local peer_list; peer_list="$(IFS=,; echo "${peers[*]}")"
	for n in "${NODES[@]}"; do
		alias="$(field "$n" 2)"; name="$(field "$n" 1)"; ip="$(field "$n" 3)"
		configure_node "$alias" "$name"
		global_install "$alias" "$name" 2 "$peer_list" "$ip"
	done
	cmd_start
	cmd_status
}

# cmd_start runs `orama global start` on every node at once: a chain of five needs four of them up
# (more than two thirds of the committee) before any block, so starting one after the other would only wait on each RPC.
cmd_start() {
	local pids=() n
	for n in "${NODES[@]}"; do
		# The provider and archiver need a node id and are started by `register`; started now they
		# would exit on the missing id and restart every few seconds until then.
		local svcs=(chain ipfs indexer)
		log "[$(field "$n" 1)] orama global start ${svcs[*]}"
		remote_run "$(field "$n" 2)" sudo "$BIN_DIR/orama" global start "${svcs[@]}" < /dev/null &
		pids+=($!)
	done
	local failed=0 pid
	for pid in "${pids[@]}"; do wait "$pid" || failed=1; done
	[ "$failed" = 0 ] || { echo "orama global start failed on at least one node" >&2; exit 1; }
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
# register

# wait_for_epoch polls the chain's current epoch (x/emission) until it reaches MIN_EPOCH. It does not
# sleep for a fixed time: every poll is a query, and the loop ends on the first answer that is high
# enough or on the deadline.
wait_for_epoch() {
	local alias; alias="$(field "${NODES[0]}" 2)"
	local waited=0 epoch=""
	log "waiting for epoch $MIN_EPOCH (epochs last $EPOCH_DURATION)"
	while [ "$waited" -lt "$EPOCH_WAIT_SECONDS" ]; do
		epoch="$(remote_run "$alias" "$TOOLS_DIR/stagenet-node" epoch --rpc "$RPC_ADDR" 2>/dev/null || true)"
		if [[ "$epoch" =~ ^[0-9]+$ ]] && [ "$epoch" -ge "$MIN_EPOCH" ]; then
			log "the chain is at epoch $epoch"
			return 0
		fi
		sleep "$EPOCH_POLL_SECONDS"
		waited=$((waited + EPOCH_POLL_SECONDS))
	done
	echo "the chain did not reach epoch $MIN_EPOCH in ${EPOCH_WAIT_SECONDS}s (last answer: '${epoch}')" >&2
	exit 1
}

cmd_register() {
	log "building the node helper"
	(cd "$chain_root" && make build-linux-amd64-global)
	cp "$chain_root/build/stagenet-node-linux-amd64" "$work/stagenet-node"
	local n alias name ip asn
	for n in "${NODES[@]}"; do stage_tools "$(field "$n" 2)" "$(field "$n" 1)"; done
	wait_for_epoch
	for n in "${NODES[@]}"; do
		alias="$(field "$n" 2)"; name="$(field "$n" 1)"; ip="$(field "$n" 3)"
		asn="$(asn_of "$name")"
		log "[$name] registering (asn $asn, endpoint http://$ip:$PROVIDER_PORT)"
		run_remote_script "$alias" register-node.sh "$CHAIN_ID" "stagenet-$name" "$ip" "$asn" \
			"$STORAGE_BOND_NORAMA" "$ARCHIVER_BOND_NORAMA" "$CAPACITY_BYTES" "$HOT_KEY_FUND_NORAMA" \
			"$TX_GAS"
	done
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
	up) cmd_up ;;
	start) cmd_start ;;
	status) cmd_status ;;
	invariants) cmd_invariants ;;
	register) cmd_register ;;
	smoke) cmd_smoke ;;
	gen-shielded) cmd_gen_shielded ;;
	*) echo "usage: $0 reset|up|start|status|invariants|register|smoke|gen-shielded" >&2; exit 2 ;;
esac
