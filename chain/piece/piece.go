// Package piece is the Orama piece commitment: a binary Merkle tree over 1 KiB leaves
// (plans/open-network/track-c-chain.md C7).
//
// Leaves are padded to LeafSize with zeros. The leaf count is then padded up to a power
// of two with additional all-zero leaves. Those padding leaves are real tree nodes (proofs
// grow with the padded height, about 1.8 KB at 64 GiB) but challenges are drawn mod the
// real leaf count, so a padding leaf is never selected.
//
// Domain separation is a single leading tag byte on every SHA-256 preimage:
//
//	0x00 || "ORAMA_PIECE_EMPTY_V1"   empty piece (no leaves)
//	0x01 || leaf[1024]               leaf
//	0x02 || left[32] || right[32]    internal node
package piece

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"math/bits"
)

const (
	// LeafSize is the fixed leaf width. A proof carries the leaf, so a 64 GiB piece
	// (2^26 leaves, height 26) is 1024 + 26*32 = 1856 bytes.
	LeafSize = 1024

	// TagEmpty, TagLeaf and TagNode domain-separate the three SHA-256 preimages.
	TagEmpty byte = 0x00
	TagLeaf  byte = 0x01
	TagNode  byte = 0x02

	// EmptyLabel is hashed under TagEmpty for a zero-length piece.
	EmptyLabel = "ORAMA_PIECE_EMPTY_V1"
)

// Commitment is the piece root plus the leaf counts a verifier needs. PaddedLeafCount is
// always the next power of two at or above RealLeafCount (or 0 when the piece is empty).
type Commitment struct {
	Root            []byte
	RealLeafCount   uint64
	PaddedLeafCount uint64
}

// Proof is an inclusion proof of one leaf. Siblings run from the leaf's sibling up to the
// root, so len(Siblings) == log2(PaddedLeafCount). Index is the leaf's index in the padded
// tree; challenges only ever use indexes below RealLeafCount.
type Proof struct {
	Index    uint64
	Leaf     []byte
	Siblings [][]byte
}

// RealLeafCount returns how many 1 KiB leaves data occupies. A partial tail leaf counts.
// Zero-length data has no leaves.
func RealLeafCount(n int) uint64 {
	if n <= 0 {
		return 0
	}
	return (uint64(n) + LeafSize - 1) / LeafSize
}

// PaddedLeafCount pads a real leaf count up to a power of two. Zero stays zero.
func PaddedLeafCount(real uint64) uint64 {
	if real == 0 {
		return 0
	}
	if real&(real-1) == 0 {
		return real
	}
	return 1 << bits.Len64(real-1)
}

// ProofSize is the encoded proof byte length for a padded tree: the 1 KiB leaf plus one
// 32-byte sibling per level. It grows with the padded tree, not the real leaf count.
func ProofSize(padded uint64) (int, error) {
	if padded == 0 {
		return 0, nil
	}
	if padded&(padded-1) != 0 {
		return 0, fmt.Errorf("padded leaf count %d is not a power of two", padded)
	}
	return LeafSize + bits.TrailingZeros64(padded)*32, nil
}

// EmptyRoot is the commitment root of a zero-length piece.
func EmptyRoot() []byte {
	h := sha256.New()
	h.Write([]byte{TagEmpty})
	h.Write([]byte(EmptyLabel))
	return h.Sum(nil)
}

// Commit builds the piece commitment of data.
func Commit(data []byte) (Commitment, error) {
	root, _, real, padded, err := build(data)
	if err != nil {
		return Commitment{}, err
	}
	return Commitment{Root: root, RealLeafCount: real, PaddedLeafCount: padded}, nil
}

// Prove returns an inclusion proof for leaf index. index may address a padding leaf
// (callers that draw challenges must not); the chain never does, because LeafIndex mods
// by the real count.
func Prove(data []byte, index uint64) (Proof, error) {
	_, levels, real, padded, err := build(data)
	if err != nil {
		return Proof{}, err
	}
	if padded == 0 {
		return Proof{}, errors.New("empty piece has no leaves to prove")
	}
	if index >= padded {
		return Proof{}, fmt.Errorf("leaf index %d is outside the padded tree (%d)", index, padded)
	}
	leaf := leafBytes(data, index, real)
	siblings := make([][]byte, 0, len(levels)-1)
	idx := index
	for level := 0; level < len(levels)-1; level++ {
		sib := idx ^ 1
		siblings = append(siblings, append([]byte(nil), levels[level][sib]...))
		idx /= 2
	}
	return Proof{Index: index, Leaf: leaf, Siblings: siblings}, nil
}

// Verify checks proof against commitment. A padding-leaf proof verifies; callers that
// enforce challenges must reject proof.Index >= commitment.RealLeafCount themselves
// (see VerifyChallenge).
func Verify(c Commitment, proof Proof) error {
	if err := validateShape(c, proof); err != nil {
		return err
	}
	got := fold(proof)
	if len(c.Root) != sha256.Size || !bytesEqual(got, c.Root) {
		return errors.New("piece proof root mismatch")
	}
	return nil
}

// VerifyChallenge verifies proof and rejects padding leaves. The chain uses this.
func VerifyChallenge(c Commitment, proof Proof) error {
	if c.RealLeafCount == 0 {
		return errors.New("empty piece cannot be challenged")
	}
	if proof.Index >= c.RealLeafCount {
		return fmt.Errorf("challenge index %d is a padding leaf (real count %d)", proof.Index, c.RealLeafCount)
	}
	return Verify(c, proof)
}

// LeafIndex draws a challenge index in [0, realLeafCount). The full SHA-256 digest is
// reduced mod realLeafCount, so a padding leaf is never returned. seed is hashed again
// here so callers can pass a domain-separated preimage.
func LeafIndex(seed []byte, realLeafCount uint64) (uint64, error) {
	if realLeafCount == 0 {
		return 0, errors.New("cannot draw a challenge from an empty piece")
	}
	sum := sha256.Sum256(seed)
	n := new(big.Int).SetBytes(sum[:])
	n.Mod(n, new(big.Int).SetUint64(realLeafCount))
	return n.Uint64(), nil
}

func validateShape(c Commitment, proof Proof) error {
	if c.PaddedLeafCount != PaddedLeafCount(c.RealLeafCount) {
		return fmt.Errorf("padded leaf count %d does not match real count %d", c.PaddedLeafCount, c.RealLeafCount)
	}
	if c.PaddedLeafCount == 0 {
		return errors.New("empty piece has no proof")
	}
	if proof.Index >= c.PaddedLeafCount {
		return fmt.Errorf("proof index %d is outside the padded tree (%d)", proof.Index, c.PaddedLeafCount)
	}
	if len(proof.Leaf) != LeafSize {
		return fmt.Errorf("proof leaf is %d bytes, want %d", len(proof.Leaf), LeafSize)
	}
	wantSiblings := bits.TrailingZeros64(c.PaddedLeafCount)
	if len(proof.Siblings) != wantSiblings {
		return fmt.Errorf("proof has %d siblings, padded tree wants %d", len(proof.Siblings), wantSiblings)
	}
	for i, sib := range proof.Siblings {
		if len(sib) != sha256.Size {
			return fmt.Errorf("sibling %d is %d bytes, want %d", i, len(sib), sha256.Size)
		}
	}
	return nil
}

func fold(proof Proof) []byte {
	acc := hashLeaf(proof.Leaf)
	idx := proof.Index
	for _, sib := range proof.Siblings {
		if idx%2 == 0 {
			acc = hashNode(acc, sib)
		} else {
			acc = hashNode(sib, acc)
		}
		idx /= 2
	}
	return acc
}

// build returns the root, every tree level (leaves first), and the two counts.
func build(data []byte) (root []byte, levels [][][]byte, real, padded uint64, err error) {
	real = RealLeafCount(len(data))
	padded = PaddedLeafCount(real)
	if padded == 0 {
		return EmptyRoot(), nil, 0, 0, nil
	}
	if padded > uint64(^uint(0)>>1) {
		return nil, nil, 0, 0, fmt.Errorf("padded leaf count %d does not fit in memory", padded)
	}
	level := make([][]byte, padded)
	for i := uint64(0); i < padded; i++ {
		level[i] = hashLeaf(leafBytes(data, i, real))
	}
	levels = append(levels, level)
	for len(level) > 1 {
		next := make([][]byte, len(level)/2)
		for i := 0; i < len(next); i++ {
			next[i] = hashNode(level[2*i], level[2*i+1])
		}
		level = next
		levels = append(levels, level)
	}
	return level[0], levels, real, padded, nil
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
	h.Write([]byte{TagLeaf})
	h.Write(leaf)
	return h.Sum(nil)
}

func hashNode(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{TagNode})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var d byte
	for i := range a {
		d |= a[i] ^ b[i]
	}
	return d == 0
}
