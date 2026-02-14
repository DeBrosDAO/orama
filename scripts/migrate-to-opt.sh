#!/usr/bin/env bash
#
# Migrate an existing node from /home/orama to /opt/orama.
#
# This is a one-time migration for nodes installed with the old architecture
# (dedicated orama user, /home/orama base). After migration, redeploy with
# the new root-based architecture.
#
# Usage:
#   scripts/migrate-to-opt.sh <user@host> <password>
#
# Example:
#   scripts/migrate-to-opt.sh root@51.195.109.238 'mypassword'
#
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "Usage: $0 <user@host> <password>"
  exit 1
fi

USERHOST="$1"
PASS="$2"

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR)

echo "== Migrating $USERHOST from /home/orama → /opt/orama =="
echo ""

sshpass -p "$PASS" ssh -n "${SSH_OPTS[@]}" "$USERHOST" 'bash -s' <<'REMOTE'
set -e

echo "1. Stopping all services..."
systemctl stop orama-node orama-gateway orama-ipfs orama-ipfs-cluster orama-olric orama-anyone-relay orama-anyone-client coredns caddy 2>/dev/null || true
systemctl disable orama-node orama-gateway orama-ipfs orama-ipfs-cluster orama-olric orama-anyone-relay orama-anyone-client coredns caddy 2>/dev/null || true

echo "2. Creating /opt/orama..."
mkdir -p /opt/orama

echo "3. Migrating data..."
if [ -d /home/orama/.orama ]; then
  cp -a /home/orama/.orama /opt/orama/
  echo "   .orama/ copied"
fi
if [ -d /home/orama/bin ]; then
  cp -a /home/orama/bin /opt/orama/
  echo "   bin/ copied"
fi
if [ -d /home/orama/src ]; then
  cp -a /home/orama/src /opt/orama/
  echo "   src/ copied"
fi

echo "4. Removing old service files..."
rm -f /etc/systemd/system/orama-*.service
rm -f /etc/systemd/system/coredns.service
rm -f /etc/systemd/system/caddy.service
systemctl daemon-reload

echo "5. Removing orama user..."
userdel -r orama 2>/dev/null || true
rm -rf /home/orama

echo "6. Removing old sudoers files..."
rm -f /etc/sudoers.d/orama-access
rm -f /etc/sudoers.d/orama-deployments
rm -f /etc/sudoers.d/orama-wireguard

echo "7. Tearing down WireGuard (will be re-created on install)..."
systemctl stop wg-quick@wg0 2>/dev/null || true
wg-quick down wg0 2>/dev/null || true
systemctl disable wg-quick@wg0 2>/dev/null || true
rm -f /etc/wireguard/wg0.conf

echo "8. Resetting UFW..."
ufw --force reset
ufw allow 22/tcp
ufw --force enable

echo "9. Cleaning temp files..."
rm -f /tmp/orama /tmp/network-source.tar.gz /tmp/network-source.zip
rm -rf /tmp/network-extract /tmp/coredns-build /tmp/caddy-build

echo ""
echo "Migration complete. Data preserved at /opt/orama/"
echo "Old /home/orama removed."
echo ""
echo "Next: redeploy with new architecture:"
echo "  ./bin/orama install --vps-ip <ip> --nameserver --domain <domain> --base-domain <domain>"
REMOTE

echo ""
echo "Done."
