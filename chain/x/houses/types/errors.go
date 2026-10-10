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

	// ErrAdvanceRejected marks a failure that belongs to the one proposal being advanced (a staking,
	// power or operator read that failed, a tally over data that does not add up). The proposal is
	// retried in a later block, at most MaxAdvanceAttempts times. Any other failure (a collection
	// that cannot be read or decoded) fails the block.
	ErrAdvanceRejected = errors.New("proposal advance rejected")
)
