package types

import (
	"bytes"
	"fmt"
)

// Tuple is everything one attestation states about a range: the bundle CID, the bundle content
// hash, the block-hash Merkle root and the piece commitment of the bundle file. x/archive tallies
// attestations per tuple, so archivers that differ in any one field are not counting toward each
// other, and only a tuple that reaches the operator quorum is ever archived.
type Tuple struct {
	BundleCid  string
	BundleHash []byte
	MerkleRoot []byte
	Piece      Piece
}

// Tuple reads the tuple a MsgAttest attests.
func (m *MsgAttest) Tuple() Tuple {
	return Tuple{BundleCid: m.BundleCid, BundleHash: m.BundleHash, MerkleRoot: m.MerkleRoot, Piece: m.PieceOf()}
}

// Tuple reads the tuple of a candidate.
func (c Candidate) Tuple() Tuple {
	return Tuple{
		BundleCid: c.BundleCid, BundleHash: c.BundleHash, MerkleRoot: c.MerkleRoot,
		Piece: Piece{Root: c.PieceRoot, RealLeafCount: c.RealLeafCount, PaddedLeafCount: c.PaddedLeafCount, PieceBytes: c.PieceBytes},
	}
}

// Winner reads the winning tuple of a decided range. It is empty for a range that is not decided.
func (r RangeRecord) Winner() Tuple {
	return Tuple{BundleCid: r.BundleCid, BundleHash: r.BundleHash, MerkleRoot: r.MerkleRoot, Piece: r.PieceOf()}
}

// NewCandidate starts a candidate for t with no attesters.
func NewCandidate(t Tuple) Candidate {
	return Candidate{
		BundleCid:       t.BundleCid,
		BundleHash:      bytes.Clone(t.BundleHash),
		MerkleRoot:      bytes.Clone(t.MerkleRoot),
		PieceRoot:       bytes.Clone(t.Piece.Root),
		RealLeafCount:   t.Piece.RealLeafCount,
		PaddedLeafCount: t.Piece.PaddedLeafCount,
		PieceBytes:      t.Piece.PieceBytes,
	}
}

// Equal reports whether two tuples agree on every field.
func (t Tuple) Equal(o Tuple) bool {
	return t.BundleCid == o.BundleCid && bytes.Equal(t.BundleHash, o.BundleHash) &&
		bytes.Equal(t.MerkleRoot, o.MerkleRoot) && t.Piece.Equal(o.Piece)
}

// Mismatch names the first field o differs from t in (the Merkle root, then the bundle, then the
// piece commitment) as the matching sentinel error, and is nil when the tuples are equal.
func (t Tuple) Mismatch(o Tuple) error {
	switch {
	case !bytes.Equal(t.MerkleRoot, o.MerkleRoot):
		return ErrWrongRoot
	case t.BundleCid != o.BundleCid || !bytes.Equal(t.BundleHash, o.BundleHash):
		return ErrWrongBundle
	case !t.Piece.Equal(o.Piece):
		return ErrWrongPiece
	}
	return nil
}

// isZero reports whether t carries nothing: the winning tuple of a range that is not decided.
func (t Tuple) isZero() bool {
	return t.BundleCid == "" && len(t.BundleHash) == 0 && len(t.MerkleRoot) == 0 &&
		len(t.Piece.Root) == 0 && t.Piece.RealLeafCount == 0 && t.Piece.PaddedLeafCount == 0 && t.Piece.PieceBytes == 0
}

// Validate checks the tuple's own shape: a bundle CID, two 32-byte hashes and a well-formed piece
// commitment.
func (t Tuple) Validate() error {
	if err := ValidateBundleCID(t.BundleCid); err != nil {
		return err
	}
	if err := ValidateHash("bundle_hash", t.BundleHash); err != nil {
		return err
	}
	if err := ValidateHash("merkle_root", t.MerkleRoot); err != nil {
		return err
	}
	if err := t.Piece.Validate(); err != nil {
		return fmt.Errorf("piece commitment: %w", err)
	}
	return nil
}

// AttestedBy returns the tuple the archiver key attested for this range, and false when it has not
// attested it.
func (r RangeRecord) AttestedBy(archiver string) (Tuple, bool) {
	if r.Decided {
		for _, a := range r.Archivers {
			if a == archiver {
				return r.Winner(), true
			}
		}
		return Tuple{}, false
	}
	for _, c := range r.Candidates {
		for _, a := range c.Archivers {
			if a == archiver {
				return c.Tuple(), true
			}
		}
	}
	return Tuple{}, false
}

// Contested reports whether the range holds a tuple other than t: a decided range whose winner is
// not t, or an undecided one with a candidate that is not t.
func (r RangeRecord) Contested(t Tuple) bool {
	if r.Decided {
		return !r.Winner().Equal(t)
	}
	for _, c := range r.Candidates {
		if !c.Tuple().Equal(t) {
			return true
		}
	}
	return false
}
