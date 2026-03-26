#!/usr/bin/env bash
#
# Patch: Open ORPort 9001 in UFW on all relay-mode nodes.
#
# The upgrade path resets UFW and rebuilds rules, but doesn't include
# port 9001 because the --anyone-relay flag isn't passed during upgrade.
# This script adds the missing rule on all relay nodes (not nameservers).
#
# Usage:
#   scripts/patches/fix-ufw-orport.sh --devnet
#   scripts/patches/fix-ufw-orport.sh --testnet
#
set -euo pipefail

ENV=""
for arg in "$@"; do
  case "$arg" in
    --devnet)  ENV="devnet" ;;
    --testnet) ENV="testnet" ;;
    -h|--help)
      echo "Usage: scripts/patches/fix-ufw-orport.sh --devnet|--testnet"
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

  local cmd="sudo ufw allow 9001/tcp >/dev/null 2>&1 && echo PATCH_OK"

  local result
  if [[ -n "$ssh_key" ]]; then
    expanded_key="${ssh_key/#\~/$HOME}"
    result=$(ssh -n "${SSH_OPTS[@]}" -i "$expanded_key" "$user_host" "$cmd" 2>&1)
  else
    result=$(sshpass -p "$password" ssh -n "${SSH_OPTS[@]}" "$user_host" "$cmd" 2>&1)
  fi

  if echo "$result" | grep -q "PATCH_OK"; then
    echo "  OK  $user_host"
  else
    echo "  ERR $user_host: $result"
  fi
}

# Parse nodes from conf — only relay nodes (role=node), skip nameservers
HOSTS=()
PASSES=()
KEYS=()

while IFS='|' read -r env host pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "$ENV" ]] && continue
  role="$(echo "$role" | xargs)"
  [[ "$role" != "node" ]] && continue  # skip nameservers
  HOSTS+=("$host")
  PASSES+=("$pass")
  KEYS+=("${key:-}")
done < "$CONF"

echo "== fix-ufw-orport ($ENV) — ${#HOSTS[@]} relay nodes =="

for i in "${!HOSTS[@]}"; do
  fix_node "${HOSTS[$i]}" "${PASSES[$i]}" "${KEYS[$i]}" &
done

wait
echo "Done."
