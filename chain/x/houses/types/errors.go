package types

import "errors"

var (
	// ErrTierClosed means the proposal's tier is not open. During bootstrap
	// both tiers are closed.
	ErrTierClosed = errors.New("governance tier is closed")

	// ErrNotEligible means the signer is not in the eligible operator set.
	ErrNotEligible = errors.New("operator is not eligible")

	// ErrBondLocked means the house bond cannot move because the operator has
	// a vote on a proposal still in voting or the veto window.
	ErrBondLocked = errors.New("house bond is locked by an open vote")
)
