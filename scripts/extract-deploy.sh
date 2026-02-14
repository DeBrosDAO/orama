#!/bin/bash
# Extracts /tmp/network-source.tar.gz on a VPS and places the CLI binary.
# Run as root on the target VPS.
#
# What it does:
#   1. Extracts source to /home/orama/src/
#   2. Installs CLI to /usr/local/bin/orama
#   All other binaries are built from source during `orama install`.
#
# Usage: sudo bash /home/orama/src/scripts/extract-deploy.sh

set -e

ARCHIVE="/tmp/network-source.tar.gz"
SRC_DIR="/home/orama/src"
BIN_DIR="/home/orama/bin"

if [ ! -f "$ARCHIVE" ]; then
    echo "Error: $ARCHIVE not found"
    exit 1
fi

# Ensure orama user exists
if ! id -u orama &>/dev/null; then
    echo "Creating 'orama' user..."
    useradd -m -s /bin/bash orama
fi

echo "Extracting source..."
rm -rf "$SRC_DIR"
mkdir -p "$SRC_DIR" "$BIN_DIR"
tar xzf "$ARCHIVE" -C "$SRC_DIR"
chown -R orama:orama "$SRC_DIR" || true

# Install CLI binary
if [ -f "$SRC_DIR/bin-linux/orama" ]; then
    cp "$SRC_DIR/bin-linux/orama" /usr/local/bin/orama
    chmod +x /usr/local/bin/orama
    echo "  ✓ CLI installed: /usr/local/bin/orama"
else
    echo "  ⚠️  CLI binary not found in archive (bin-linux/orama)"
fi

chown -R orama:orama "$BIN_DIR" || true

echo "Done. Ready for: sudo orama install --vps-ip <ip> ..."
