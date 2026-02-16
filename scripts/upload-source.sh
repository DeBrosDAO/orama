#!/bin/bash
# Upload and extract the source archive to one or more VPS nodes.
#
# Prerequisites:
#   make build-linux
#   ./scripts/generate-source-archive.sh
#
# Usage:
#   ./scripts/upload-source.sh <vps-ip> [<vps-ip2> ...]
#   ./scripts/upload-source.sh --env testnet    # upload to all testnet nodes
#
# After uploading, run install:
#   ./bin/orama install --vps-ip <ip> --nameserver --domain ...

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVE="/tmp/network-source.tar.gz"
CONF="$SCRIPT_DIR/remote-nodes.conf"

if [ ! -f "$ARCHIVE" ]; then
    echo "Error: $ARCHIVE not found"
    echo "Run: make build-linux && ./scripts/generate-source-archive.sh"
    exit 1
fi

# Resolve VPS list from --env flag or direct IPs
resolve_nodes() {
    if [ "$1" = "--env" ] && [ -n "$2" ] && [ -f "$CONF" ]; then
        grep "^$2|" "$CONF" | while IFS='|' read -r env userhost pass role; do
            local user="${userhost%%@*}"
            local host="${userhost##*@}"
            echo "$user|$host|$pass"
        done
        return
    fi

    # Direct IPs — look up credentials from conf
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
        # Fallback: prompt for credentials
        echo "ubuntu|$ip|"
    done
}

upload_to_node() {
    local user="$1" host="$2" pass="$3"

    echo "→ Uploading to $user@$host..."

    # Upload archive
    if [ -n "$pass" ]; then
        sshpass -p "$pass" scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$ARCHIVE" "$user@$host:/tmp/network-source.tar.gz"
    else
        scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            "$ARCHIVE" "$user@$host:/tmp/network-source.tar.gz"
    fi

    # Extract on VPS
    local sudo_prefix=""
    [ "$user" != "root" ] && sudo_prefix="sudo "

    local extract_cmd="${sudo_prefix}bash -c 'rm -rf /opt/orama/src && mkdir -p /opt/orama/src /opt/orama/bin && tar xzf /tmp/network-source.tar.gz -C /opt/orama/src 2>/dev/null && if [ -f /opt/orama/src/bin-linux/orama ]; then cp /opt/orama/src/bin-linux/orama /usr/local/bin/orama && chmod +x /usr/local/bin/orama; fi && echo \"  ✓ Extracted (\$(ls /opt/orama/src/ | wc -l) files)\"'"

    if [ -n "$pass" ]; then
        sshpass -p "$pass" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" "$extract_cmd"
    else
        ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            "$user@$host" "$extract_cmd"
    fi
}

# Main
if [ $# -eq 0 ]; then
    echo "Usage: $0 <vps-ip> [<vps-ip2> ...]"
    echo "       $0 --env testnet"
    exit 1
fi

echo "Source archive: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"
echo ""

resolve_nodes "$@" | while IFS='|' read -r user host pass; do
    upload_to_node "$user" "$host" "$pass"
    echo ""
done

echo "Done. Now run: ./bin/orama install --vps-ip <ip> ..."
