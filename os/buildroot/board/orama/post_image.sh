#!/bin/bash
# OramaOS post-image script.
# Runs after rootfs image is created. Sets up dm-verity and final disk image.
# $BINARIES_DIR contains the built images (rootfs.squashfs, bzImage, etc.)
set -euo pipefail

BINARIES_DIR="$1"
BOARD_DIR="$(dirname "$0")"

echo "=== OramaOS post_image.sh ==="

# --- Generate dm-verity hash tree for rootfs ---
ROOTFS="$BINARIES_DIR/rootfs.squashfs"
VERITY_HASH="$BINARIES_DIR/rootfs.verity"
VERITY_TABLE="$BINARIES_DIR/rootfs.verity.table"

if command -v veritysetup &>/dev/null; then
    echo "Generating dm-verity hash tree..."
    veritysetup format "$ROOTFS" "$VERITY_HASH" > "$VERITY_TABLE"
    ROOT_HASH=$(grep "Root hash:" "$VERITY_TABLE" | awk '{print $3}')
    echo "dm-verity root hash: $ROOT_HASH"
    echo "$ROOT_HASH" > "$BINARIES_DIR/rootfs.roothash"
else
    echo "WARNING: veritysetup not found, skipping dm-verity (dev build only)"
fi

# --- Generate partition image using genimage ---
if [ -f "$BOARD_DIR/genimage.cfg" ]; then
    GENIMAGE_TMP="$BINARIES_DIR/genimage.tmp"
    rm -rf "$GENIMAGE_TMP"
    genimage \
        --rootpath "$TARGET_DIR" \
        --tmppath "$GENIMAGE_TMP" \
        --inputpath "$BINARIES_DIR" \
        --outputpath "$BINARIES_DIR" \
        --config "$BOARD_DIR/genimage.cfg"
    rm -rf "$GENIMAGE_TMP"
    echo "Disk image generated: $BINARIES_DIR/orama-os.img"
fi

# --- Convert to qcow2 for cloud deployment ---
if command -v qemu-img &>/dev/null; then
    echo "Converting to qcow2..."
    qemu-img convert -f raw -O qcow2 \
        "$BINARIES_DIR/orama-os.img" \
        "$BINARIES_DIR/orama-os.qcow2"
    echo "qcow2 image: $BINARIES_DIR/orama-os.qcow2"
fi

echo "=== OramaOS post_image.sh complete ==="
