package testutil

import (
	"encoding/binary"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

// Bundle describes a bundle with the canonical framing. Its proof and signatures are filler, so
// only a test verifier accepts it.
type Bundle struct {
	// Nullifiers and Commitments have one entry per action. A nil entry is filled from Seed and
	// the action index.
	Nullifiers   [][bundle.NodeLen]byte
	Commitments  [][bundle.NodeLen]byte
	ValueBalance int64
	Anchor       [bundle.NodeLen]byte
	// Seed makes generated nullifiers and commitments unique to one bundle.
	Seed byte
	// Tail is appended after the canonical bundle when non-nil; used to build malformed input.
	Tail []byte
}

// Encode returns the bundle bytes. Actions is the number of actions.
func (b Bundle) Encode(actions int) []byte {
	out := []byte{byte(actions)}
	for i := 0; i < actions; i++ {
		action := make([]byte, bundle.ActionLen)
		nf, cmx := b.nullifier(i), b.commitment(i)
		copy(action[32:], nf[:])
		copy(action[96:], cmx[:])
		out = append(out, action...)
	}
	out = append(out, 0x03) // flags
	out = binary.LittleEndian.AppendUint64(out, uint64(b.ValueBalance))
	out = append(out, b.Anchor[:]...)
	proof := bundle.ProofLen(actions)
	out = append(out, 0xfd, byte(proof), byte(proof>>8))
	out = append(out, make([]byte, proof)...)
	out = append(out, make([]byte, actions*bundle.SigLen+bundle.SigLen)...)
	return append(out, b.Tail...)
}

func (b Bundle) nullifier(i int) [bundle.NodeLen]byte {
	if i < len(b.Nullifiers) {
		return b.Nullifiers[i]
	}
	return unique(0x01, b.Seed, i)
}

func (b Bundle) commitment(i int) [bundle.NodeLen]byte {
	if i < len(b.Commitments) {
		return b.Commitments[i]
	}
	return unique(0x02, b.Seed, i)
}

func unique(kind, seed byte, i int) [bundle.NodeLen]byte {
	var n [bundle.NodeLen]byte
	n[0], n[1], n[2] = kind, seed, byte(i)
	return n
}

// Nullifier is the nullifier Encode generates for action i of a bundle with this seed.
func Nullifier(seed byte, i int) [bundle.NodeLen]byte { return unique(0x01, seed, i) }
