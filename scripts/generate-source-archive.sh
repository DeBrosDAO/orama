#!/bin/bash
# Generates a tarball of the current codebase for deployment
# Output: /tmp/network-source.tar.gz
#
# Includes bin-linux/orama (CLI binary cross-compiled via make build-linux).
# All other binaries are built from source on the VPS during install.
#
# Usage:
#   make build-linux
#   ./scripts/generate-source-archive.sh
#   ./bin/orama install --vps-ip <ip> --nameserver --domain ...

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT="/tmp/network-source.tar.gz"

cd "$PROJECT_ROOT"

# Remove root-level binaries before archiving (they'll be rebuilt on VPS)
rm -f gateway cli node orama-cli-linux 2>/dev/null

# Verify CLI binary exists
if [ ! -f "bin-linux/orama" ]; then
    echo "Error: bin-linux/orama not found. Run 'make build-linux' first."
    exit 1
fi

echo "Generating source archive (with CLI binary)..."

tar czf "$OUTPUT" \
    --exclude='.git' \
    --exclude='node_modules' \
    --exclude='*.log' \
    --exclude='.DS_Store' \
    --exclude='bin/' \
    --exclude='dist/' \
    --exclude='coverage/' \
    --exclude='.claude/' \
    --exclude='testdata/' \
    --exclude='examples/' \
    --exclude='*.tar.gz' \
    .

echo "Archive created: $OUTPUT"
echo "Size: $(du -h $OUTPUT | cut -f1)"
echo "Includes CLI binary: bin-linux/orama"
