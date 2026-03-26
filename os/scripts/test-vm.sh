#!/bin/bash
# Launch OramaOS in a QEMU VM for testing.
#
# Usage:
#   ./scripts/test-vm.sh output/orama-os.qcow2
#
# The VM runs with:
#   - 2 CPUs, 2GB RAM
#   - VirtIO disk, network
#   - Serial console (for debugging)
#   - Host network (user mode) with port forwarding:
#     - 9999 → 9999 (enrollment server)
#     - 9998 → 9998 (command receiver)
set -euo pipefail

IMAGE="${1:-output/orama-os.qcow2}"

if [ ! -f "$IMAGE" ]; then
    echo "Error: image not found: $IMAGE"
    echo "Run 'make build' first, or specify image path."
    exit 1
fi

# Create a temporary copy to avoid modifying the original
WORK_IMAGE="/tmp/orama-os-test-$(date +%s).qcow2"
cp "$IMAGE" "$WORK_IMAGE"

echo "=== OramaOS Test VM ==="
echo "Image: $IMAGE"
echo "Working copy: $WORK_IMAGE"
echo ""
echo "Port forwarding:"
echo "  localhost:9999 → VM:9999 (enrollment)"
echo "  localhost:9998 → VM:9998 (commands)"
echo ""
echo "Press Ctrl-A X to exit QEMU."
echo ""

qemu-system-x86_64 \
    -enable-kvm \
    -cpu host \
    -smp 2 \
    -m 2G \
    -drive file="$WORK_IMAGE",format=qcow2,if=virtio \
    -netdev user,id=net0,hostfwd=tcp::9999-:9999,hostfwd=tcp::9998-:9998 \
    -device virtio-net-pci,netdev=net0 \
    -nographic \
    -serial mon:stdio \
    -bios /usr/share/ovmf/OVMF.fd 2>/dev/null || \
qemu-system-x86_64 \
    -cpu max \
    -smp 2 \
    -m 2G \
    -drive file="$WORK_IMAGE",format=qcow2,if=virtio \
    -netdev user,id=net0,hostfwd=tcp::9999-:9999,hostfwd=tcp::9998-:9998 \
    -device virtio-net-pci,netdev=net0 \
    -nographic \
    -serial mon:stdio

# Clean up
rm -f "$WORK_IMAGE"
echo "Cleaned up working copy."
