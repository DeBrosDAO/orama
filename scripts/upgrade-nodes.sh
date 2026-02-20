#!/bin/bash
# Rolling upgrade of nodes: runs `orama node upgrade --restart` one node at a time.
#
# Usage:
#   ./scripts/upgrade-nodes.sh --env testnet
#   ./scripts/upgrade-nodes.sh --env devnet
#   ./scripts/upgrade-nodes.sh <vps-ip> [<vps-ip2> ...]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONF="$SCRIPT_DIR/remote-nodes.conf"

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

upgrade_node() {
    local user="$1" host="$2" pass="$3"

    echo "→ Upgrading $user@$host..."

    local sudo_prefix=""
    [ "$user" != "root" ] && sudo_prefix="sudo "

    local cmd="${sudo_prefix}orama node upgrade --restart"

    if [ -n "$pass" ]; then
        sshpass -p "$pass" ssh -n -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            -o PreferredAuthentications=password -o PubkeyAuthentication=no \
            "$user@$host" "$cmd"
    else
        ssh -n -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
            "$user@$host" "$cmd"
    fi
}

if [ $# -eq 0 ]; then
    echo "Usage: $0 --env <testnet|devnet>"
    echo "       $0 <vps-ip> [<vps-ip2> ...]"
    exit 1
fi

# Count nodes
node_count=$(resolve_nodes "$@" | wc -l | tr -d ' ')
echo "Rolling upgrade: $node_count nodes (serial)"
echo ""

i=0
resolve_nodes "$@" | while IFS='|' read -r user host pass; do
    i=$((i + 1))
    echo "[$i/$node_count] $user@$host"
    upgrade_node "$user" "$host" "$pass"
    echo "  ✓ Done"
    echo ""
done

echo "Rolling upgrade complete."
