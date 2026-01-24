#!/bin/bash
# Generates a tarball of the current codebase for deployment
# Output: /tmp/network-source.tar.gz
#
# Usage: ./scripts/generate-source-archive.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT="/tmp/network-source.tar.gz"

echo "Generating source archive..."

cd "$PROJECT_ROOT"

# Remove root-level binaries before archiving (they'll be rebuilt on VPS)
rm -f gateway cli node orama-cli-linux 2>/dev/null

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
