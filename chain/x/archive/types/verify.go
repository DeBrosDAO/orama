package types

import (
	"bytes"
	"fmt"

	"github.com/cometbft/cometbft/crypto/merkle"
)

// MerkleRoot is the CometBFT block-hash Merkle root of blockHashes, in height
// order. Each entry is a block hash (Header.Hash), not the raw header bytes.
func MerkleRoot(blockHashes [][]byte) []byte {
	return merkle.HashFromByteSlices(blockHashes)
}

// VerifyBundle checks that blockHashes produce merkleRoot.
//
// blockHashes are the range's block hashes in height order, one per block.
// bundleHash is the 32-byte content hash of the bundle; it is not a leaf.
// The chain record binds that hash to merkleRoot. A mutated header changes
// its block hash and fails the root check.
func VerifyBundle(blockHashes [][]byte, bundleHash, merkleRoot []byte) error {
	if err := ValidateHash("bundle_hash", bundleHash); err != nil {
		return err
	}
	if err := ValidateHash("merkle_root", merkleRoot); err != nil {
		return err
	}
	if len(blockHashes) == 0 {
		return fmt.Errorf("bundle has no block hashes")
	}
	for i, hash := range blockHashes {
		if err := ValidateHash(fmt.Sprintf("block hash %d", i), hash); err != nil {
			return err
		}
	}
	if !bytes.Equal(MerkleRoot(blockHashes), merkleRoot) {
		return fmt.Errorf("%w", ErrWrongRoot)
	}
	return nil
}
