// Package archiver packs finalised block ranges into bundle files, attests
// each range to x/archive, and verifies a bundle against the chain.
package archiver

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// Bundle is one height range and the two hashes an attestation carries.
type Bundle struct {
	Start       int64
	End         int64
	BlockHashes [][]byte
	ContentHash []byte
	MerkleRoot  []byte
}

// Pack checks the block hashes and binds the body bytes to the Merkle root.
func Pack(start int64, blockHashes [][]byte, body []byte) (Bundle, error) {
	if start < 1 {
		return Bundle{}, errors.New("archive range must start at height 1 or later")
	}
	if len(blockHashes) == 0 {
		return Bundle{}, errors.New("archive range has no block hashes")
	}
	end := start + int64(len(blockHashes)) - 1
	sum := sha256.Sum256(body)
	root := types.MerkleRoot(blockHashes)
	if err := types.VerifyBundle(blockHashes, sum[:], root); err != nil {
		return Bundle{}, err
	}
	return Bundle{
		Start: start, End: end, BlockHashes: blockHashes,
		ContentHash: append([]byte(nil), sum[:]...),
		MerkleRoot:  append([]byte(nil), root...),
	}, nil
}

// SaveCursor records the last height a packer finished.
func SaveCursor(path string, height int64) error {
	if height < 0 {
		return fmt.Errorf("cursor height %d is negative", height)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(height))
	return writeAtomic(path, buf[:], 0o600)
}

// LoadCursor reads a cursor written by SaveCursor. A missing file is height 0.
func LoadCursor(path string) (int64, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(buf) != 8 {
		return 0, fmt.Errorf("cursor is %d bytes", len(buf))
	}
	v := binary.BigEndian.Uint64(buf)
	if v > uint64(^uint64(0)>>1) {
		return 0, errors.New("cursor does not fit in int64")
	}
	return int64(v), nil
}
