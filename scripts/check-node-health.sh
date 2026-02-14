#!/bin/bash
# Check health of an Orama Network node via SSH
#
# Usage: ./scripts/check-node-health.sh <user@ip> <password> [label]
# Example: ./scripts/check-node-health.sh ubuntu@57.128.223.92 '@5YnN5wIqYnyJ4' Hermes

if [ $# -lt 2 ]; then
    echo "Usage: $0 <user@ip> <password> [label]"
    echo "Example: $0 ubuntu@1.2.3.4 'mypassword' MyNode"
    exit 1
fi

USERHOST="$1"
PASS="$2"
LABEL="${3:-$USERHOST}"

echo "════════════════════════════════════════"
echo "  Node Health: $LABEL ($USERHOST)"
echo "════════════════════════════════════════"
echo ""

sshpass -p "$PASS" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 "$USERHOST" "bash -s" <<'REMOTE'

WG_IP=$(ip -4 addr show wg0 2>/dev/null | grep -oP 'inet \K[0-9.]+' || true)

# 1. Services
echo "── Services ──"
for svc in orama-node orama-ipfs orama-ipfs-cluster orama-olric orama-anyone-relay orama-anyone-client coredns caddy; do
    status=$(systemctl is-active "$svc" 2>/dev/null || true)
    case "$status" in
        active)     mark="✓";;
        inactive)   mark="·";;
        activating) mark="~";;
        *)          mark="✗";;
    esac
    printf "  %s %-25s %s\n" "$mark" "$svc" "$status"
done
echo ""

# 2. WireGuard
echo "── WireGuard ──"
if [ -n "$WG_IP" ]; then
    echo "  IP: $WG_IP"
    PEERS=$(sudo wg show wg0 2>/dev/null | grep -c '^peer:' || echo 0)
    echo "  Peers: $PEERS"
    sudo wg show wg0 2>/dev/null | grep -A2 '^peer:' | grep -E 'endpoint|latest handshake' | while read -r line; do
        echo "    $line"
    done
else
    echo "  not configured"
fi
echo ""

# 3. RQLite (HTTP API on port 5001)
echo "── RQLite ──"
RQLITE_ADDR=""
for addr in "${WG_IP}:5001" "localhost:5001"; do
    if curl -sf "http://${addr}/nodes" >/dev/null 2>&1; then
        RQLITE_ADDR="$addr"
        break
    fi
done
if [ -n "$RQLITE_ADDR" ]; then
    # Get node state from status
    STATE=$(curl -sf "http://${RQLITE_ADDR}/status" 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(d.get('store',{}).get('raft',{}).get('state','?'))
" 2>/dev/null || echo "?")
    echo "  This node: $STATE"
    # Get cluster nodes
    curl -sf "http://${RQLITE_ADDR}/nodes" 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
for addr,info in sorted(d.items()):
    r = 'ok' if info.get('reachable') else 'UNREACHABLE'
    l = ' (LEADER)' if info.get('leader') else ''
    v = 'voter' if info.get('voter') else 'non-voter'
    print('  ' + addr + ': ' + r + ', ' + v + l)
print('  Total: ' + str(len(d)) + ' nodes')
" 2>/dev/null || echo "  (parse error)"
else
    echo "  not responding"
fi
echo ""

# 4. IPFS
echo "── IPFS ──"
PEERS=$(IPFS_PATH=/opt/orama/.orama/data/ipfs/repo /usr/local/bin/ipfs swarm peers 2>/dev/null)
if [ -n "$PEERS" ]; then
    COUNT=$(echo "$PEERS" | wc -l)
    echo "  Connected peers: $COUNT"
    echo "$PEERS" | while read -r addr; do echo "    $addr"; done
else
    echo "  no peers connected"
fi
echo ""

# 5. Gateway
echo "── Gateway ──"
GW=$(curl -sf http://localhost:6001/health 2>/dev/null)
if [ -n "$GW" ]; then
    echo "$GW" | python3 -c "
import sys,json
d=json.load(sys.stdin)
print('  Status: ' + d.get('status','?'))
srv=d.get('server',{})
print('  Uptime: ' + srv.get('uptime','?'))
cli=d.get('client',{})
if cli:
    checks=cli.get('checks',{})
    for k,v in checks.items():
        print('  ' + k + ': ' + str(v))
" 2>/dev/null || echo "  responding (parse error)"
else
    echo "  not responding"
fi
echo ""

# 6. Olric
echo "── Olric ──"
if systemctl is-active orama-olric &>/dev/null; then
    echo "  service: active"
    # Olric doesn't have a simple HTTP health endpoint; just check the process
    OLRIC_PID=$(pgrep -f olric-server || true)
    if [ -n "$OLRIC_PID" ]; then
        echo "  pid: $OLRIC_PID"
        echo "  listening: $(sudo ss -tlnp 2>/dev/null | grep olric | awk '{print $4}' | tr '\n' ' ')"
    fi
else
    echo "  not running"
fi
echo ""

# 7. Resources
echo "── Resources ──"
echo "  RAM:  $(free -h | awk '/Mem:/{print $3"/"$2}')"
echo "  Disk: $(df -h / | awk 'NR==2{print $3"/"$2" ("$5" used)"}')"
echo ""

REMOTE

echo "════════════════════════════════════════"
