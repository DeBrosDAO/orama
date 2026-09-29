package types

import "errors"

var (
	// ErrWrongRoot is returned when an attestation or a bundle's block hashes
	// do not match the block-hash Merkle root of the winning tuple.
	ErrWrongRoot = errors.New("wrong merkle root")
	// ErrWrongBundle is returned when the bundle CID or content hash does not
	// match the tuple that won the range.
	ErrWrongBundle = errors.New("bundle does not match the pinned range")
	// ErrWrongPiece is returned when a piece commitment differs from the one the winning tuple of
	// the range carries.
	ErrWrongPiece = errors.New("piece commitment does not match the pinned range")
	// ErrPieceTooLarge is returned when a bundle file exceeds Params.MaxPieceBytes.
	ErrPieceTooLarge = errors.New("bundle file is larger than max_piece_bytes")
	// ErrQuorumPending is returned when archive deals are asked for before enough operators
	// have attested the range's bundle.
	ErrQuorumPending = errors.New("range has not reached the attestation quorum")
	// ErrUnknownRange is returned when a message names a range nobody has attested.
	ErrUnknownRange = errors.New("unknown height range")
	// ErrOverlap is returned when a new range shares a height with an existing one.
	ErrOverlap = errors.New("height range overlaps an existing range")
	// ErrNotCanonicalRange is returned when a range is not one of the fixed ranges of
	// Params.RangeBlocks heights.
	ErrNotCanonicalRange = errors.New("height range is not a canonical range")
	// ErrNotFinalized is returned when a range includes the block being executed
	// or a later one. Only already-committed heights can be archived.
	ErrNotFinalized = errors.New("height range is not finalized")
	// ErrNotArchived is returned when a caller treats a range as archived before
	// it has 3 matching attestations and 3 replica deal ids.
	ErrNotArchived = errors.New("range is not archived")
	// ErrNotArchiveDeal is returned when a replica id is not an active
	// x/storage ARCHIVE deal.
	ErrNotArchiveDeal = errors.New("not an active ARCHIVE deal")
	// ErrDealAttached is returned when a deal id is already a replica of
	// another range.
	ErrDealAttached = errors.New("deal is already attached to another range")
	// ErrDealsFull is returned when a range already has all the live deals it may have.
	ErrDealsFull = errors.New("range already has its live archive deals")
	// ErrConflictingAttestation is returned when an operator that already attested a range
	// attests a different tuple for it: an operator counts toward one tuple per range.
	ErrConflictingAttestation = errors.New("operator already attested a different tuple for this range")
	// ErrCandidatesFull is returned when a range already holds the most candidate tuples it may
	// and the attestation would start another.
	ErrCandidatesFull = errors.New("range already holds the most candidate tuples it may")
	// ErrNotAttester is returned when an operator that did not attest a range
	// tries to attach deals to it.
	ErrNotAttester = errors.New("operator did not attest this range")
)
