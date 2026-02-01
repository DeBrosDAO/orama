#!/bin/bash
# Extracts /tmp/network-source.tar.gz on a VPS and places everything correctly.
# Run as root on the target VPS.
#
# What it does:
#   1. Extracts source to /home/debros/src/
#   2. Installs CLI to /usr/local/bin/orama
#   3. If bin-linux/ is in the archive (pre-built), copies binaries to their locations:
#      - orama-node, gateway, identity, rqlite-mcp, olric-server → /home/debros/bin/
#      - coredns → /usr/local/bin/coredns
#      - caddy → /usr/bin/caddy
#
# Usage: sudo bash /home/debros/src/scripts/extract-deploy.sh
#   (or pipe via SSH: ssh root@host 'bash -s' < scripts/extract-deploy.sh)

set -e

ARCHIVE="/tmp/network-source.tar.gz"
SRC_DIR="/home/debros/src"
BIN_DIR="/home/debros/bin"

if [ ! -f "$ARCHIVE" ]; then
    echo "Error: $ARCHIVE not found"
    exit 1
fi

# Ensure debros user exists (orama install also creates it, but we need it now for chown)
if ! id -u debros &>/dev/null; then
    echo "Creating 'debros' user..."
    useradd -m -s /bin/bash debros
fi

echo "Extracting source..."
rm -rf "$SRC_DIR"
mkdir -p "$SRC_DIR" "$BIN_DIR"
tar xzf "$ARCHIVE" -C "$SRC_DIR"
id -u debros &>/dev/null && chown -R debros:debros "$SRC_DIR" || true

# Install CLI
if [ -f "$SRC_DIR/bin-linux/orama" ]; then
    cp "$SRC_DIR/bin-linux/orama" /usr/local/bin/orama
    chmod +x /usr/local/bin/orama
    echo "  ✓ CLI installed: /usr/local/bin/orama"
fi

# Place pre-built binaries if present
if [ -d "$SRC_DIR/bin-linux" ]; then
    echo "Installing pre-built binaries..."

    for bin in orama-node gateway identity rqlite-mcp olric-server orama; do
        if [ -f "$SRC_DIR/bin-linux/$bin" ]; then
            cp "$SRC_DIR/bin-linux/$bin" "$BIN_DIR/$bin"
            chmod +x "$BIN_DIR/$bin"
            echo "  ✓ $bin → $BIN_DIR/$bin"
        fi
    done

    if [ -f "$SRC_DIR/bin-linux/coredns" ]; then
        cp "$SRC_DIR/bin-linux/coredns" /usr/local/bin/coredns
        chmod +x /usr/local/bin/coredns
        echo "  ✓ coredns → /usr/local/bin/coredns"
    fi

    if [ -f "$SRC_DIR/bin-linux/caddy" ]; then
        cp "$SRC_DIR/bin-linux/caddy" /usr/bin/caddy
        chmod +x /usr/bin/caddy
        echo "  ✓ caddy → /usr/bin/caddy"
    fi

    id -u debros &>/dev/null && chown -R debros:debros "$BIN_DIR" || true
    echo "All binaries installed."
else
    echo "No pre-built binaries in archive (bin-linux/ not found)."
    echo "Install CLI manually: sudo mv /tmp/orama /usr/local/bin/orama"
fi

echo "Done. Ready for: sudo orama install --no-pull --pre-built ..."
