#!/usr/bin/env bash
#
# Patch: Fix broken logrotate config on all nodes in an environment.
#
# The `anon` apt package ships /etc/logrotate.d/anon with:
#   postrotate: invoke-rc.d anon reload
# But we use debros-anyone-relay, not the anon service. So the relay
# never gets SIGHUP after rotation, keeps writing to the old fd, and
# the new notices.log stays empty (causing false "bootstrap=0%" in inspector).
#
# This script replaces the postrotate with: killall -HUP anon
#
# Usage:
#   scripts/patches/fix-logrotate.sh --devnet
#   scripts/patches/fix-logrotate.sh --testnet
#
set -euo pipefail

ENV=""
for arg in "$@"; do
  case "$arg" in
    --devnet)  ENV="devnet" ;;
    --testnet) ENV="testnet" ;;
    -h|--help)
      echo "Usage: scripts/patches/fix-logrotate.sh --devnet|--testnet"
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

# The fixed logrotate config (base64-encoded to avoid shell escaping issues)
CONFIG_B64=$(base64 <<'EOF'
/var/log/anon/*log {
    daily
    rotate 5
    compress
    delaycompress
    missingok
    notifempty
    create 0640 debian-anon adm
    sharedscripts
    postrotate
        /usr/bin/killall -HUP anon 2>/dev/null || true
    endscript
}
EOF
)

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10)

fix_node() {
  local user_host="$1"
  local password="$2"
  local ssh_key="$3"
  local b64="$4"

  local cmd="echo '$b64' | base64 -d | sudo tee /etc/logrotate.d/anon > /dev/null && echo PATCH_OK"

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

# Parse nodes from conf
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

echo "== fix-logrotate ($ENV) — ${#HOSTS[@]} nodes =="

for i in "${!HOSTS[@]}"; do
  fix_node "${HOSTS[$i]}" "${PASSES[$i]}" "${KEYS[$i]}" "$CONFIG_B64" &
done

wait
echo "Done."
