#!/usr/bin/env bash
#
# Clean all testnet nodes for fresh reinstall.
# Preserves Anyone relay keys (/var/lib/anon/) for --anyone-migrate.
# DOES NOT TOUCH DEVNET NODES.
#
# Usage: scripts/clean-testnet.sh [--nuclear]
#   --nuclear  Also remove shared binaries (rqlited, ipfs, coredns, caddy, etc.)
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$ROOT_DIR/scripts/remote-nodes.conf"

[[ -f "$CONF" ]] || { echo "ERROR: Missing $CONF"; exit 1; }
command -v sshpass >/dev/null 2>&1 || { echo "ERROR: sshpass not installed (brew install sshpass / apt install sshpass)"; exit 1; }

NUCLEAR=false
[[ "${1:-}" == "--nuclear" ]] && NUCLEAR=true

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR -o PubkeyAuthentication=no)

# ── Cleanup script (runs as root on each remote node) ─────────────────────
# Uses a quoted heredoc so NO local variable expansion happens.
# This script is uploaded to /tmp/orama-clean.sh and executed remotely.
CLEANUP_SCRIPT=$(cat <<'SCRIPT_END'
#!/bin/bash
set -e
export DEBIAN_FRONTEND=noninteractive

echo "  Stopping services..."
systemctl stop orama-node orama-gateway orama-ipfs orama-ipfs-cluster orama-olric orama-anyone-relay orama-anyone-client coredns caddy 2>/dev/null || true
systemctl disable orama-node orama-gateway orama-ipfs orama-ipfs-cluster orama-olric orama-anyone-relay orama-anyone-client coredns caddy 2>/dev/null || true

echo "  Removing systemd service files..."
rm -f /etc/systemd/system/orama-*.service
rm -f /etc/systemd/system/coredns.service
rm -f /etc/systemd/system/caddy.service
rm -f /etc/systemd/system/orama-deploy-*.service
systemctl daemon-reload

echo "  Tearing down WireGuard..."
systemctl stop wg-quick@wg0 2>/dev/null || true
wg-quick down wg0 2>/dev/null || true
systemctl disable wg-quick@wg0 2>/dev/null || true
rm -f /etc/wireguard/wg0.conf

echo "  Resetting UFW firewall..."
ufw --force reset
ufw allow 22/tcp
ufw --force enable

echo "  Removing orama data..."
rm -rf /opt/orama

echo "  Removing legacy user and data..."
userdel -r orama 2>/dev/null || true
rm -rf /home/orama

echo "  Removing sudoers files..."
rm -f /etc/sudoers.d/orama-access
rm -f /etc/sudoers.d/orama-deployments
rm -f /etc/sudoers.d/orama-wireguard

echo "  Removing CoreDNS and Caddy configs..."
rm -rf /etc/coredns
rm -rf /etc/caddy
rm -rf /var/lib/caddy

echo "  Cleaning temp files..."
rm -f /tmp/orama /tmp/network-source.tar.gz /tmp/network-source.zip
rm -rf /tmp/network-extract /tmp/coredns-build /tmp/caddy-build

# Nuclear: also remove shared binaries
if [ "${1:-}" = "--nuclear" ]; then
  echo "  Removing shared binaries (nuclear)..."
  rm -f /usr/local/bin/rqlited
  rm -f /usr/local/bin/ipfs
  rm -f /usr/local/bin/ipfs-cluster-service
  rm -f /usr/local/bin/olric-server
  rm -f /usr/local/bin/coredns
  rm -f /usr/local/bin/xcaddy
  rm -f /usr/bin/caddy
  rm -f /usr/local/bin/orama
fi

# Verify Anyone relay keys are preserved
if [ -d /var/lib/anon/keys ]; then
  echo "  Anyone relay keys PRESERVED at /var/lib/anon/keys"
  if [ -f /var/lib/anon/fingerprint ]; then
    fp=$(cat /var/lib/anon/fingerprint 2>/dev/null || true)
    echo "  Relay fingerprint: $fp"
  fi
  if [ -f /var/lib/anon/wallet ]; then
    wallet=$(cat /var/lib/anon/wallet 2>/dev/null || true)
    echo "  Relay wallet: $wallet"
  fi
else
  echo "  WARNING: No Anyone relay keys found at /var/lib/anon/"
fi

echo "  DONE"
SCRIPT_END
)

# ── Parse testnet nodes only ──────────────────────────────────────────────
hosts=()
passes=()
users=()

while IFS='|' read -r env hostspec pass role key; do
  [[ -z "$env" || "$env" == \#* ]] && continue
  env="${env%%#*}"
  env="$(echo "$env" | xargs)"
  [[ "$env" != "testnet" ]] && continue

  hosts+=("$hostspec")
  passes+=("$pass")
  users+=("${hostspec%%@*}")
done < "$CONF"

if [[ ${#hosts[@]} -eq 0 ]]; then
  echo "ERROR: No testnet nodes found in $CONF"
  exit 1
fi

echo "== clean-testnet.sh — ${#hosts[@]} testnet nodes =="
for i in "${!hosts[@]}"; do
  echo "  [$((i+1))] ${hosts[$i]}"
done
echo ""
echo "This will CLEAN all testnet nodes (stop services, remove data)."
echo "Anyone relay keys (/var/lib/anon/) will be PRESERVED."
echo "Devnet nodes will NOT be touched."
$NUCLEAR && echo "Nuclear mode: shared binaries will also be removed."
echo ""
read -rp "Type 'yes' to continue: " confirm
if [[ "$confirm" != "yes" ]]; then
  echo "Aborted."
  exit 0
fi

# ── Execute cleanup on each node ──────────────────────────────────────────
failed=()
succeeded=0
NUCLEAR_FLAG=""
$NUCLEAR && NUCLEAR_FLAG="--nuclear"

for i in "${!hosts[@]}"; do
  h="${hosts[$i]}"
  p="${passes[$i]}"
  u="${users[$i]}"
  echo ""
  echo "== [$((i+1))/${#hosts[@]}] Cleaning $h =="

  # Step 1: Upload cleanup script
  # No -n flag here — we're piping the script content via stdin
  if ! echo "$CLEANUP_SCRIPT" | sshpass -p "$p" ssh "${SSH_OPTS[@]}" "$h" \
    "cat > /tmp/orama-clean.sh && chmod +x /tmp/orama-clean.sh" 2>&1; then
    echo "  !! FAILED to upload script to $h"
    failed+=("$h")
    continue
  fi

  # Step 2: Execute the cleanup script as root
  if [[ "$u" == "root" ]]; then
    # Root: run directly
    if ! sshpass -p "$p" ssh -n "${SSH_OPTS[@]}" "$h" \
      "bash /tmp/orama-clean.sh $NUCLEAR_FLAG; rm -f /tmp/orama-clean.sh" 2>&1; then
      echo "  !! FAILED: $h"
      failed+=("$h")
      continue
    fi
  else
    # Non-root: escape password for single-quote embedding, pipe to sudo -S
    escaped_p=$(printf '%s' "$p" | sed "s/'/'\\\\''/g")
    if ! sshpass -p "$p" ssh -n "${SSH_OPTS[@]}" "$h" \
      "printf '%s\n' '${escaped_p}' | sudo -S bash /tmp/orama-clean.sh $NUCLEAR_FLAG; rm -f /tmp/orama-clean.sh" 2>&1; then
      echo "  !! FAILED: $h"
      failed+=("$h")
      continue
    fi
  fi

  echo "  OK: $h cleaned"
  ((succeeded++)) || true
done

echo ""
echo "========================================"
echo "Cleanup complete: $succeeded succeeded, ${#failed[@]} failed"
if [[ ${#failed[@]} -gt 0 ]]; then
  echo ""
  echo "Failed nodes:"
  for f in "${failed[@]}"; do
    echo "  - $f"
  done
  echo ""
  echo "Troubleshooting:"
  echo "  1. Check connectivity: ssh <user>@<host>"
  echo "  2. Check password in remote-nodes.conf"
  echo "  3. Try cleaning manually: docs/CLEAN_NODE.md"
fi
echo ""
echo "Anyone relay keys preserved at /var/lib/anon/ on all nodes."
echo "Use --anyone-migrate during install to reuse existing relay identity."
echo "========================================"
