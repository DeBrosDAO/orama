package policy

import (
	"errors"

	"cosmossdk.io/math"
)

// Target is where an unshield may land. Only the owner of that target may
// sign the unshield. A plain user bank account is never a target.
type Target uint8

const (
	TargetPlainUser Target = iota
	TargetContract
	TargetOwnBond
	TargetOwnNodeBond
	TargetOwnDeposit
	TargetFeeEarnings
)

// MaxFeeTopup is the per-tx cap on an unshield into the signer's own
// earnings account. That balance pays fees and cannot be sent.
// 10 ORAMA, in norama. plans/open-network.md does not fix this number;
// the constant is the genesis default.
const MaxFeeTopup int64 = 10_000_000_000

var (
	ErrForeignTarget = errors.New("unshield target is not owned by the signer")
	ErrPlainAccount  = errors.New("unshield into a user bank account is refused")
	ErrFeeTopupCap   = errors.New("fee top-up exceeds the per-tx cap")
	ErrAmount        = errors.New("amount must be positive")
)

// CheckTarget allows an unshield only to a target the signer owns, and never
// to a plain bank account. signer and owner are bech32 account strings.
func CheckTarget(kind Target, signer, owner string, amount math.Int, maxFeeTopup math.Int) error {
	if !amount.IsPositive() {
		return ErrAmount
	}
	if kind == TargetPlainUser {
		return ErrPlainAccount
	}
	if signer == "" || signer != owner {
		return ErrForeignTarget
	}
	if kind == TargetFeeEarnings && amount.GT(maxFeeTopup) {
		return ErrFeeTopupCap
	}
	return nil
}
