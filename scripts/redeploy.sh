#!/usr/bin/env bash
#
# Redeploy to all nodes in a given environment (devnet or testnet).
# Reads node credentials from scripts/remote-nodes.conf.
#
# Flow (per docs/DEV_DEPLOY.md):
#  1) make build-linux
#  2) scripts/generate-source-archive.sh -> /tmp/network-source.tar.gz
#  3) scp archive + extract-deploy.sh + conf to hub node
#  4) from hub: sshpass scp to all other nodes + sudo bash /tmp/extract-deploy.sh
#  5) rolling: upgrade followers one-by-one, leader last
#
# Usage:
#   scripts/redeploy.sh --devnet
#   scripts/redeploy.sh --testnet
#   scripts/redeploy.sh --devnet --no-build
#   scripts/redeploy.sh --testnet --no-build
#
set -euo pipefail

# ── Parse flags ──────────────────────────────────────────────────────────────
ENV=""
NO_BUILD=0

for arg in "$@"; do
  case "$arg" in
    --devnet)   ENV="devnet" ;;
    --testnet)  ENV="testnet" ;;
    --no-build) NO_BUILD=1 ;;
    -h|--help)
      echo "Usage: scripts/redeploy.sh --devnet|--testnet [--no-build]"
      exit 0
      ;;
    *)
      echo "Unknown flag: $arg" >&2
      echo "Usage: scripts/redeploy.sh --devnet|--testnet [--no-build]" >&2
      exit 1
      ;;
  esac
done

if [[ -z "$ENV" ]]; then
  echo "ERROR: specify --devnet or --testnet" >&2
  exit 1
fi

# ── Paths ────────────────────────────────────────────────────────────────────
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$ROOT_DIR/scripts/remote-nodes.conf"
ARCHIVE="/tmp/network-source.tar.gz"
EXTRACT_SCRIPT="$ROOT_DIR/scripts/extract-deploy.sh"

die() { echo "ERROR: $*" >&2; exit 1; }
need_file() { [[ -f "$1" ]] || die "Missing file: $1"; }

need_file "$CONF"
need_file "$EXTRACT_SCRIPT"

# ── Load nodes from conf ────────────────────────────────────────────────────
HOSTS=()
PASSES=()
ROLES=()
SSH_KEYS=()

while IFS='|' read -r env host pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "$ENV" ]] && continue

  HOSTS+=("$host")
  PASSES+=("$pass")
  ROLES+=("${role:-node}")
  SSH_KEYS+=("${key:-}")
done < "$CONF"

if [[ ${#HOSTS[@]} -eq 0 ]]; then
  die "No nodes found for environment '$ENV' in $CONF"
fi

echo "== redeploy.sh ($ENV) — ${#HOSTS[@]} nodes =="
for i in "${!HOSTS[@]}"; do
  echo "  [$i] ${HOSTS[$i]} (${ROLES[$i]})"
done

# ── Pick hub node ────────────────────────────────────────────────────────────
# Hub = first node that has an SSH key configured (direct SCP from local).
# If none have a key, use the first node (via sshpass).
HUB_IDX=0
HUB_KEY=""
for i in "${!HOSTS[@]}"; do
  if [[ -n "${SSH_KEYS[$i]}" ]]; then
    expanded_key="${SSH_KEYS[$i]/#\~/$HOME}"
    if [[ -f "$expanded_key" ]]; then
      HUB_IDX=$i
      HUB_KEY="$expanded_key"
      break
    fi
  fi
done

HUB_HOST="${HOSTS[$HUB_IDX]}"
HUB_PASS="${PASSES[$HUB_IDX]}"

echo "Hub: $HUB_HOST (idx=$HUB_IDX, key=${HUB_KEY:-none})"

# ── Build ────────────────────────────────────────────────────────────────────
if [[ "$NO_BUILD" -eq 0 ]]; then
  echo "== build-linux =="
  (cd "$ROOT_DIR" && make build-linux) || {
    echo "WARN: make build-linux failed; continuing if existing bin-linux is acceptable."
  }
else
  echo "== skipping build (--no-build) =="
fi

# ── Generate source archive ─────────────────────────────────────────────────
echo "== generate source archive =="
(cd "$ROOT_DIR" && ./scripts/generate-source-archive.sh)
need_file "$ARCHIVE"

# ── Helper: SSH/SCP to hub ───────────────────────────────────────────────────
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)

hub_scp() {
  if [[ -n "$HUB_KEY" ]]; then
    scp -i "$HUB_KEY" "${SSH_OPTS[@]}" "$@"
  else
    sshpass -p "$HUB_PASS" scp "${SSH_OPTS[@]}" "$@"
  fi
}

hub_ssh() {
  if [[ -n "$HUB_KEY" ]]; then
    ssh -i "$HUB_KEY" "${SSH_OPTS[@]}" "$@"
  else
    sshpass -p "$HUB_PASS" ssh "${SSH_OPTS[@]}" "$@"
  fi
}

# ── Upload to hub ────────────────────────────────────────────────────────────
echo "== upload archive + extract script + conf to hub ($HUB_HOST) =="
hub_scp "$ARCHIVE" "$EXTRACT_SCRIPT" "$CONF" "$HUB_HOST":/tmp/

# ── Remote: fan-out + extract + rolling upgrade ─────────────────────────────
echo "== fan-out + extract + rolling upgrade from hub =="

hub_ssh "$HUB_HOST" "DEPLOY_ENV=$ENV HUB_IDX=$HUB_IDX bash -s" <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

TAR=/tmp/network-source.tar.gz
EX=/tmp/extract-deploy.sh
CONF=/tmp/remote-nodes.conf

[[ -f "$TAR" ]] || { echo "Missing $TAR on hub"; exit 2; }
[[ -f "$EX" ]]  || { echo "Missing $EX on hub"; exit 2; }
[[ -f "$CONF" ]] || { echo "Missing $CONF on hub"; exit 2; }
chmod +x "$EX" || true

# Parse conf file on the hub — same format as local
hosts=()
passes=()
idx=0
hub_host=""
hub_pass=""

while IFS='|' read -r env host pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "$DEPLOY_ENV" ]] && continue

  if [[ $idx -eq $HUB_IDX ]]; then
    hub_host="$host"
    hub_pass="$pass"
  else
    hosts+=("$host")
    passes+=("$pass")
  fi
  ((idx++)) || true
done < "$CONF"

echo "Hub: $hub_host (this machine)"
echo "Fan-out nodes: ${#hosts[@]}"

# Install sshpass on hub if needed
if [[ ${#hosts[@]} -gt 0 ]] && ! command -v sshpass >/dev/null 2>&1; then
  echo "Installing sshpass on hub..."
  printf '%s\n' "$hub_pass" | sudo -S apt-get update -y >/dev/null
  printf '%s\n' "$hub_pass" | sudo -S apt-get install -y sshpass >/dev/null
fi

echo "== fan-out: upload to ${#hosts[@]} nodes =="
for i in "${!hosts[@]}"; do
  h="${hosts[$i]}"
  p="${passes[$i]}"
  echo "  -> $h"
  sshpass -p "$p" scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "$TAR" "$EX" "$h":/tmp/
done

echo "== extract on all fan-out nodes =="
for i in "${!hosts[@]}"; do
  h="${hosts[$i]}"
  p="${passes[$i]}"
  echo "  -> $h"
  sshpass -p "$p" ssh -n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "$h" "printf '%s\n' '$p' | sudo -S bash /tmp/extract-deploy.sh >/tmp/extract.log 2>&1 && echo OK"
done

echo "== extract on hub =="
printf '%s\n' "$hub_pass" | sudo -S bash "$EX" >/tmp/extract.log 2>&1

# ── Raft state detection ──
raft_state() {
  local h="$1" p="$2"
  local cmd="curl -s http://localhost:5001/status"
  local parse_py='import sys,json; j=json.load(sys.stdin); r=j.get("store",{}).get("raft",{}); print((r.get("state") or ""), (r.get("num_peers") or 0), (r.get("voter") is True))'
  sshpass -p "$p" ssh -n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "$h" "$cmd | python3 -c '$parse_py'" 2>/dev/null || true
}

echo "== detect leader =="
leader=""
leader_pass=""

for i in "${!hosts[@]}"; do
  h="${hosts[$i]}"
  p="${passes[$i]}"
  out="$(raft_state "$h" "$p")"
  echo "  $h -> ${out:-NO_OUTPUT}"
  if [[ "$out" == Leader* ]]; then
    leader="$h"
    leader_pass="$p"
    break
  fi
done

# Check hub itself
if [[ -z "$leader" ]]; then
  hub_out="$(curl -s http://localhost:5001/status | python3 -c 'import sys,json; j=json.load(sys.stdin); r=j.get("store",{}).get("raft",{}); print((r.get("state") or ""), (r.get("num_peers") or 0), (r.get("voter") is True))' 2>/dev/null || true)"
  echo "  hub(localhost) -> ${hub_out:-NO_OUTPUT}"
  if [[ "$hub_out" == Leader* ]]; then
    leader="HUB"
    leader_pass="$hub_pass"
  fi
fi

if [[ -z "$leader" ]]; then
  echo "No leader detected. Aborting before upgrades."
  exit 3
fi
echo "Leader: $leader"

upgrade_one() {
  local h="$1" p="$2"
  echo "== upgrade $h =="
  sshpass -p "$p" ssh -n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "$h" "printf '%s\n' '$p' | sudo -S orama prod stop && printf '%s\n' '$p' | sudo -S orama upgrade --no-pull --pre-built --restart"
}

upgrade_hub() {
  echo "== upgrade hub (localhost) =="
  printf '%s\n' "$hub_pass" | sudo -S orama prod stop
  printf '%s\n' "$hub_pass" | sudo -S orama upgrade --no-pull --pre-built --restart
}

echo "== rolling upgrade (followers first) =="
for i in "${!hosts[@]}"; do
  h="${hosts[$i]}"
  p="${passes[$i]}"
  [[ "$h" == "$leader" ]] && continue
  upgrade_one "$h" "$p"
done

# Upgrade hub if not the leader
if [[ "$leader" != "HUB" ]]; then
  upgrade_hub
fi

# Upgrade leader last
echo "== upgrade leader last =="
if [[ "$leader" == "HUB" ]]; then
  upgrade_hub
else
  upgrade_one "$leader" "$leader_pass"
fi

# Clean up conf from hub
rm -f "$CONF"

echo "Done."
REMOTE

echo "== complete =="
