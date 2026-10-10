package types

import "errors"

var (
	// ErrBundleSize means the bundle is empty, over the size limit or has too many actions.
	ErrBundleSize = errors.New("shielded bundle has an unacceptable size")
	// ErrValueBalance means the bundle's value balance has the wrong sign or size for its message.
	ErrValueBalance = errors.New("shielded bundle value balance does not match the message")
	// ErrFeeTooLow means a signer-less transfer's value balance does not cover its fee.
	ErrFeeTooLow = errors.New("shielded transfer fee is below the required fee")
	// ErrNullifierSpent means a nullifier was already spent or is already pending.
	ErrNullifierSpent = errors.New("nullifier already spent or pending")
	// ErrDuplicateNullifier means one bundle repeats a nullifier.
	ErrDuplicateNullifier = errors.New("bundle repeats a nullifier")
	// ErrAnchorUnknown means the bundle's anchor is not in the anchor window.
	ErrAnchorUnknown = errors.New("bundle anchor is not in the anchor window")
	// ErrSigner means a signer-less message names a signer other than the fixed protocol address.
	ErrSigner = errors.New("signer-less shielded transfer must name the protocol signer address")
	// ErrTxShape means a shielded message is not the only message of its tx, or a signer-less tx
	// carries a signature, a fee, a memo or the wrong gas limit.
	ErrTxShape = errors.New("shielded message has the wrong transaction shape")
	// ErrTarget means the unshield target is unknown, unlinked or missing its fields.
	ErrTarget = errors.New("unshield target is not valid")
	// ErrTargetNotLinked means the target's module has no path for it yet.
	ErrTargetNotLinked = errors.New("unshield target is not linked")
	// ErrFeeTopupCap means a fee top-up is above max_fee_topup.
	ErrFeeTopupCap = errors.New("fee top-up is above max_fee_topup")
	// ErrAmountTooSmall means the amount does not cover the per-nullifier fee.
	ErrAmountTooSmall = errors.New("amount does not cover the nullifier fees")
	// ErrNullifierStore means the nullifier database failed or disagrees with the chain state.
	ErrNullifierStore = errors.New("nullifier store error")
)
