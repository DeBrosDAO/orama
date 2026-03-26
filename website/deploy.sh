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
sshpass -p "$REMOTE_PASS" rsync -avz --delete \
  dist/ \
  "$REMOTE_USER@$REMOTE_HOST:$REMOTE_PATH/"

echo "Done. Live at https://$DOMAIN"
