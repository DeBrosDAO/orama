#!/bin/bash
set -e

# Deploy CoreDNS to nameserver nodes
# Usage: ./deploy-coredns.sh <node1_ip> <node2_ip> <node3_ip> <node4_ip>

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

if [ $# -lt 4 ]; then
    echo "Usage: $0 <node1_ip> <node2_ip> <node3_ip> <node4_ip>"
    echo "Example: $0 1.2.3.4 1.2.3.5 1.2.3.6 1.2.3.7"
    exit 1
fi

NODES=("$1" "$2" "$3" "$4")
BINARY="$PROJECT_ROOT/bin/coredns-custom"
COREFILE="$PROJECT_ROOT/configs/coredns/Corefile"
SYSTEMD_SERVICE="$PROJECT_ROOT/configs/coredns/coredns.service"

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    echo "❌ CoreDNS binary not found at $BINARY"
    echo "Run ./build-coredns.sh first"
    exit 1
fi

echo "🚀 Deploying CoreDNS to ${#NODES[@]} nodes..."
echo ""

for i in "${!NODES[@]}"; do
    node="${NODES[$i]}"
    node_num=$((i + 1))

    echo "[$node_num/4] Deploying to ns${node_num}.orama.network ($node)..."

    # Copy binary
    echo "  → Copying binary..."
    scp "$BINARY" "debros@$node:/tmp/coredns"
    ssh "debros@$node" "sudo mv /tmp/coredns /usr/local/bin/coredns && sudo chmod +x /usr/local/bin/coredns"

    # Copy Corefile
    echo "  → Copying configuration..."
    ssh "debros@$node" "sudo mkdir -p /etc/coredns"
    scp "$COREFILE" "debros@$node:/tmp/Corefile"
    ssh "debros@$node" "sudo mv /tmp/Corefile /etc/coredns/Corefile"

    # Copy systemd service
    echo "  → Installing systemd service..."
    scp "$SYSTEMD_SERVICE" "debros@$node:/tmp/coredns.service"
    ssh "debros@$node" "sudo mv /tmp/coredns.service /etc/systemd/system/coredns.service"

    # Start service
    echo "  → Starting CoreDNS..."
    ssh "debros@$node" "sudo systemctl daemon-reload"
    ssh "debros@$node" "sudo systemctl enable coredns"
    ssh "debros@$node" "sudo systemctl restart coredns"

    # Check status
    echo "  → Checking status..."
    if ssh "debros@$node" "sudo systemctl is-active --quiet coredns"; then
        echo "  ✅ CoreDNS running on ns${node_num}.orama.network"
    else
        echo "  ❌ CoreDNS failed to start on ns${node_num}.orama.network"
        echo "  Check logs: ssh debros@$node sudo journalctl -u coredns -n 50"
    fi

    echo ""
done

echo "✅ Deployment complete!"
echo ""
echo "Next steps:"
echo "  1. Test DNS resolution: dig @${NODES[0]} test.orama.network"
echo "  2. Update registrar NS records (ONLY after testing):"
echo "     NS    orama.network.    ns1.orama.network."
echo "     NS    orama.network.    ns2.orama.network."
echo "     NS    orama.network.    ns3.orama.network."
echo "     NS    orama.network.    ns4.orama.network."
echo "     A     ns1.orama.network. ${NODES[0]}"
echo "     A     ns2.orama.network. ${NODES[1]}"
echo "     A     ns3.orama.network. ${NODES[2]}"
echo "     A     ns4.orama.network. ${NODES[3]}"
echo ""
