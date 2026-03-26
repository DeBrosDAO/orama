#!/bin/bash
# OramaOS full image build script.
#
# Prerequisites:
#   - Go 1.23+ installed
#   - Buildroot downloaded (set BUILDROOT_SRC or it clones automatically)
#   - Host tools: genimage, qemu-img (optional), veritysetup (optional)
#
# Usage:
#   ORAMA_VERSION=1.0.0 ARCH=amd64 ./scripts/build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

VERSION="${ORAMA_VERSION:-dev}"
ARCH="${ARCH:-amd64}"
OUTPUT_DIR="$ROOT_DIR/output"
BUILDROOT_DIR="$ROOT_DIR/buildroot"
BUILDROOT_SRC="${BUILDROOT_SRC:-$ROOT_DIR/.buildroot}"
BUILDROOT_VERSION="2024.02.9"

mkdir -p "$OUTPUT_DIR"

echo "=== OramaOS Build ==="
echo "Version: $VERSION"
echo "Arch:    $ARCH"
echo "Output:  $OUTPUT_DIR"
echo ""

# --- Step 1: Cross-compile orama-agent ---
echo "--- Step 1: Building orama-agent ---"
cd "$ROOT_DIR/agent"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
    go build -ldflags "-s -w -X main.version=$VERSION" \
    -o "$OUTPUT_DIR/orama-agent" ./cmd/orama-agent/
echo "Built: orama-agent"

# --- Step 2: Collect service binaries ---
echo "--- Step 2: Collecting service binaries ---"
BINS_DIR="$OUTPUT_DIR/orama-bins"
mkdir -p "$BINS_DIR"

# Copy the agent
cp "$OUTPUT_DIR/orama-agent" "$BINS_DIR/orama-agent"

# If the main orama project has been built, collect its binaries
ORAMA_BUILD="${ORAMA_BUILD_DIR:-$ROOT_DIR/../orama}"
if [ -d "$ORAMA_BUILD/build" ]; then
    echo "Copying service binaries from $ORAMA_BUILD/build/"
    for bin in rqlited olric-server ipfs ipfs-cluster-service coredns gateway; do
        src="$ORAMA_BUILD/build/$bin"
        if [ -f "$src" ]; then
            cp "$src" "$BINS_DIR/$bin"
            echo "  Copied: $bin"
        else
            echo "  WARNING: $bin not found in $ORAMA_BUILD/build/"
        fi
    done
else
    echo "WARNING: orama build dir not found at $ORAMA_BUILD/build/"
    echo "  Service binaries must be placed in $BINS_DIR/ manually."
fi

# --- Step 3: Download Buildroot if needed ---
if [ ! -d "$BUILDROOT_SRC" ]; then
    echo "--- Step 3: Downloading Buildroot $BUILDROOT_VERSION ---"
    TARBALL="buildroot-$BUILDROOT_VERSION.tar.xz"
    wget -q "https://buildroot.org/downloads/$TARBALL" -O "/tmp/$TARBALL"
    mkdir -p "$BUILDROOT_SRC"
    tar xf "/tmp/$TARBALL" -C "$BUILDROOT_SRC" --strip-components=1
    rm "/tmp/$TARBALL"
else
    echo "--- Step 3: Using existing Buildroot at $BUILDROOT_SRC ---"
fi

# --- Step 4: Configure and build with Buildroot ---
echo "--- Step 4: Running Buildroot ---"
BUILD_OUTPUT="$OUTPUT_DIR/buildroot-build"
mkdir -p "$BUILD_OUTPUT"

# Copy our board and config files into the Buildroot tree
cp -r "$BUILDROOT_DIR/board" "$BUILDROOT_SRC/"
cp -r "$BUILDROOT_DIR/configs" "$BUILDROOT_SRC/"

# Place binaries where post_build.sh expects them
mkdir -p "$BUILD_OUTPUT/images/orama-bins"
cp "$BINS_DIR"/* "$BUILD_OUTPUT/images/orama-bins/" 2>/dev/null || true

# Set version for post_build.sh
export ORAMA_VERSION="$VERSION"
export BINARIES_DIR="$BUILD_OUTPUT/images"

# Run Buildroot
cd "$BUILDROOT_SRC"
make O="$BUILD_OUTPUT" orama_defconfig
make O="$BUILD_OUTPUT" -j"$(nproc)"

# --- Step 5: Copy final artifacts ---
echo "--- Step 5: Copying artifacts ---"
FINAL_PREFIX="orama-os-${VERSION}-${ARCH}"

if [ -f "$BUILD_OUTPUT/images/orama-os.img" ]; then
    cp "$BUILD_OUTPUT/images/orama-os.img" "$OUTPUT_DIR/${FINAL_PREFIX}.img"
    echo "Raw image:  $OUTPUT_DIR/${FINAL_PREFIX}.img"
fi

if [ -f "$BUILD_OUTPUT/images/orama-os.qcow2" ]; then
    cp "$BUILD_OUTPUT/images/orama-os.qcow2" "$OUTPUT_DIR/${FINAL_PREFIX}.qcow2"
    echo "qcow2:      $OUTPUT_DIR/${FINAL_PREFIX}.qcow2"
fi

if [ -f "$BUILD_OUTPUT/images/rootfs.roothash" ]; then
    cp "$BUILD_OUTPUT/images/rootfs.roothash" "$OUTPUT_DIR/${FINAL_PREFIX}.roothash"
    echo "Root hash:  $OUTPUT_DIR/${FINAL_PREFIX}.roothash"
fi

# Generate SHA256 checksums
cd "$OUTPUT_DIR"
sha256sum "${FINAL_PREFIX}"* > "${FINAL_PREFIX}.sha256"
echo "Checksums:  $OUTPUT_DIR/${FINAL_PREFIX}.sha256"

echo ""
echo "=== OramaOS Build Complete ==="
echo "Artifacts in $OUTPUT_DIR/"
