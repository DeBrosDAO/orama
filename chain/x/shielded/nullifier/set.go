// Package nullifier stores spent nullifiers and a running accumulator.
// Two nodes that insert the same nullifiers in the same order produce the
// same accumulator. The set is not an IAVL tree.
package nullifier

import (
	"crypto/sha256"
	"errors"
)

// ErrSpent is a nullifier that was already inserted, including earlier in
// the same block.
var ErrSpent = errors.New("nullifier already spent")

// Set is an append-only nullifier set.
type Set struct {
	spent map[[32]byte]struct{}
	acc   [32]byte
	n     int
}

// New returns an empty set. The accumulator starts at 32 zero bytes.
func New() *Set {
	return &Set{spent: make(map[[32]byte]struct{})}
}

// Accumulator is the running hash.
func (s *Set) Accumulator() [32]byte { return s.acc }

// Len is the number of spent nullifiers.
func (s *Set) Len() int { return s.n }

// Insert refuses a duplicate and folds the nullifier into the accumulator.
func (s *Set) Insert(n [32]byte) error {
	if _, ok := s.spent[n]; ok {
		return ErrSpent
	}
	s.spent[n] = struct{}{}
	s.acc = Fold(s.acc, n)
	s.n++
	return nil
}

// Fold is SHA-256(prev || nullifier).
func Fold(prev, nullifier [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write(prev[:])
	_, _ = h.Write(nullifier[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
