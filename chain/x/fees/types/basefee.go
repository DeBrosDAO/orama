// Package types implements x/fees's pure, deterministic calculations
// (plans/open-network/track-c-chain.md C2): the EIP-1559-style base fee update and the state
// deposit refund/burn split. Both are pure functions of their arguments (no time.Now, no map
// iteration, no floating point - math.LegacyDec is exact fixed-point decimal arithmetic).
package types

import "cosmossdk.io/math"

// NextBaseFee returns the next block's per-gas-unit base fee, adjusted from current toward
// Params.TargetBlockGasFraction fullness by at most Params.MaxBaseFeeChangeFraction, and never
// below Params.MinBaseFee (plans/open-network/track-c-chain.md C2: "It adjusts toward 50%
// fullness, by at most 12.5% per block. The floor is P2").
//
// gasUsed and gasLimit describe the block the fee is being adjusted FROM (i.e. this should be
// called once per block, after that block's gas usage is known, to compute the fee that applies to
// the NEXT block). If gasLimit is 0 (no block gas limit configured), current is returned
// unchanged: there is no meaningful "fullness" to react to.
func NextBaseFee(current math.Int, gasUsed, gasLimit uint64, p Params) math.Int {
	if gasLimit == 0 {
		return current
	}

	target := math.LegacyNewDec(int64(gasLimit)).Mul(p.TargetBlockGasFraction)
	used := math.LegacyNewDec(int64(gasUsed))

	// fullnessDelta is (used-target)/target, the signed fraction by which this block's usage
	// missed or exceeded the target - the EIP-1559 error term.
	fullnessDelta := used.Sub(target).Quo(target)

	changeFraction := fullnessDelta
	if changeFraction.GT(p.MaxBaseFeeChangeFraction) {
		changeFraction = p.MaxBaseFeeChangeFraction
	}
	negMax := p.MaxBaseFeeChangeFraction.Neg()
	if changeFraction.LT(negMax) {
		changeFraction = negMax
	}

	next := current.ToLegacyDec().Mul(math.LegacyOneDec().Add(changeFraction)).TruncateInt()

	// Security review B4 ("the base fee never rises"): at a small integer base fee (notably right
	// at Params.MinBaseFee, e.g. 1 norama), a sub-100% percentage increase truncates straight back
	// to the same integer - current=1, changeFraction=0.125 gives 1*1.125=1.125, which truncates to
	// 1, not 2 - so the fee could never move up from the floor no matter how full every block was.
	// EIP-1559 itself guarantees at least a 1-unit move whenever the block was above (or below) the
	// target; mirror that here rather than letting integer truncation silently cancel a real
	// adjustment. The symmetric floor-side rule never pushes next below Params.MinBaseFee itself.
	if changeFraction.IsPositive() && next.LTE(current) {
		next = current.AddRaw(1)
	} else if changeFraction.IsNegative() && next.GTE(current) && current.GT(p.MinBaseFee) {
		next = current.SubRaw(1)
	}

	if next.LT(p.MinBaseFee) {
		next = p.MinBaseFee
	}
	return next
}
