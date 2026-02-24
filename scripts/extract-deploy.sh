#!/bin/bash
# Extracts archives and places binaries on VPS nodes.
#
# Supports two archive formats:
#   1. Binary archive (from `orama build`): contains bin/, systemd/, manifest.json
#      → Extracts to /opt/orama/, installs CLI from /opt/orama/bin/orama
#   2. Source archive (legacy): contains Go source code + bin-linux/orama
#      → Extracts to /opt/orama/src/, installs CLI from bin-linux/orama
#
# Local mode (run directly on VPS):
#   sudo bash /opt/orama/src/scripts/extract-deploy.sh
#
# Remote mode (run from dev machine):
#   ./scripts/extract-deploy.sh --env testnet
#   ./scripts/extract-deploy.sh <vps-ip> [<vps-ip2> ...]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="/opt/orama/src"
BIN_DIR="/opt/orama/bin"
CONF="$SCRIPT_DIR/remote-nodes.conf"

# Detect archive: binary archive has manifest.json at root
detect_archive() {
    local archive="$1"
    if tar tzf "$archive" 2>/dev/null | grep -q "^manifest\.json$"; then
        echo "binary"
    else
        echo "source"
    fi
}

# Find archive: check for binary archive first, then source archive
find_archive() {
    # Check for binary archive (newest orama-*-linux-*.tar.gz in /tmp)
    local binary_archive
    binary_archive=$(ls -t /tmp/orama-*-linux-*.tar.gz 2>/dev/null | head -1)
    if [ -n "$binary_archive" ]; then
        echo "$binary_archive"
        return
    fi

    # Fall back to source archive
    if [ -f "/tmp/network-source.tar.gz" ]; then
        echo "/tmp/network-source.tar.gz"
        return
    fi

    echo ""
}

# --- Local mode (no args) ---
if [ $# -eq 0 ]; then
    ARCHIVE=$(find_archive)
    if [ -z "$ARCHIVE" ]; then
        echo "Error: No archive found in /tmp/"
        echo "  Expected: /tmp/orama-*-linux-*.tar.gz (binary) or /tmp/network-source.tar.gz (source)"
        exit 1
    fi

    FORMAT=$(detect_archive "$ARCHIVE")
    echo "Archive: $ARCHIVE (format: $FORMAT)"

    if [ "$FORMAT" = "binary" ]; then
        # Binary archive → extract to /opt/orama/
        echo "Extracting binary archive..."
        mkdir -p /opt/orama
        tar xzf "$ARCHIVE" -C /opt/orama

        # Install CLI binary
        if [ -f "$BIN_DIR/orama" ]; then
            cp "$BIN_DIR/orama" /usr/local/bin/orama
            chmod +x /usr/local/bin/orama
            echo "  ✓ CLI installed: /usr/local/bin/orama"
        else
            echo "  ⚠️  CLI binary not found in archive (bin/orama)"
        fi

        echo "Done. Ready for: sudo orama node install --vps-ip <ip> ..."
    else
        # Source archive → extract to /opt/orama/src/ (legacy)
        echo "Extracting source archive..."
        rm -rf "$SRC_DIR"
        mkdir -p "$SRC_DIR" "$BIN_DIR"
        tar xzf "$ARCHIVE" -C "$SRC_DIR"

        # Install CLI binary
        if [ -f "$SRC_DIR/bin-linux/orama" ]; then
            cp "$SRC_DIR/bin-linux/orama" /usr/local/bin/orama
            chmod +x /usr/local/bin/orama
            echo "  ✓ CLI installed: /usr/local/bin/orama"
        else
            echo "  ⚠️  CLI binary not found in archive (bin-linux/orama)"
        fi

        echo "Done. Ready for: sudo orama node install --vps-ip <ip> ..."
    fi
    exit 0
fi

# --- Remote mode ---

resolve_nodes() {
    if [ "$1" = "--env" ] && [ -n "$2" ] && [ -f "$CONF" ]; then
        grep "^$2|" "$CONF" | while IFS='|' read -r env userhost pass role; do
            local user="${userhost%%@*}"
            local host="${userhost##*@}"
            echo "$user|$host|$pass"
        done
        return
    fi

    for ip in "$@"; do
        if [ -f "$CONF" ]; then
            local match
            match=$(grep "|[^|]*@${ip}|" "$CONF" | head -1)
            if [ -n "$match" ]; then
                local userhost pass
                userhost=$(echo "$match" | cut -d'|' -f2)
                pass=$(echo "$match" | cut -d'|' -f3)
                local user="${userhost%%@*}"
                echo "$user|$ip|$pass"
                continue
            fi
        fi
        echo "ubuntu|$ip|"
    done
}

extract_on_node() {
    local user="$1" host="$2" pass="$3"

    echo "→ Extracting on $user@$host..."

    local sudo_prefix=""
    [ "$user" != "root" ] && sudo_prefix="sudo "

    local extract_cmd="${sudo_prefix}bash -c 'rm -rf /opt/orama/src && mkdir -p /opt/orama/src /opt/orama/bin && tar xzf /tmp/network-source.tar.gz -C /opt/orama/src 2>/dev/null && if [ -f /opt/orama/src/bin-linux/orama ]; then cp /opt/orama/src/bin-linux/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama; fi && echo \"  ✓ Extracted (\$(ls /opt/orama/src/ | wc -l) files)\"'"

    if [ -n "$pass" ]; then
        sshpass -p "$pass" ssh -n -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" "$extract_cmd"
    else
        ssh -n -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            "$user@$host" "$extract_cmd"
    fi
}

resolve_nodes "$@" | while IFS='|' read -r user host pass; do
    extract_on_node "$user" "$host" "$pass"
    echo ""
done

echo "Done."
