#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONF="$SCRIPT_DIR/remote.conf"

if [ ! -f "$CONF" ]; then
  echo "Error: remote.conf not found. Create it with REMOTE_USER, REMOTE_HOST, REMOTE_PASS, REMOTE_PATH."
  exit 1
fi

source "$CONF"

echo "Building website..."
cd "$SCRIPT_DIR"
pnpm build

echo "Deploying to $REMOTE_USER@$REMOTE_HOST:$REMOTE_PATH..."
# Force password-only auth. Without this, an ssh-agent holding several keys
# offers them all first and trips the server's MaxAuthTries ("Too many
# authentication failures") before sshpass ever sends the password.
sshpass -p "$REMOTE_PASS" rsync -avz --delete \
  -e "ssh -o PubkeyAuthentication=no -o PreferredAuthentications=password -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new" \
  dist/ \
  "$REMOTE_USER@$REMOTE_HOST:$REMOTE_PATH/"

echo "Done. Live at https://$DOMAIN"
