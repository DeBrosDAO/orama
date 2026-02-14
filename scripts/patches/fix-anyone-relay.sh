#!/usr/bin/env bash
#
# Patch: Fix Anyone relay after orama upgrade.
#
# After orama upgrade, the firewall reset drops the ORPort 9001 rule because
# preferences.yaml didn't have anyone_relay=true. This patch:
#   1. Opens port 9001/tcp in UFW
#   2. Re-enables orama-anyone-relay (survives reboot)
#   3. Saves anyone_relay preference so future upgrades preserve the rule
#
# Usage:
#   scripts/patches/fix-anyone-relay.sh --devnet
#   scripts/patches/fix-anyone-relay.sh --testnet
#
set -euo pipefail

ENV=""
for arg in "$@"; do
  case "$arg" in
    --devnet)  ENV="devnet" ;;
    --testnet) ENV="testnet" ;;
    -h|--help)
      echo "Usage: scripts/patches/fix-anyone-relay.sh --devnet|--testnet"
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

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o PreferredAuthentications=publickey,password)

fix_node() {
  local user_host="$1"
  local password="$2"
  local ssh_key="$3"

  # The remote script:
  #   1. Check if anyone relay service exists, skip if not
  #   2. Open ORPort 9001 in UFW
  #   3. Enable the service (auto-start on boot)
  #   4. Update preferences.yaml with anyone_relay: true
  local cmd
  cmd=$(cat <<'REMOTE'
set -e
PREFS="/home/orama/.orama/preferences.yaml"

# Only patch nodes that have the Anyone relay service installed
if [ ! -f /etc/systemd/system/orama-anyone-relay.service ]; then
  echo "SKIP_NO_RELAY"
  exit 0
fi

# 1. Open ORPort 9001 in UFW
sudo ufw allow 9001/tcp >/dev/null 2>&1

# 2. Enable the service so it survives reboot
sudo systemctl enable orama-anyone-relay >/dev/null 2>&1

# 3. Restart the service if not running
if ! systemctl is-active --quiet orama-anyone-relay; then
  sudo systemctl start orama-anyone-relay >/dev/null 2>&1
fi

# 4. Save anyone_relay preference if missing
if [ -f "$PREFS" ]; then
  if ! grep -q "anyone_relay:" "$PREFS"; then
    echo "anyone_relay: true" | sudo tee -a "$PREFS" >/dev/null
    echo "anyone_orport: 9001" | sudo tee -a "$PREFS" >/dev/null
  elif grep -q "anyone_relay: false" "$PREFS"; then
    sudo sed -i 's/anyone_relay: false/anyone_relay: true/' "$PREFS"
    if ! grep -q "anyone_orport:" "$PREFS"; then
      echo "anyone_orport: 9001" | sudo tee -a "$PREFS" >/dev/null
    fi
  fi
fi

echo "PATCH_OK"
REMOTE
)

  local result
  if [[ -n "$ssh_key" ]]; then
    expanded_key="${ssh_key/#\~/$HOME}"
    result=$(ssh -n "${SSH_OPTS[@]}" -i "$expanded_key" "$user_host" "$cmd" 2>&1)
  else
    result=$(sshpass -p "$password" ssh -n "${SSH_OPTS[@]}" -o PubkeyAuthentication=no "$user_host" "$cmd" 2>&1)
  fi

  if echo "$result" | grep -q "PATCH_OK"; then
    echo "  OK   $user_host — UFW 9001/tcp opened, service enabled, prefs saved"
  elif echo "$result" | grep -q "SKIP_NO_RELAY"; then
    echo "  SKIP $user_host — no Anyone relay installed"
  else
    echo "  ERR  $user_host: $result"
  fi
}

# Parse ALL nodes from conf (both node and nameserver roles)
# The fix_node function skips nodes without the relay service installed
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

echo "== fix-anyone-relay ($ENV) — checking ${#HOSTS[@]} nodes =="

for i in "${!HOSTS[@]}"; do
  fix_node "${HOSTS[$i]}" "${PASSES[$i]}" "${KEYS[$i]}" &
done

wait
echo "Done."
