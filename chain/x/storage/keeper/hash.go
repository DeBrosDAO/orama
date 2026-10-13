package keeper

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

func hashStep(seed []byte, i uint64) []byte {
	h := sha256.New()
	h.Write(seed)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], i)
	h.Write(b[:])
	return h.Sum(nil)
}

func modHash(sum []byte, count uint64) uint64 {
	n := new(big.Int).SetBytes(sum)
	n.Mod(n, new(big.Int).SetUint64(count))
	return n.Uint64()
}
