package types

import "errors"

var (
	// ErrRootNotInBuffer is returned when a proof or mint names a root that the
	// tree's changelog no longer holds.
	ErrRootNotInBuffer = errors.New("merkle root is not in the changelog buffer")

	// ErrStaleProof is returned when a proof matched a buffered root but the
	// leaf has been modified since that root.
	ErrStaleProof = errors.New("stale merkle proof")

	// ErrInvalidProof is returned when a proof does not hash to the root it names.
	ErrInvalidProof = errors.New("invalid merkle proof")
)
