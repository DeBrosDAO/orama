package types

import "cosmossdk.io/math"

// MaxVotingPowerShiftPerEpoch is the largest total-variation distance one
// lambda step may move the normalized voting-power distribution. CometBFT
// light clients assume less than one third of voting power is replaced
// between headers a client might skip; x/power advances lambda once per
// epoch, so that bound is enforced per epoch.
func MaxVotingPowerShiftPerEpoch() math.LegacyDec {
	return math.LegacyOneDec().QuoInt64(3)
}

// VotingPowerShift is the total-variation distance between the normalized
// power distributions at lambdaA and lambdaB, for one fixed set of bootstrap
// and ramped-capped shares. It is 0 when the two lambdas assign the same
// weights, and at most 1.
func VotingPowerShift(bootstrap, rampedCapped []math.LegacyDec, lambdaA, lambdaB math.LegacyDec) math.LegacyDec {
	a := normalizedPowerShares(bootstrap, rampedCapped, lambdaA)
	b := normalizedPowerShares(bootstrap, rampedCapped, lambdaB)
	l1 := math.LegacyZeroDec()
	for i := range a {
		delta := a[i].Sub(b[i])
		if delta.IsNegative() {
			delta = delta.Neg()
		}
		l1 = l1.Add(delta)
	}
	return l1.QuoInt64(2)
}

// ClampLambdaStep returns the largest lambda in [prev, candidate] whose
// voting-power shift from prev is at most maxShift. candidate below prev is
// returned unchanged: lambda is monotonic, and this function does not raise it.
func ClampLambdaStep(prev, candidate math.LegacyDec, bootstrap, rampedCapped []math.LegacyDec, maxShift math.LegacyDec) math.LegacyDec {
	return ClampLambdaStepBoth(prev, candidate, bootstrap, rampedCapped, rampedCapped, maxShift)
}

// ClampLambdaStepBoth is ClampLambdaStep measured on two share vectors at once:
// the ramped shares that this block will publish, and the unramped capped
// shares those validators already hold. A step is allowed only when both
// shifts stay within maxShift. Measuring the unramped stake stops a lambda
// jump that does not move this block's published weights (because the ramp is
// still zero) from showing up as a large shift once the ramp ticks.
func ClampLambdaStepBoth(prev, candidate math.LegacyDec, bootstrap, published, latent []math.LegacyDec, maxShift math.LegacyDec) math.LegacyDec {
	if !candidate.GT(prev) {
		return candidate
	}
	if shiftWithin(prev, candidate, bootstrap, published, latent, maxShift) {
		return candidate
	}

	delta := candidate.Sub(prev)
	lo := math.LegacyZeroDec()
	hi := math.LegacyOneDec()
	best := prev
	for i := 0; i < 64; i++ {
		mid := lo.Add(hi).QuoInt64(2)
		trial := prev.Add(delta.Mul(mid))
		if !shiftWithin(prev, trial, bootstrap, published, latent, maxShift) {
			hi = mid
			continue
		}
		best = trial
		lo = mid
	}
	if best.LT(prev) {
		return prev
	}
	if best.GT(candidate) {
		return prev
	}
	return best
}

func shiftWithin(prev, trial math.LegacyDec, bootstrap, published, latent []math.LegacyDec, maxShift math.LegacyDec) bool {
	if VotingPowerShift(bootstrap, published, prev, trial).GT(maxShift) {
		return false
	}
	return !VotingPowerShift(bootstrap, latent, prev, trial).GT(maxShift)
}

// normalizedPowerShares is ComputePower divided by the sum of every entry, so
// the result sums to 1 whenever any entry has positive power.
func normalizedPowerShares(bootstrap, rampedCapped []math.LegacyDec, lambda math.LegacyDec) []math.LegacyDec {
	raw := make([]math.LegacyDec, len(bootstrap))
	sum := math.LegacyZeroDec()
	for i := range bootstrap {
		capped := math.LegacyZeroDec()
		if i < len(rampedCapped) {
			capped = rampedCapped[i]
		}
		raw[i] = ComputePower(bootstrap[i], capped, lambda)
		sum = sum.Add(raw[i])
	}
	out := make([]math.LegacyDec, len(raw))
	if !sum.IsPositive() {
		for i := range out {
			out[i] = math.LegacyZeroDec()
		}
		return out
	}
	for i := range raw {
		out[i] = raw[i].Quo(sum)
	}
	return out
}
