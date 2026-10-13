package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultTargetBlockGasFraction is the genesis default for Params.TargetBlockGasFraction: 50%,
// per plans/open-network/track-c-chain.md C2's EIP-1559-style base fee.
const DefaultTargetBlockGasFraction = "0.5"

// DefaultMaxBaseFeeChangeFraction is the genesis default for Params.MaxBaseFeeChangeFraction:
// 12.5% per block, the largest single-block move the base fee can make.
const DefaultMaxBaseFeeChangeFraction = "0.125"

// DefaultDepositRefundFraction and DefaultDepositBurnFraction are the genesis defaults for
// Params.DepositRefundFraction/DepositBurnFraction: 99% refunded, 1% burned (C2 "State deposits").
const (
	DefaultDepositRefundFraction = "0.99"
	DefaultDepositBurnFraction   = "0.01"
)

// NewParams builds a Params from its fields, applying no defaults.
func NewParams(
	targetBlockGasFraction, maxBaseFeeChangeFraction math.LegacyDec,
	minBaseFee, initialBaseFee math.Int,
	depositRefundFraction, depositBurnFraction math.LegacyDec,
) Params {
	return Params{
		TargetBlockGasFraction:   targetBlockGasFraction,
		MaxBaseFeeChangeFraction: maxBaseFeeChangeFraction,
		MinBaseFee:               minBaseFee,
		InitialBaseFee:           initialBaseFee,
		DepositRefundFraction:    depositRefundFraction,
		DepositBurnFraction:      depositBurnFraction,
	}
}

// DefaultParams returns x/fees's genesis-default Params: a 50%-full target, a 12.5% max per-block
// base-fee move, a floor of 1 norama/gas, an initial base fee of 1 norama/gas (P2 is a placeholder
// pending G1's final sign-off - see plans/open-network.md P2), and a 99%/1% deposit refund/burn
// split.
func DefaultParams() Params {
	return NewParams(
		math.LegacyMustNewDecFromStr(DefaultTargetBlockGasFraction),
		math.LegacyMustNewDecFromStr(DefaultMaxBaseFeeChangeFraction),
		math.OneInt(),
		math.OneInt(),
		math.LegacyMustNewDecFromStr(DefaultDepositRefundFraction),
		math.LegacyMustNewDecFromStr(DefaultDepositBurnFraction),
	)
}

// Validate checks Params for internal consistency.
func (p Params) Validate() error {
	if err := validateUnitFraction("target_block_gas_fraction", p.TargetBlockGasFraction); err != nil {
		return err
	}
	if p.TargetBlockGasFraction.IsZero() {
		return fmt.Errorf("target_block_gas_fraction must be positive, got 0")
	}
	if err := validateUnitFraction("max_base_fee_change_fraction", p.MaxBaseFeeChangeFraction); err != nil {
		return err
	}
	if p.MaxBaseFeeChangeFraction.IsZero() {
		return fmt.Errorf("max_base_fee_change_fraction must be positive, got 0")
	}
	if p.MinBaseFee.IsNil() || !p.MinBaseFee.IsPositive() {
		return fmt.Errorf("min_base_fee must be a positive integer")
	}
	if p.InitialBaseFee.IsNil() || p.InitialBaseFee.LT(p.MinBaseFee) {
		return fmt.Errorf("initial_base_fee must be at least min_base_fee (%s), got %s", p.MinBaseFee, p.InitialBaseFee)
	}
	if err := validateUnitFraction("deposit_refund_fraction", p.DepositRefundFraction); err != nil {
		return err
	}
	if err := validateUnitFraction("deposit_burn_fraction", p.DepositBurnFraction); err != nil {
		return err
	}
	if !p.DepositRefundFraction.Add(p.DepositBurnFraction).Equal(math.LegacyOneDec()) {
		return fmt.Errorf(
			"deposit_refund_fraction (%s) + deposit_burn_fraction (%s) must equal exactly 1",
			p.DepositRefundFraction, p.DepositBurnFraction,
		)
	}
	return nil
}

func validateUnitFraction(name string, d math.LegacyDec) error {
	if d.IsNil() {
		return fmt.Errorf("%s must be set", name)
	}
	if d.IsNegative() || d.GT(math.LegacyOneDec()) {
		return fmt.Errorf("%s must be in [0, 1], got %s", name, d)
	}
	return nil
}
