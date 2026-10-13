package cli

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// commitBundleFile reads the bundle file at path and returns its piece commitment.
func commitBundleFile(path string) (types.Piece, error) {
	info, err := os.Stat(path)
	if err != nil {
		return types.Piece{}, fmt.Errorf("bundle file: %w", err)
	}
	if info.IsDir() || info.Size() == 0 {
		return types.Piece{}, fmt.Errorf("bundle file %s must be a non-empty file", path)
	}
	if uint64(info.Size()) > types.MaxPieceBytesLimit {
		return types.Piece{}, fmt.Errorf("bundle file %s is %d bytes, over the %d byte limit", path, info.Size(), types.MaxPieceBytesLimit)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return types.Piece{}, fmt.Errorf("read bundle file: %w", err)
	}
	pc, err := piece.Commit(body)
	if err != nil {
		return types.Piece{}, fmt.Errorf("commit bundle file: %w", err)
	}
	return types.Piece{Root: pc.Root, RealLeafCount: pc.RealLeafCount, PaddedLeafCount: pc.PaddedLeafCount, PieceBytes: uint64(len(body))}, nil
}

func decodeHash(s string) ([]byte, error) {
	bz, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	if len(bz) != types.HashLen {
		return nil, fmt.Errorf("hash must be %d bytes, got %d", types.HashLen, len(bz))
	}
	return bz, nil
}
