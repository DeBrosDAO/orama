#!/bin/bash
# Build all example functions to WASM using TinyGo
#
# Prerequisites:
# - TinyGo installed: https://tinygo.org/getting-started/install/
# - On macOS: brew install tinygo
#
# Usage: ./build.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$SCRIPT_DIR/bin"

# Check if TinyGo is installed
if ! command -v tinygo &> /dev/null; then
    echo "Error: TinyGo is not installed."
    echo "Install it with: brew install tinygo (macOS) or see https://tinygo.org/getting-started/install/"
    exit 1
fi

# Create output directory
mkdir -p "$OUTPUT_DIR"

echo "Building example functions to WASM..."
echo

# Build each function
for dir in "$SCRIPT_DIR"/*/; do
    if [ -f "$dir/main.go" ]; then
        name=$(basename "$dir")
        echo "Building $name..."
        cd "$dir"
        tinygo build -o "$OUTPUT_DIR/$name.wasm" -target wasi main.go
        echo "  -> $OUTPUT_DIR/$name.wasm"
    fi
done

echo
echo "Done! WASM files are in $OUTPUT_DIR/"
ls -lh "$OUTPUT_DIR"/*.wasm 2>/dev/null || echo "No WASM files built."

