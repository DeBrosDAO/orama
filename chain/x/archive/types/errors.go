package types

import "errors"

var (
	// ErrWrongRoot is returned when an attestation or a bundle's block hashes
	// do not match the pinned block-hash Merkle root.
	ErrWrongRoot = errors.New("wrong merkle root")
	// ErrWrongBundle is returned when the bundle CID or content hash does not
	// match the range pinned by the first attestation.
	ErrWrongBundle = errors.New("bundle does not match the pinned range")
	// ErrUnknownRange is returned when a message names a range nobody has attested.
	ErrUnknownRange = errors.New("unknown height range")
	// ErrOverlap is returned when a new range shares a height with an existing one.
	ErrOverlap = errors.New("height range overlaps an existing range")
	// ErrNotFinalized is returned when a range includes the block being executed
	// or a later one. Only already-committed heights can be archived.
	ErrNotFinalized = errors.New("height range is not finalized")
	// ErrNotArchived is returned when a caller treats a range as archived before
	// it has 3 matching attestations and 3 replica deal ids.
	ErrNotArchived = errors.New("range is not archived")
	// ErrSameOperator is returned when a second node of an operator that
	// already attested a range attests it again. Each operator counts once.
	ErrSameOperator = errors.New("operator already attested this range")
	// ErrNotArchiveDeal is returned when a replica id is not an active
	// x/storage ARCHIVE deal.
	ErrNotArchiveDeal = errors.New("not an active ARCHIVE deal")
)
