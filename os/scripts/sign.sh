#!/bin/bash
# Sign OramaOS image artifacts with rootwallet.
#
# Usage:
#   ./scripts/sign.sh output/orama-os-1.0.0-amd64
#
# This signs the checksum file, producing a .sig file that can be verified
# with the embedded public key on nodes.
set -euo pipefail

PREFIX="$1"

if [ -z "$PREFIX" ]; then
    echo "Usage: $0 <artifact-prefix>"
    echo "  e.g.: $0 output/orama-os-1.0.0-amd64"
    exit 1
fi

CHECKSUM_FILE="${PREFIX}.sha256"
if [ ! -f "$CHECKSUM_FILE" ]; then
    echo "Error: checksum file not found: $CHECKSUM_FILE"
    echo "Run 'make build' first."
    exit 1
fi

# Compute hash of the checksum file
HASH=$(sha256sum "$CHECKSUM_FILE" | awk '{print $1}')
echo "Signing hash: $HASH"

# Sign with rootwallet (EVM secp256k1 personal_sign)
if ! command -v rw &>/dev/null; then
    echo "Error: 'rw' (rootwallet CLI) not found in PATH"
    exit 1
fi

SIGNATURE=$(rw sign "$HASH" --chain evm 2>&1)
if [ $? -ne 0 ]; then
    echo "Error: rw sign failed: $SIGNATURE"
    exit 1
fi

# Write signature file
SIG_FILE="${PREFIX}.sig"
echo "$SIGNATURE" > "$SIG_FILE"
echo "Signature written: $SIG_FILE"

# Verify the signature
echo "Verifying signature..."
VERIFY=$(rw verify "$HASH" "$SIGNATURE" --chain evm 2>&1)
if [ $? -ne 0 ]; then
    echo "WARNING: Signature verification failed: $VERIFY"
    exit 1
fi

echo "Signature verified successfully."
echo ""
echo "Artifacts:"
echo "  Checksum:  $CHECKSUM_FILE"
echo "  Signature: $SIG_FILE"
