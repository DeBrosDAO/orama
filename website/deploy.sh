#!/bin/bash
# Build the site, run its tests, and publish dist/ to the web host.
#
# remote.conf (gitignored) sets:
#   REMOTE       ssh destination, e.g. root@185.185.83.89 or a ~/.ssh/config alias
#   REMOTE_PATH  directory nginx serves, e.g. /opt/orama-website
#   DOMAIN       public hostname, for the final message
#
# The build prints the investor page to a PDF with Google Chrome or Chromium
# (scripts/build-pdf.mjs); set CHROME_PATH if it is not in a standard place.
#
# Authentication is by SSH key only. The nginx site config lives in
# deploy/orama.network.nginx.conf and is installed once, not by this script.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONF="$SCRIPT_DIR/remote.conf"

if [ ! -f "$CONF" ]; then
  echo "Error: $CONF not found. Create it with REMOTE, REMOTE_PATH and DOMAIN." >&2
  exit 1
fi

# shellcheck source=/dev/null
source "$CONF"
: "${REMOTE:?remote.conf must set REMOTE}"
: "${REMOTE_PATH:?remote.conf must set REMOTE_PATH}"
: "${DOMAIN:?remote.conf must set DOMAIN}"

cd "$SCRIPT_DIR"
echo "Testing..."
pnpm test
echo "Building..."
pnpm build

echo "Publishing to $REMOTE:$REMOTE_PATH..."
ssh -o BatchMode=yes "$REMOTE" "mkdir -p '$REMOTE_PATH'"
rsync -az --delete -e "ssh -o BatchMode=yes" dist/ "$REMOTE:$REMOTE_PATH/"

echo "Done. Live at https://$DOMAIN"
