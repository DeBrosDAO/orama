package types

import "cosmossdk.io/math"

// SplitDeposit returns the refund and burn amounts for a released state deposit of the given
// total amount (plans/open-network/track-c-chain.md C2: "99% is refunded to earnings, and 1% is
// burned"). The two always sum to exactly amount: the burn share absorbs any flooring remainder
// from the refund share's Dec multiplication, so nothing is silently lost or gained.
func SplitDeposit(amount math.Int, p Params) (refund, burn math.Int) {
	refund = amount.ToLegacyDec().Mul(p.DepositRefundFraction).TruncateInt()
	burn = amount.Sub(refund)
	return refund, burn
}
