#!/usr/bin/env bash
#
# Patch: Persist MTU = 1420 in /etc/wireguard/wg0.conf on all nodes.
#
# The WireGuard provisioner now generates configs with MTU = 1420, but
# existing nodes were provisioned without it. Some nodes default to
# MTU 65456, causing packet fragmentation and TCP retransmissions.
#
# This script adds "MTU = 1420" to wg0.conf if it's missing.
# It does NOT restart WireGuard — the live MTU is already correct.
#
# Usage:
#   scripts/patches/fix-wg-mtu.sh --devnet
#   scripts/patches/fix-wg-mtu.sh --testnet
#
set -euo pipefail

ENV=""
for arg in "$@"; do
  case "$arg" in
    --devnet)  ENV="devnet" ;;
    --testnet) ENV="testnet" ;;
    -h|--help)
      echo "Usage: scripts/patches/fix-wg-mtu.sh --devnet|--testnet"
      exit 0
      ;;
    *) echo "Unknown flag: $arg" >&2; exit 1 ;;
  esac
done

if [[ -z "$ENV" ]]; then
  echo "ERROR: specify --devnet or --testnet" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONF="$ROOT_DIR/scripts/remote-nodes.conf"
[[ -f "$CONF" ]] || { echo "ERROR: Missing $CONF" >&2; exit 1; }

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10)

fix_node() {
  local user_host="$1"
  local password="$2"
  local ssh_key="$3"

  local cmd='
    CONF=/etc/wireguard/wg0.conf
    if ! sudo test -f "$CONF"; then
      echo "SKIP_NO_CONF"
      exit 0
    fi
    if sudo grep -q "^MTU" "$CONF" 2>/dev/null; then
      echo "SKIP_ALREADY"
      exit 0
    fi
    sudo sed -i "/^ListenPort/a MTU = 1420" "$CONF"
    if sudo grep -q "^MTU = 1420" "$CONF" 2>/dev/null; then
      echo "PATCH_OK"
    else
      echo "PATCH_FAIL"
    fi
  '

  local result
  if [[ -n "$ssh_key" ]]; then
    expanded_key="${ssh_key/#\~/$HOME}"
    result=$(ssh -n "${SSH_OPTS[@]}" -i "$expanded_key" "$user_host" "$cmd" 2>&1)
  else
    result=$(sshpass -p "$password" ssh -n "${SSH_OPTS[@]}" -o PreferredAuthentications=password -o PubkeyAuthentication=no "$user_host" "$cmd" 2>&1)
  fi

  if echo "$result" | grep -q "PATCH_OK"; then
    echo "  PATCHED  $user_host"
  elif echo "$result" | grep -q "SKIP_ALREADY"; then
    echo "  OK       $user_host (MTU already set)"
  elif echo "$result" | grep -q "SKIP_NO_CONF"; then
    echo "  SKIP     $user_host (no wg0.conf)"
  else
    echo "  ERR      $user_host: $result"
  fi
}

# Parse all nodes from conf (both nameservers and regular nodes)
HOSTS=()
PASSES=()
KEYS=()

while IFS='|' read -r env host pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "$ENV" ]] && continue
  HOSTS+=("$host")
  PASSES+=("$pass")
  KEYS+=("${key:-}")
done < "$CONF"

echo "== fix-wg-mtu ($ENV) — ${#HOSTS[@]} nodes =="

for i in "${!HOSTS[@]}"; do
  fix_node "${HOSTS[$i]}" "${PASSES[$i]}" "${KEYS[$i]}" &
done

wait
echo "Done."
