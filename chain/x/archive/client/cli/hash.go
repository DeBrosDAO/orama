package cli

import (
	"encoding/hex"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

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
