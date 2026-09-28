// Package pieceroot is the piece-commitment root a storage client needs.
// The algorithm is the one in chain/piece. This package does not import
// chain. core/pkg/pieceroot tests check roots against chain/piece/testdata/vectors.json.
package pieceroot

import (
	"crypto/sha256"
	"fmt"
	"math/bits"
)

const (
	// LeafSize is the fixed leaf width.
	LeafSize = 1024

	tagEmpty byte = 0x00
	tagLeaf  byte = 0x01
	tagNode  byte = 0x02

	emptyLabel = "ORAMA_PIECE_EMPTY_V1"
)

// Commitment is the root and the leaf counts stored on a deal.
type Commitment struct {
	Root            []byte
	RealLeafCount   uint64
	PaddedLeafCount uint64
}

// Commit builds the piece root of data.
func Commit(data []byte) (Commitment, error) {
	root, real, padded, err := build(data)
	if err != nil {
		return Commitment{}, err
	}
	return Commitment{Root: root, RealLeafCount: real, PaddedLeafCount: padded}, nil
}

func realLeafCount(n int) uint64 {
	if n <= 0 {
		return 0
	}
	return (uint64(n) + LeafSize - 1) / LeafSize
}

func paddedLeafCount(real uint64) uint64 {
	if real == 0 {
		return 0
	}
	if real&(real-1) == 0 {
		return real
	}
	return 1 << bits.Len64(real-1)
}

func emptyRoot() []byte {
	h := sha256.New()
	h.Write([]byte{tagEmpty})
	h.Write([]byte(emptyLabel))
	return h.Sum(nil)
}

func build(data []byte) (root []byte, real, padded uint64, err error) {
	real = realLeafCount(len(data))
	padded = paddedLeafCount(real)
	if padded == 0 {
		return emptyRoot(), 0, 0, nil
	}
	if padded > uint64(^uint(0)>>1) {
		return nil, 0, 0, fmt.Errorf("padded leaf count %d does not fit in memory", padded)
	}
	level := make([][]byte, padded)
	for i := uint64(0); i < padded; i++ {
		level[i] = hashLeaf(leafBytes(data, i, real))
	}
	for len(level) > 1 {
		next := make([][]byte, len(level)/2)
		for i := 0; i < len(next); i++ {
			next[i] = hashNode(level[2*i], level[2*i+1])
		}
		level = next
	}
	return level[0], real, padded, nil
}

func leafBytes(data []byte, index, real uint64) []byte {
	buf := make([]byte, LeafSize)
	if index >= real {
		return buf
	}
	start := index * LeafSize
	if uint64(len(data)) <= start {
		return buf
	}
	end := start + LeafSize
	if end > uint64(len(data)) {
		end = uint64(len(data))
	}
	copy(buf, data[start:end])
	return buf
}

func hashLeaf(leaf []byte) []byte {
	h := sha256.New()
	h.Write([]byte{tagLeaf})
	h.Write(leaf)
	return h.Sum(nil)
}

func hashNode(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{tagNode})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}
